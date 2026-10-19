package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	openaiwsv2 "github.com/Wei-Shaw/sub2api/internal/service/openai_ws_v2"
	coderws "github.com/coder/websocket"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const CodexAPIResponsesBodyLimit int64 = 64 << 20

func CodexAPIRequestBodyLimit(target string) int64 {
	switch target {
	case "/v1/responses", "/codex/responses", "/backend-api/codex/responses":
		return CodexAPIResponsesBodyLimit
	default:
		return 16 << 20
	}
}

const (
	codexAPIReplayLaneLimit       = 32
	codexAPICorrelationLimitBytes = 8 << 10
)

type codexAPIReplayHistory struct {
	id    string
	input []json.RawMessage
}

type codexAPIBridgeTurn struct {
	lane        string
	input       []json.RawMessage
	complete    bool
	id          string
	collector   openAIWSToolCallReplayCollector
	outputOrder map[string]int
	unfinished  map[string]bool
	sawOutput   bool
	cancel      context.CancelFunc
}

func (t *codexAPIBridgeTurn) discardReplay() {
	t.complete = false
	t.input = nil
	t.collector = openAIWSToolCallReplayCollector{}
	t.outputOrder = nil
	t.unfinished = nil
}

type codexAPIBridgeFrame struct {
	kind       coderws.MessageType
	payload    []byte
	err        error
	turn       *codexAPIBridgeTurn
	local      bool
	keepActive bool
}

// codexAPIWSHTTPBridge owns replay only for this live client connection. The
// shared relay still owns client inspection, resource binding, usage and TTFT.
type codexAPIWSHTTPBridge struct {
	service         *OpenAIGatewayService
	account         *Account
	incoming        *http.Request
	target          string
	native          openaiwsv2.FrameConn
	ctx             context.Context
	cancel          context.CancelFunc
	frames          chan codexAPIBridgeFrame
	mu              sync.Mutex
	history         map[string]codexAPIReplayHistory
	pending         map[string]*codexAPIBridgeTurn
	httpMode        bool
	switched        time.Time
	nativeAmbiguous bool
	replayLimited   bool
}

func (s *OpenAIGatewayService) CodexAPIWebSocketTransport(ctx context.Context, native openaiwsv2.FrameConn, account *Account, incoming *http.Request, target string) openaiwsv2.FrameConn {
	if CodexAPIRequestBodyLimit(target) != CodexAPIResponsesBodyLimit || !s.openAIWSHTTPBridgeEnabled() {
		return native
	}
	ctx, cancel := context.WithCancel(ctx)
	bridge := &codexAPIWSHTTPBridge{
		service: s, account: account, incoming: incoming, target: target,
		native: native, ctx: ctx, cancel: cancel, frames: make(chan codexAPIBridgeFrame),
		history: make(map[string]codexAPIReplayHistory), pending: make(map[string]*codexAPIBridgeTurn),
	}
	go bridge.readNative()
	return bridge
}

func (b *codexAPIWSHTTPBridge) Close() error {
	b.cancel()
	b.mu.Lock()
	clear(b.history)
	for _, turn := range b.pending {
		turn.discardReplay()
	}
	clear(b.pending)
	b.mu.Unlock()
	return b.native.Close()
}

func (b *codexAPIWSHTTPBridge) readNative() {
	for {
		kind, payload, err := b.native.ReadFrame(b.ctx)
		b.mu.Lock()
		switched := b.httpMode
		b.mu.Unlock()
		if err != nil && switched {
			return
		}
		if !b.emit(codexAPIBridgeFrame{kind: kind, payload: payload, err: err}) || err != nil {
			return
		}
	}
}

func (b *codexAPIWSHTTPBridge) emit(frame codexAPIBridgeFrame) bool {
	select {
	case b.frames <- frame:
		return true
	case <-b.ctx.Done():
		return false
	}
}

func (b *codexAPIWSHTTPBridge) ReadFrame(ctx context.Context) (coderws.MessageType, []byte, error) {
	for {
		select {
		case frame := <-b.frames:
			if frame.err != nil {
				b.mu.Lock()
				switched := b.httpMode
				b.mu.Unlock()
				if switched {
					continue
				}
			}
			if frame.err == nil && !frame.local {
				b.observe(frame.payload, frame.turn, frame.keepActive)
			}
			return frame.kind, frame.payload, frame.err
		case <-ctx.Done():
			return 0, nil, ctx.Err()
		case <-b.ctx.Done():
			return 0, nil, b.ctx.Err()
		}
	}
}

func codexAPIBridgeRequestError(lane, code, message string) []byte {
	payload := buildOpenAIWSHTTPBridgeErrorEvent(http.StatusBadRequest, message)
	payload, _ = sjson.SetBytes(payload, "error.type", "invalid_request_error")
	payload, _ = sjson.SetBytes(payload, "error.code", code)
	return codexAPIEventLane(payload, lane)
}

func codexAPIEventLane(payload []byte, lane string) []byte {
	if lane != "" {
		if updated, err := sjson.SetRawBytes(payload, "stream_id", []byte(lane)); err == nil {
			return updated
		}
	}
	return payload
}

func codexAPIMessageLane(payload []byte) string {
	lane := gjson.GetBytes(payload, "stream_id")
	if lane.Type == gjson.String {
		canonical, _ := json.Marshal(lane.String())
		return string(canonical)
	}
	return lane.Raw
}

func (b *codexAPIWSHTTPBridge) WriteFrame(ctx context.Context, kind coderws.MessageType, payload []byte) error {
	write, rejection, err := b.PrepareWriteFrame(ctx, kind, payload)
	if err != nil {
		return err
	}
	if len(rejection) > 0 {
		select {
		case b.frames <- codexAPIBridgeFrame{kind: coderws.MessageText, payload: rejection, local: true}:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		case <-b.ctx.Done():
			return b.ctx.Err()
		}
	}
	return write()
}

func (b *codexAPIWSHTTPBridge) PrepareWriteFrame(ctx context.Context, kind coderws.MessageType, payload []byte) (openaiwsv2.PreparedFrameWrite, []byte, error) {
	if err := b.ctx.Err(); err != nil {
		return nil, nil, err
	}
	nativeWrite := func() error { return b.native.WriteFrame(ctx, kind, payload) }
	event := gjson.GetBytes(payload, "type").String()
	lane := codexAPIMessageLane(payload)
	previous := gjson.GetBytes(payload, "previous_response_id").String()
	b.mu.Lock()
	requiresHTTP := b.httpMode || event == "response.create" && b.service.shouldBridgeOpenAIWSHTTP(b.account, len(payload), "")
	if len(lane) > codexAPICorrelationLimitBytes || len(previous) > codexAPICorrelationLimitBytes || len(gjson.GetBytes(payload, "response_id").String()) > codexAPICorrelationLimitBytes {
		if requiresHTTP {
			b.mu.Unlock()
			return nil, codexAPIBridgeRequestError(lane, "replay_limit", "HTTP replay correlation identifiers exceed the 8 KiB limit"), nil
		}
		b.disableNativeReplay()
		b.mu.Unlock()
		return nativeWrite, nil, nil
	}
	if b.replayLimited {
		b.mu.Unlock()
		if requiresHTTP {
			return nil, codexAPIBridgeRequestError(lane, "replay_limit", "Native correlation identifiers exceeded the replay limit on this connection"), nil
		}
		return nativeWrite, nil, nil
	}
	if event == "response.cancel" || event == "response.interrupt" {
		turn := b.pending[lane]
		id := gjson.GetBytes(payload, "response_id").String()
		if lane == "" && id != "" {
			turn = nil
			for _, candidate := range b.pending {
				if candidate.id == id {
					if turn != nil {
						turn = nil
						break
					}
					turn = candidate
				}
			}
		}
		if turn != nil && turn.cancel != nil && (id == "" || id == turn.id) {
			if mode := gjson.GetBytes(payload, "mode").String(); event == "response.interrupt" && mode != "" && mode != "discard_partial_items" {
				b.mu.Unlock()
				return nil, codexAPIBridgeRequestError(lane, "unsupported_interrupt", "HTTP interruption supports discard_partial_items only"), nil
			}
			b.mu.Unlock()
			return func() error { turn.cancel(); return nil }, nil, nil
		}
	}
	if event != "response.create" {
		httpMode := b.httpMode
		b.mu.Unlock()
		if httpMode {
			return nil, codexAPIBridgeRequestError(lane, "unsupported_event", "Event is unavailable after switching this connection to HTTP"), nil
		}
		return nativeWrite, nil, nil
	}
	history, hasHistory := b.history[lane]
	hasHistory = hasHistory && history.id == previous && previous != ""
	current, exists, inputErr := openAIWSExtractNormalizedInputSequence(payload)
	if input := gjson.GetBytes(payload, "input"); input.Type == gjson.String {
		message, err := sjson.SetBytes([]byte(`{"type":"message","role":"user","content":[{"type":"input_text","text":""}]}`), "content.0.text", input.String())
		inputErr = err
		current = []json.RawMessage{message}
	}
	complete := inputErr == nil && (previous == "" || hasHistory)
	// Input views retain the wire payload. Own the retained bytes separately so
	// the connection budget does not hide a larger backing allocation.
	if complete {
		for i := range current {
			current[i] = bytes.Clone(current[i])
		}
	}
	full := current
	if complete && hasHistory {
		if !openAIWSRawItemsHasPrefix(current, history.input) {
			full = combineOpenAIWSReplayItems(history.input, current)
		}
		exists = true
	}
	fullSize := codexAPIReplayBytes(full)
	bridge := b.httpMode || b.service.shouldBridgeOpenAIWSHTTP(b.account, len(payload), "") ||
		complete && b.service.shouldBridgeOpenAIWSHTTP(b.account, fullSize, "")
	if !openAIWSPayloadBoolFromRaw(payload, "generate", true) && bridge {
		b.mu.Unlock()
		return nil, codexAPIBridgeRequestError(lane, "unsupported_warmup", "generate=false requires the native WebSocket transport"), nil
	}
	if bridge && !complete {
		b.mu.Unlock()
		return nil, codexAPIBridgeRequestError(lane, "previous_response_not_found", "Complete previous_response_id history is unavailable on this connection and stream"), nil
	}
	if bridge && b.nativeAmbiguous {
		b.mu.Unlock()
		return nil, codexAPIBridgeRequestError(lane, "incomplete_replay", "Overlapping native responses require a new connection with full input before HTTP bridging"), nil
	}
	if bridge && (len(b.pending) > 0 && !b.httpMode || b.pending[lane] != nil) {
		b.mu.Unlock()
		return nil, codexAPIBridgeRequestError(lane, "bridge_busy", "Wait for the active response before switching transport or reusing its stream"), nil
	}
	if len(b.pending)+len(b.history) >= codexAPIReplayLaneLimit && b.pending[lane] == nil && b.history[lane].id == "" {
		if bridge {
			b.mu.Unlock()
			return nil, codexAPIBridgeRequestError(lane, "replay_limit", "Connection replay stream limit reached"), nil
		}
		b.nativeAmbiguous = true
		b.mu.Unlock()
		return nativeWrite, nil, nil
	}
	if prior := b.pending[lane]; prior != nil {
		// Ambiguous overlapping native turns cannot produce a replay anchor.
		prior.discardReplay()
		b.nativeAmbiguous = true
		b.mu.Unlock()
		return nativeWrite, nil, nil
	}
	var body []byte
	target := b.target
	if bridge {
		for _, item := range full {
			if gjson.GetBytes(item, "type").String() == "item_reference" {
				b.mu.Unlock()
				return nil, codexAPIBridgeRequestError(lane, "incomplete_replay", "HTTP replay requires full input items instead of item_reference"), nil
			}
		}
		if fullSize > int(CodexAPIResponsesBodyLimit) {
			b.mu.Unlock()
			return nil, codexAPIBridgeRequestError(lane, "request_too_large", "Replayed request exceeds the 64 MiB HTTP limit"), nil
		}
		var err error
		var replayable bool
		body, replayable, err = buildOpenAIWSCurrentTurnRetryPayload(payload, full, exists, "")
		if err != nil || !replayable {
			b.mu.Unlock()
			return nil, codexAPIBridgeRequestError(lane, "incomplete_replay", "Complete input and tool-call context are required for HTTP replay"), nil
		}
		if err == nil {
			body, err = prepareOpenAIWSHTTPBridgeBody(b.account, body)
		}
		if kind == coderws.MessageBinary || target != "/v1/responses" || gjson.GetBytes(payload, "stream").Bool() {
			target = "/backend-api/codex/responses"
		}
		if err == nil {
			body, err = sjson.DeleteBytes(body, "stream_id")
		}
		if err != nil || int64(len(body)) > CodexAPIResponsesBodyLimit {
			b.mu.Unlock()
			return nil, codexAPIBridgeRequestError(lane, "request_too_large", "Unable to prepare request within the 64 MiB HTTP limit"), nil
		}
	}
	turn := &codexAPIBridgeTurn{lane: lane, input: full, complete: complete}
	if !complete {
		turn.input = nil
	}
	b.pending[lane] = turn
	b.enforceReplayBudget()
	if !bridge {
		b.mu.Unlock()
		return nativeWrite, nil, nil
	}
	switching := !b.httpMode
	b.httpMode = true
	if switching {
		b.switched = time.Now()
	}
	turnCtx, cancel := context.WithCancel(b.ctx)
	turn.cancel = cancel
	b.mu.Unlock()
	return func() error {
		if switching {
			// codex-api holds a concurrency permit for the lifetime of its socket.
			// Release it before HTTP so a gateway concurrency limit of one works.
			_ = b.native.Close()
		}
		go b.runHTTP(turnCtx, turn, body, target)
		return nil
	}, nil, nil
}

func codexAPIReplayBytes(items []json.RawMessage) int {
	total := 2
	for _, item := range items {
		total += len(item) + 1
	}
	return total
}

func (b *codexAPIWSHTTPBridge) disableNativeReplay() {
	// Native requests may still be active after tracking is released. The flag
	// prevents a later large request from mistaking this connection for idle.
	b.replayLimited = true
	clear(b.history)
	clear(b.pending)
}

func (b *codexAPIWSHTTPBridge) enforceReplayBudget() {
	total := 0
	seen := make(map[*byte]bool)
	count := func(items []json.RawMessage) {
		for _, item := range items {
			if len(item) > 0 && !seen[&item[0]] {
				seen[&item[0]] = true
				total += len(item)
			}
		}
	}
	for lane, history := range b.history {
		total += len(lane) + len(history.id)
		count(history.input)
	}
	for lane, turn := range b.pending {
		total += len(lane) + len(turn.id)
		count(turn.input)
		count(turn.collector.allItems)
		count(turn.collector.items)
		for key := range turn.collector.allSeen {
			total += len(key)
		}
		for key := range turn.collector.seen {
			total += len(key)
		}
		for key := range turn.outputOrder {
			total += len(key)
		}
		for key := range turn.unfinished {
			total += len(key)
		}
	}
	if int64(total) <= CodexAPIResponsesBodyLimit {
		return
	}
	clear(b.history)
	for _, turn := range b.pending {
		turn.discardReplay()
	}
}

func codexAPIOutputItemKey(item gjson.Result) string {
	if id := item.Get("id").String(); id != "" {
		return strings.Clone(id)
	}
	return strings.TrimSpace(item.Raw)
}

func (t *codexAPIBridgeTurn) observeOutput(event string, payload []byte) {
	t.sawOutput = t.sawOutput || isOpenAIWSTokenEvent(event) || event == "response.output_item.added" || event == "response.output_item.done"
	if event != "response.output_item.added" && event != "response.output_item.done" {
		return
	}
	item := gjson.GetBytes(payload, "item")
	key := codexAPIOutputItemKey(item)
	index := gjson.GetBytes(payload, "output_index")
	unfinishedKey := key
	if index.Exists() {
		unfinishedKey = strconv.Itoa(int(index.Int()))
	}
	if event == "response.output_item.added" {
		if t.unfinished == nil {
			t.unfinished = make(map[string]bool)
		}
		t.unfinished[unfinishedKey] = true
		return
	}
	delete(t.unfinished, unfinishedKey)
	if index.Exists() && index.Int() >= 0 {
		if t.outputOrder == nil {
			t.outputOrder = make(map[string]int)
		}
		t.outputOrder[key] = int(index.Int())
	}
	t.collector.AddEvent(event, payload)
}

func (t *codexAPIBridgeTurn) completedOutput(event string, payload []byte) ([]json.RawMessage, bool) {
	output := gjson.GetBytes(payload, "response.output")
	if output.IsArray() && len(output.Array()) > 0 {
		collector := &openAIWSToolCallReplayCollector{}
		collector.AddEvent(event, payload)
		items := collector.AllItems()
		return items, len(items) == len(output.Array())
	}
	items := t.collector.AllItems()
	if len(t.unfinished) > 0 {
		return nil, false
	}
	if len(items) == 0 {
		return nil, output.IsArray() && !t.sawOutput
	}
	for _, item := range items {
		if _, ok := t.outputOrder[codexAPIOutputItemKey(gjson.ParseBytes(item))]; !ok {
			return nil, false
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		return t.outputOrder[codexAPIOutputItemKey(gjson.ParseBytes(items[i]))] < t.outputOrder[codexAPIOutputItemKey(gjson.ParseBytes(items[j]))]
	})
	for index, item := range items {
		if t.outputOrder[codexAPIOutputItemKey(gjson.ParseBytes(item))] != index {
			return nil, false
		}
	}
	return items, true
}

func (b *codexAPIWSHTTPBridge) observe(payload []byte, turn *codexAPIBridgeTurn, keepActive bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.ctx.Err() != nil {
		return
	}
	event, id, _ := parseOpenAIWSEventEnvelope(payload)
	if len(id) > codexAPICorrelationLimitBytes || len(codexAPIMessageLane(payload)) > codexAPICorrelationLimitBytes {
		if !b.httpMode {
			b.disableNativeReplay()
		}
		return
	}
	if turn == nil {
		if lane := gjson.GetBytes(payload, "stream_id"); lane.Exists() {
			turn = b.pending[codexAPIMessageLane(payload)]
		} else {
			for _, candidate := range b.pending {
				if candidate.id == id && id != "" || len(b.pending) == 1 {
					turn = candidate
					break
				}
			}
		}
	}
	if turn == nil {
		return
	}
	if id != "" {
		turn.id = strings.Clone(id)
	}
	if event == "error" && (keepActive || turn.id != "") {
		turn.discardReplay()
		return
	}
	if turn.complete {
		turn.observeOutput(event, payload)
	}
	if !isOpenAIWSTerminalEvent(event) && event != "error" {
		b.enforceReplayBudget()
		return
	}
	delete(b.pending, turn.lane)
	if (event == "response.completed" || event == "response.done") && turn.complete && turn.id != "" {
		if output, complete := turn.completedOutput(event, payload); complete {
			b.history[turn.lane] = codexAPIReplayHistory{id: turn.id, input: combineOpenAIWSReplayItems(turn.input, output)}
		}
	}
	b.enforceReplayBudget()
}

func (b *codexAPIWSHTTPBridge) runHTTP(ctx context.Context, turn *codexAPIBridgeTurn, body []byte, target string) {
	defer turn.cancel()
	err := b.streamHTTP(ctx, turn, body, target)
	if err == nil || b.ctx.Err() != nil {
		return
	}
	b.mu.Lock()
	id := turn.id
	b.mu.Unlock()
	payload := buildOpenAIWSHTTPBridgeFailedEvent(id, gjson.GetBytes(body, "model").String(), time.Now().Unix(), nil, err.Error())
	if ctx.Err() != nil {
		payload, _ = sjson.SetBytes(payload, "type", "response.incomplete")
		payload, _ = sjson.SetBytes(payload, "response.status", "incomplete")
		payload, _ = sjson.SetBytes(payload, "response.incomplete_details.reason", "interrupted")
		payload, _ = sjson.DeleteBytes(payload, "response.error")
	}
	b.emit(codexAPIBridgeFrame{kind: coderws.MessageText, payload: codexAPIEventLane(payload, turn.lane), turn: turn})
}

func (b *codexAPIWSHTTPBridge) streamHTTP(ctx context.Context, turn *codexAPIBridgeTurn, body []byte, target string) error {
	var response *http.Response
	for attempt := 0; ; attempt++ {
		incoming := b.incoming.Clone(ctx)
		incoming.Method = http.MethodPost
		incoming.Body = io.NopCloser(bytes.NewReader(body))
		incoming.ContentLength = int64(len(body))
		for name := range incoming.Header {
			if strings.HasPrefix(strings.ToLower(name), "sec-websocket-") {
				incoming.Header.Del(name)
			}
		}
		incoming.Header.Del("Content-Encoding")
		incoming.Header.Set("Content-Type", "application/json")
		incoming.Header.Set("Accept", "text/event-stream")
		incoming.Header.Set("Accept-Encoding", "identity")
		var err error
		response, err = b.service.RoundTripCodexAPI(ctx, b.account, incoming, target)
		if err != nil {
			return errors.New("codex API HTTP bridge request failed")
		}
		if response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices {
			break
		}
		errorBody, _ := io.ReadAll(io.LimitReader(response.Body, openAIWSHTTPBridgeErrorBodyLimitBytes))
		_ = response.Body.Close()
		if response.StatusCode != http.StatusTooManyRequests || gjson.GetBytes(errorBody, "error.code").String() != "gateway_busy" || attempt >= 3 || time.Since(b.switched) > time.Second {
			payload := buildOpenAIWSHTTPBridgeErrorEvent(response.StatusCode, extractOpenAISSEErrorMessage(errorBody))
			b.emit(codexAPIBridgeFrame{kind: coderws.MessageText, payload: codexAPIEventLane(payload, turn.lane), turn: turn})
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(attempt+1) * 25 * time.Millisecond):
		}
	}
	defer func() { _ = response.Body.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = response.Body.Close() })
	defer stop()
	scanner := bufio.NewScanner(io.LimitReader(response.Body, CodexAPIResponsesBodyLimit))
	buffer := getSSEScannerBuf64K()
	defer putSSEScannerBuf64K(buffer)
	scanner.Buffer(buffer[:0], int(CodexAPIResponsesBodyLimit))
	var parser openAICompatSSEFrameParser
	frameBytes := 0
	bareError := ""
	var parseError error
	emit := func(frame openAICompatSSEFrame, ok bool) bool {
		if !ok || strings.TrimSpace(frame.Data) == "[DONE]" {
			return false
		}
		payload := []byte(openAICompatPayloadWithEventType(frame.Data, frame.EventType))
		if !json.Valid(payload) {
			parseError = errors.New("codex API HTTP response contains incomplete or invalid JSON")
			return true
		}
		_, id, _ := parseOpenAIWSEventEnvelope(payload)
		if len(id) > codexAPICorrelationLimitBytes {
			parseError = errors.New("codex API HTTP response exceeds the 8 KiB correlation limit")
			return true
		}
		payload = codexAPIEventLane(payload, turn.lane)
		event := gjson.GetBytes(payload, "type").String()
		if event == "error" {
			bareError = extractOpenAISSEErrorMessage(payload)
			if bareError == "" {
				bareError = gjson.GetBytes(payload, "message").String()
			}
		}
		b.emit(codexAPIBridgeFrame{kind: coderws.MessageText, payload: payload, turn: turn, keepActive: event == "error"})
		return isOpenAIWSTerminalEvent(event)
	}
	for scanner.Scan() {
		line := scanner.Text()
		frameBytes += len(line)
		if int64(frameBytes) > CodexAPIResponsesBodyLimit {
			return errors.New("codex API HTTP event exceeds the response limit")
		}
		frame, ready := parser.AddLine(line)
		if ready {
			frameBytes = 0
		}
		if emit(frame, ready) {
			return parseError
		}
	}
	if emit(parser.Finish()) {
		return parseError
	}
	if err := scanner.Err(); err != nil {
		return errors.New("codex API HTTP response could not be read")
	}
	if bareError != "" {
		return errors.New(bareError)
	}
	return errors.New("codex API HTTP response ended before a terminal event")
}

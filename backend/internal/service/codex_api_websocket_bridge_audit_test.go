package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
	"unsafe"

	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestCodexAPIWSBridgeAuditRetainsNativeSeparateOutputItems(t *testing.T) {
	bodies := make(chan []byte, 2)
	count := 0
	bridge, _ := newCodexAPIBridgeFixture(t, 1, codexAPIBridgeHTTPFixture{do: func(r *http.Request, _ int64) (*http.Response, error) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		bodies <- body
		count++
		if count == 1 {
			frames := "data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"type\":\"function_call\",\"id\":\"tool-item\",\"call_id\":\"call-1\",\"name\":\"tool\",\"arguments\":\"{}\"}}\n\n" +
				"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"native-complete\",\"output\":[]}}\n\n"
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(frames))}, nil
		}
		return codexAPIBridgeResponse("next", `[]`), nil
	}})
	codexAPIBridgeWrite(t, bridge, `{"type":"response.create","stream":true,"model":"gpt-5","input":[]}`)
	require.Equal(t, "response.output_item.done", gjson.GetBytes(codexAPIBridgeRead(t, bridge), "type").String())
	require.Equal(t, "response.completed", gjson.GetBytes(codexAPIBridgeRead(t, bridge), "type").String())
	<-bodies
	codexAPIBridgeWrite(t, bridge, `{"type":"response.create","stream":true,"model":"gpt-5","previous_response_id":"native-complete","input":[{"type":"function_call_output","call_id":"call-1","output":"result"}]}`)
	event := codexAPIBridgeRead(t, bridge)
	require.Equal(t, "response.completed", gjson.GetBytes(event, "type").String(), "next turn lost captured native tool context: %s", event)
	body := <-bodies
	require.Equal(t, "function_call", gjson.GetBytes(body, "input.0.type").String())
	require.Equal(t, "function_call_output", gjson.GetBytes(body, "input.1.type").String())
}

func TestCodexAPIWSBridgeAuditInterruptCancelsHTTP(t *testing.T) {
	started := make(chan context.Context, 1)
	bridge, _ := newCodexAPIBridgeFixture(t, 1, codexAPIBridgeHTTPFixture{do: func(r *http.Request, _ int64) (*http.Response, error) {
		started <- r.Context()
		reader, writer := io.Pipe()
		go func() {
			defer func() { _ = writer.Close() }()
			_, _ = io.WriteString(writer, "data: {\"type\":\"response.created\",\"response\":{\"id\":\"interrupt-me\"}}\n\n")
			<-r.Context().Done()
		}()
		return &http.Response{StatusCode: http.StatusOK, Body: reader}, nil
	}})
	require.NoError(t, bridge.WriteFrame(t.Context(), coderws.MessageText, []byte(`{"type":"response.create","stream":true,"model":"gpt-5","stream_id":"interrupt-lane","input":[]}`)))
	requestContext := <-started
	require.Equal(t, "response.created", gjson.GetBytes(codexAPIBridgeRead(t, bridge), "type").String())
	codexAPIBridgeWrite(t, bridge, `{"type":"response.interrupt","response_id":"interrupt-me","mode":"discard_partial_items"}`)
	event := codexAPIBridgeRead(t, bridge)
	select {
	case <-requestContext.Done():
	case <-time.After(100 * time.Millisecond):
		t.Fatalf("Codex interrupt did not cancel upstream HTTP; response: %s", event)
	}
	require.Equal(t, "response.incomplete", gjson.GetBytes(event, "type").String())
	require.Equal(t, "interrupted", gjson.GetBytes(event, "response.incomplete_details.reason").String())
	require.Equal(t, "interrupt-lane", gjson.GetBytes(event, "stream_id").String())
}

func TestCodexAPIWSBridgeAuditNativeInterruptRemainsUnchanged(t *testing.T) {
	bridge, native := newCodexAPIBridgeFixture(t, 1000, codexAPIBridgeHTTPFixture{})
	for _, payload := range []string{
		`{"type":"response.create","model":"gpt-5","stream_id":"native","input":"hello"}`,
		`{"type":"response.interrupt","response_id":"native-response","mode":"discard_partial_items"}`,
	} {
		require.NoError(t, bridge.WriteFrame(t.Context(), coderws.MessageText, []byte(payload)))
		require.Equal(t, payload, string(<-native.written))
	}
}

func TestCodexAPIWSBridgeAuditBareErrorEOFEndsClientTurn(t *testing.T) {
	bridge, _ := newCodexAPIBridgeFixture(t, 1, codexAPIBridgeHTTPFixture{do: func(*http.Request, int64) (*http.Response, error) {
		frames := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"failed-turn\"}}\n\n" +
			"event: error\ndata: {\"type\":\"error\",\"sequence_number\":1,\"code\":\"upstream_error\",\"message\":\"upstream deadline exceeded\",\"param\":null}\n\n"
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(frames))}, nil
	}})
	codexAPIBridgeWrite(t, bridge, `{"type":"response.create","stream":true,"model":"gpt-5","input":[]}`)
	require.Equal(t, "response.created", gjson.GetBytes(codexAPIBridgeRead(t, bridge), "type").String())
	require.Equal(t, "error", gjson.GetBytes(codexAPIBridgeRead(t, bridge), "type").String())
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	_, event, err := bridge.ReadFrame(ctx)
	require.NoError(t, err, "the HTTP stream ended but the client turn never received a terminal event")
	require.Equal(t, "response.failed", gjson.GetBytes(event, "type").String())
	require.Equal(t, "failed-turn", gjson.GetBytes(event, "response.id").String())
	require.Contains(t, gjson.GetBytes(event, "response.error.message").String(), "deadline")
}

func TestCodexAPIWSBridgeAuditUnknownParentRetainsNoInput(t *testing.T) {
	bridge, native := newCodexAPIBridgeFixture(t, 1000, codexAPIBridgeHTTPFixture{})
	payload := []byte(`{"type":"response.create","model":"gpt-5","previous_response_id":"external","input":[{"role":"user","content":"tiny"}],"metadata":"` + strings.Repeat("x", 500) + `"}`)
	require.NoError(t, bridge.WriteFrame(t.Context(), coderws.MessageText, payload))
	require.Equal(t, payload, <-native.written)
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	require.False(t, bridge.pending[""].complete)
	require.Empty(t, bridge.pending[""].input, "incomplete snapshots must not pin entire native frames")
}

func TestCodexAPIWSBridgeAuditCompletedOutputIsCompleteAndOrdered(t *testing.T) {
	for _, tc := range []struct {
		name     string
		events   []string
		terminal string
		complete bool
		ids      []string
	}{
		{
			name: "indexed_items_out_of_order_without_terminal_output",
			events: []string{
				`{"type":"response.output_item.done","output_index":1,"item":{"type":"message","id":"second","role":"assistant","content":[]}}`,
				`{"type":"response.output_item.done","output_index":0,"item":{"type":"reasoning","id":"first","summary":[]}}`,
			},
			terminal: `{"type":"response.completed","response":{"id":"r"}}`, complete: true, ids: []string{"first", "second"},
		},
		{
			name: "unfinished_item",
			events: []string{
				`{"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"unfinished"}}`,
			},
			terminal: `{"type":"response.completed","response":{"id":"r","output":[]}}`,
		},
		{
			name: "missing_index_zero",
			events: []string{
				`{"type":"response.output_item.done","output_index":1,"item":{"type":"message","id":"second","content":[]}}`,
			},
			terminal: `{"type":"response.completed","response":{"id":"r","output":[]}}`,
		},
		{
			name: "uncollected_delta",
			events: []string{
				`{"type":"response.output_text.delta","output_index":0,"delta":"unretained"}`,
			},
			terminal: `{"type":"response.completed","response":{"id":"r","output":[]}}`,
		},
		{
			name:     "invalid_terminal_item",
			terminal: `{"type":"response.completed","response":{"id":"r","output":[{"type":"message","id":"valid"},{"id":"invalid"}]}}`,
		},
		{
			name: "invalid_done_item_is_not_empty_output",
			events: []string{
				`{"type":"response.output_item.done","output_index":0,"item":{"id":"invalid"}}`,
			},
			terminal: `{"type":"response.completed","response":{"id":"r","output":[]}}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			turn := &codexAPIBridgeTurn{}
			for _, event := range tc.events {
				turn.observeOutput(gjson.Get(event, "type").String(), []byte(event))
			}
			items, complete := turn.completedOutput("response.completed", []byte(tc.terminal))
			require.Equal(t, tc.complete, complete)
			if complete {
				ids := make([]string, 0, len(items))
				for _, item := range items {
					ids = append(ids, gjson.GetBytes(item, "id").String())
				}
				require.Equal(t, tc.ids, ids)
			}
		})
	}
}

func TestCodexAPIWSBridgeAuditReplayBudgetIncludesCollectorKeys(t *testing.T) {
	bridge, _ := newCodexAPIBridgeFixture(t, 1, codexAPIBridgeHTTPFixture{})
	key := strings.Repeat("x", int(CodexAPIResponsesBodyLimit))
	bridge.pending[""] = &codexAPIBridgeTurn{complete: true, input: []json.RawMessage{json.RawMessage(`{}`)}, collector: openAIWSToolCallReplayCollector{allSeen: map[string]struct{}{key: {}}}}
	bridge.enforceReplayBudget()
	require.False(t, bridge.pending[""].complete)
	require.Empty(t, bridge.pending[""].collector.allSeen)
}

func TestCodexAPIWSBridgeAuditRetainedKeysOwnTheirBytes(t *testing.T) {
	raw := `{"type":"function_call","id":"item-id","call_id":"call-id","name":"tool","arguments":"` + strings.Repeat("x", 8192) + `"}`
	item := gjson.Parse(raw)
	start := uintptr(unsafe.Pointer(unsafe.StringData(item.Raw)))
	end := start + uintptr(len(item.Raw))
	assertOwned := func(key string) {
		t.Helper()
		ptr := uintptr(unsafe.Pointer(unsafe.StringData(key)))
		require.False(t, ptr >= start && ptr < end, "retained key pins the uncounted full JSON string")
	}
	assertOwned(codexAPIOutputItemKey(item))
	var collector openAIWSToolCallReplayCollector
	collector.addAllItem(item)
	collector.addItem(item)
	for key := range collector.allSeen {
		assertOwned(key)
	}
	for key := range collector.seen {
		assertOwned(key)
	}
}

func TestCodexAPIWSBridgeAuditPendingLivesUntilTerminalObservation(t *testing.T) {
	started := make(chan context.Context, 1)
	bridge, _ := newCodexAPIBridgeFixture(t, 1, codexAPIBridgeHTTPFixture{do: func(r *http.Request, _ int64) (*http.Response, error) {
		started <- r.Context()
		return codexAPIBridgeResponse("first", `[]`), nil
	}})
	require.NoError(t, bridge.WriteFrame(t.Context(), coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-5","input":"hello"}`)))
	requestContext := <-started
	frame := <-bridge.frames
	select {
	case <-requestContext.Done():
	case <-time.After(time.Second):
		t.Fatal("HTTP terminal sender did not finish")
	}
	bridge.mu.Lock()
	pending := bridge.pending[""]
	bridge.mu.Unlock()
	require.Same(t, frame.turn, pending, "a new turn must not be admitted before the prior terminal is observed")
	_, rejection, err := bridge.PrepareWriteFrame(t.Context(), coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-5","input":"too early"}`))
	require.NoError(t, err)
	require.Equal(t, "bridge_busy", gjson.GetBytes(rejection, "error.code").String())
	bridge.observe(frame.payload, frame.turn, frame.keepActive)
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	require.Empty(t, bridge.pending)
	require.Equal(t, "first", bridge.history[""].id)
}

func TestCodexAPIWSBridgeAuditCorrelationBounds(t *testing.T) {
	for _, size := range []int{8190, 8191} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			bridge, _ := newCodexAPIBridgeFixture(t, 1, codexAPIBridgeHTTPFixture{do: func(*http.Request, int64) (*http.Response, error) {
				return codexAPIBridgeResponse("bounded", `[]`), nil
			}})
			codexAPIBridgeWrite(t, bridge, fmt.Sprintf(`{"type":"response.create","model":"gpt-5","stream_id":%q,"input":"hello"}`, strings.Repeat("x", size)))
			event := codexAPIBridgeRead(t, bridge)
			if size == 8190 {
				require.Equal(t, "response.completed", gjson.GetBytes(event, "type").String())
			} else {
				require.Equal(t, "replay_limit", gjson.GetBytes(event, "error.code").String())
			}
		})
	}
}

func TestCodexAPIWSBridgeAuditOversizedNativeCorrelationDisablesReplay(t *testing.T) {
	for _, field := range []string{"stream_id", "response_id"} {
		t.Run(field, func(t *testing.T) {
			bridge, native := newCodexAPIBridgeFixture(t, 20000, codexAPIBridgeHTTPFixture{})
			bridge.history["old"] = codexAPIReplayHistory{id: "old"}
			payload := `{"type":"response.create","model":"gpt-5","input":"hello"}`
			if field == "stream_id" {
				payload = fmt.Sprintf(`{"type":"response.create","model":"gpt-5","stream_id":%q,"input":"hello"}`, strings.Repeat("x", 8193))
			}
			require.NoError(t, bridge.WriteFrame(t.Context(), coderws.MessageText, []byte(payload)))
			require.Equal(t, payload, string(<-native.written))
			if field == "response_id" {
				frame := fmt.Sprintf(`{"type":"response.created","response":{"id":%q}}`, strings.Repeat("x", 8193))
				native.read <- []byte(frame)
				require.Equal(t, frame, string(codexAPIBridgeRead(t, bridge)))
			}
			bridge.mu.Lock()
			historyCount := len(bridge.history)
			bridge.mu.Unlock()
			require.Zero(t, historyCount)
			codexAPIBridgeWrite(t, bridge, fmt.Sprintf(`{"type":"response.create","model":"gpt-5","input":%q}`, strings.Repeat("y", 20001)))
			require.Equal(t, "replay_limit", gjson.GetBytes(codexAPIBridgeRead(t, bridge), "error.code").String())
		})
	}
}

func TestCodexAPIWSBridgeAuditOversizedHTTPResponseIDIsNotExposed(t *testing.T) {
	bridge, _ := newCodexAPIBridgeFixture(t, 1, codexAPIBridgeHTTPFixture{do: func(*http.Request, int64) (*http.Response, error) {
		return codexAPIBridgeResponse(strings.Repeat("x", 8193), `[]`), nil
	}})
	codexAPIBridgeWrite(t, bridge, `{"type":"response.create","model":"gpt-5","input":"hello"}`)
	event := codexAPIBridgeRead(t, bridge)
	require.Equal(t, "response.failed", gjson.GetBytes(event, "type").String())
	require.Contains(t, gjson.GetBytes(event, "response.error.message").String(), "correlation limit")
	require.LessOrEqual(t, len(gjson.GetBytes(event, "response.id").String()), 8192)
}

func TestCodexAPIWSBridgeAuditNativeBareErrorKeepsActiveTurn(t *testing.T) {
	bridge, native := newCodexAPIBridgeFixture(t, 300, codexAPIBridgeHTTPFixture{do: func(*http.Request, int64) (*http.Response, error) {
		return codexAPIBridgeResponse("http-next", `[]`), nil
	}})
	require.NoError(t, bridge.WriteFrame(t.Context(), coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-5","input":"hello"}`)))
	<-native.written
	for _, frame := range []string{
		`{"type":"response.created","response":{"id":"native-active"}}`,
		`{"type":"error","error":{"message":"recoverable provider event"}}`,
	} {
		native.read <- []byte(frame)
		require.Equal(t, frame, string(codexAPIBridgeRead(t, bridge)))
	}
	next := fmt.Sprintf(`{"type":"response.create","model":"gpt-5","stream_id":"other","input":%q}`, strings.Repeat("x", 400))
	codexAPIBridgeWrite(t, bridge, next)
	require.Equal(t, "bridge_busy", gjson.GetBytes(codexAPIBridgeRead(t, bridge), "error.code").String())
	select {
	case <-native.closed:
		t.Fatal("native active response was closed after a nonterminal error")
	default:
	}
	native.read <- []byte(`{"type":"response.completed","response":{"id":"native-active","output":[]}}`)
	codexAPIBridgeRead(t, bridge)
	codexAPIBridgeWrite(t, bridge, next)
	require.Equal(t, "http-next", gjson.GetBytes(codexAPIBridgeRead(t, bridge), "response.id").String())
}

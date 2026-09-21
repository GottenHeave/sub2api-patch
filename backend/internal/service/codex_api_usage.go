package service

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"io"
	"math"
	"strings"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
	"github.com/tidwall/gjson"
)

const codexAPIObservationLimit = 8 << 20

// CodexAPIUsageObserver observes a copy of response bytes without changing relay
// behavior. Observation limits affect accounting availability, never delivery.
type CodexAPIUsageObserver struct {
	stream   bool
	encoding string
	buffer   []byte
	data     []byte
	event    string
	discard  bool
	result   *OpenAIForwardResult
	finished bool
}

func NewCodexAPIUsageObserver(contentType, contentEncoding string) *CodexAPIUsageObserver {
	return &CodexAPIUsageObserver{
		stream:   strings.EqualFold(strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0]), "text/event-stream"),
		encoding: strings.ToLower(strings.TrimSpace(contentEncoding)),
	}
}

func (o *CodexAPIUsageObserver) Write(p []byte) (int, error) {
	n := len(p)
	if o.finished {
		return n, nil
	}
	if !o.stream || o.encoding != "" && o.encoding != "identity" {
		if !o.discard && len(p) <= codexAPIObservationLimit-len(o.buffer) {
			o.buffer = append(o.buffer, p...)
		} else {
			o.discard, o.buffer = true, nil
		}
		return n, nil
	}
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		if i < 0 {
			i = len(p)
		}
		if len(p[:i]) <= codexAPIObservationLimit-len(o.buffer)-len(o.data) && !o.discard {
			o.buffer = append(o.buffer, p[:i]...)
		} else {
			o.discard, o.data = true, nil
			if len(bytes.Trim(p[:i], "\r")) > 0 || len(o.buffer) > 0 {
				o.buffer = []byte{1}
			}
		}
		if i == len(p) {
			break
		}
		o.line(bytes.TrimSuffix(o.buffer, []byte{'\r'}))
		o.buffer = nil
		p = p[i+1:]
	}
	return n, nil
}

func (o *CodexAPIUsageObserver) line(line []byte) {
	if len(line) == 0 {
		if !o.discard {
			o.observe(o.data, o.event)
		}
		o.data, o.event, o.discard = nil, "", false
		return
	}
	if o.discard {
		return
	}
	field, value, _ := bytes.Cut(line, []byte{':'})
	value = bytes.TrimPrefix(value, []byte{' '})
	switch string(field) {
	case "event":
		o.event = string(value)
	case "data":
		if len(o.data) > 0 {
			o.data = append(o.data, '\n')
		}
		o.data = append(o.data, value...)
	}
}

func (o *CodexAPIUsageObserver) observe(body []byte, event string) {
	if !gjson.ValidBytes(body) {
		return
	}
	if o.stream && !openAIStreamEventTypeIsTerminal(effectiveOpenAISSEEventType(body, event)) {
		return
	}
	root := gjson.ParseBytes(body)
	if response := root.Get("response"); response.IsObject() {
		root = response
	}
	usage := root.Get("usage")
	// Missing, empty and malformed usage must not become a fabricated zero bill.
	for _, name := range []string{"input_tokens", "output_tokens"} {
		v := usage.Get(name)
		if v.Type != gjson.Number || v.Float() < 0 || v.Float() >= float64(math.MaxInt) || math.Trunc(v.Float()) != v.Float() {
			return
		}
	}
	parsed, ok := extractOpenAIUsageFromJSONBytes(body)
	if !ok {
		return
	}
	o.result = &OpenAIForwardResult{
		Usage: parsed, ResponseID: extractOpenAIResponseIDFromJSONBytes(body),
		UpstreamResponseModel:       root.Get("model").String(),
		UpstreamResponseServiceTier: root.Get("service_tier").String(),
	}
}

func (o *CodexAPIUsageObserver) Result() (*OpenAIForwardResult, bool) {
	if o.finished {
		return o.result, o.result != nil
	}
	o.finished = true
	if o.encoding != "" && o.encoding != "identity" {
		if o.discard {
			return nil, false
		}
		var reader io.Reader
		var closeReader func()
		switch o.encoding {
		case "gzip":
			r, err := gzip.NewReader(bytes.NewReader(o.buffer))
			if err != nil {
				return nil, false
			}
			reader, closeReader = r, func() { _ = r.Close() }
		case "deflate":
			r, err := zlib.NewReader(bytes.NewReader(o.buffer))
			if err != nil {
				return nil, false
			}
			reader, closeReader = r, func() { _ = r.Close() }
		case "br":
			reader = brotli.NewReader(bytes.NewReader(o.buffer))
		case "zstd":
			r, err := zstd.NewReader(bytes.NewReader(o.buffer), zstd.WithDecoderMaxMemory(codexAPIObservationLimit), zstd.WithDecoderConcurrency(1))
			if err != nil {
				return nil, false
			}
			reader, closeReader = r, r.Close
		default:
			return nil, false
		}
		if closeReader != nil {
			defer closeReader()
		}
		decoded := NewCodexAPIUsageObserver("application/json", "")
		decoded.stream = o.stream
		n, err := io.Copy(decoded, io.LimitReader(reader, 4*codexAPIObservationLimit+1))
		o.buffer = nil
		if err != nil || n > 4*codexAPIObservationLimit {
			return nil, false
		}
		o.result, _ = decoded.Result()
	} else if o.stream {
		if len(o.buffer) > 0 {
			o.line(bytes.TrimSuffix(o.buffer, []byte{'\r'}))
		}
		o.line(nil)
	} else if !o.discard {
		o.observe(o.buffer, "")
	}
	o.buffer, o.data = nil, nil
	return o.result, o.result != nil
}

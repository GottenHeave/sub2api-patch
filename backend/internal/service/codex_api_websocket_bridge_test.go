package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type codexAPIBridgeNativeFixture struct {
	read    chan []byte
	written chan []byte
	closed  chan struct{}
	once    sync.Once
}

func (f *codexAPIBridgeNativeFixture) ReadFrame(ctx context.Context) (coderws.MessageType, []byte, error) {
	select {
	case body := <-f.read:
		return coderws.MessageText, body, nil
	case <-f.closed:
		return 0, nil, io.EOF
	case <-ctx.Done():
		return 0, nil, ctx.Err()
	}
}

func (f *codexAPIBridgeNativeFixture) WriteFrame(ctx context.Context, _ coderws.MessageType, body []byte) error {
	select {
	case f.written <- body:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (f *codexAPIBridgeNativeFixture) Close() error {
	f.once.Do(func() { close(f.closed) })
	return nil
}

type codexAPIBridgeHTTPFixture struct {
	HTTPUpstream
	do func(*http.Request, int64) (*http.Response, error)
}

func (f codexAPIBridgeHTTPFixture) Do(r *http.Request, _ string, account int64, _ int) (*http.Response, error) {
	return f.do(r, account)
}

func newCodexAPIBridgeFixture(t *testing.T, threshold int64, upstream codexAPIBridgeHTTPFixture) (*codexAPIWSHTTPBridge, *codexAPIBridgeNativeFixture) {
	t.Helper()
	config := &config.Config{}
	config.Gateway.OpenAIWS.HTTPBridgeEnabled = true
	config.Gateway.OpenAIWS.HTTPBridgeThresholdBytes = threshold
	svc := &OpenAIGatewayService{cfg: config, httpUpstream: upstream}
	native := &codexAPIBridgeNativeFixture{read: make(chan []byte, 8), written: make(chan []byte, 8), closed: make(chan struct{})}
	account := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeCodexAPI, Credentials: map[string]any{"base_url": "https://gateway.example", "api_key": "fixture-key"}}
	incoming := httptest.NewRequest(http.MethodGet, "/v1/responses?business=keep&api_key=local", nil)
	incoming.Header.Set("Authorization", "Bearer local-key")
	incoming.Header.Set("Sec-WebSocket-Key", "fixture-handshake")
	bridge, ok := svc.CodexAPIWebSocketTransport(t.Context(), native, account, incoming, "/v1/responses").(*codexAPIWSHTTPBridge)
	require.True(t, ok)
	t.Cleanup(func() { _ = bridge.Close() })
	return bridge, native
}

func codexAPIBridgeResponse(id, output string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(fmt.Sprintf("event: response.completed\ndata: {\"response\":{\"id\":%q,\"output\":%s}}\n\n", id, output)))}
}

func codexAPIBridgeRead(t *testing.T, bridge *codexAPIWSHTTPBridge) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	_, payload, err := bridge.ReadFrame(ctx)
	require.NoError(t, err)
	return payload
}

func codexAPIBridgeWrite(t *testing.T, bridge *codexAPIWSHTTPBridge, payload string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	t.Cleanup(cancel)
	// Local rejection events use the same backpressure as upstream frames.
	go func() { require.NoError(t, bridge.WriteFrame(ctx, coderws.MessageText, []byte(payload))) }()
}

func TestCodexAPIWSBridgePreservesTransportIdentityAndSchema(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			requests := make(chan *http.Request, 1)
			bodies := make(chan []byte, 1)
			bridge, native := newCodexAPIBridgeFixture(t, 1, codexAPIBridgeHTTPFixture{do: func(r *http.Request, account int64) (*http.Response, error) {
				require.EqualValues(t, 42, account)
				body, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				requests <- r
				bodies <- body
				for _, item := range gjson.GetBytes(body, "input").Array() {
					if !item.IsObject() {
						return &http.Response{StatusCode: 422, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"input items must be objects"}}`))}, nil
					}
				}
				return codexAPIBridgeResponse("resp_http", `[]`), nil
			}})
			codexAPIBridgeWrite(t, bridge, fmt.Sprintf(`{"type":"response.create","stream":%t,"model":"gpt-5","stream_id":"lane-a","client_metadata":{"trace":"keep"},"reasoning":{"effort":"none"},"input":"hello"}`, prepared))
			event := codexAPIBridgeRead(t, bridge)
			require.Equal(t, "response.completed", gjson.GetBytes(event, "type").String())
			require.Equal(t, "lane-a", gjson.GetBytes(event, "stream_id").String())
			req, body := <-requests, <-bodies
			wantPath := "/v1/responses"
			if prepared {
				wantPath = "/backend-api/codex/responses"
			}
			require.Equal(t, wantPath, req.URL.Path)
			require.Equal(t, "business=keep", req.URL.RawQuery)
			require.Equal(t, "Bearer fixture-key", req.Header.Get("Authorization"))
			require.Empty(t, req.Header.Get("Sec-WebSocket-Key"))
			require.Equal(t, "identity", req.Header.Get("Accept-Encoding"))
			require.Equal(t, "keep", gjson.GetBytes(body, "client_metadata.trace").String())
			require.Equal(t, "none", gjson.GetBytes(body, "reasoning.effort").String())
			require.False(t, gjson.GetBytes(body, "stream_id").Exists())
			require.False(t, gjson.GetBytes(body, "type").Exists())
			require.True(t, gjson.GetBytes(body, "stream").Bool())
			select {
			case <-native.closed:
			default:
				t.Fatal("HTTP bridge retained native concurrency permit")
			}
		})
	}
}

func TestCodexAPIWSBridgeMidSessionReplaysCompleteImageAndToolHistory(t *testing.T) {
	bodies := make(chan []byte, 2)
	turn := 0
	bridge, native := newCodexAPIBridgeFixture(t, 1200, codexAPIBridgeHTTPFixture{do: func(r *http.Request, _ int64) (*http.Response, error) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		bodies <- body
		turn++
		return codexAPIBridgeResponse(fmt.Sprintf("resp_http_%d", turn), `[{"type":"message","id":"http_output","role":"assistant","content":[{"type":"output_text","text":"done"}]}]`), nil
	}})
	initial := `{"type":"response.create","model":"gpt-5","stream":true,"stream_id":"lane","input":[{"role":"user","content":[{"type":"input_image","image_url":"` + strings.Repeat("x", 700) + `"}]}]}`
	require.NoError(t, bridge.WriteFrame(t.Context(), coderws.MessageText, []byte(initial)))
	require.Equal(t, initial, string(<-native.written))
	native.read <- []byte(`{"type":"response.output_item.done","stream_id":"lane","item":{"type":"function_call","id":"call-item","call_id":"call-1","name":"tool","arguments":"{}"}}`)
	codexAPIBridgeRead(t, bridge)
	native.read <- []byte(`{"type":"response.completed","stream_id":"lane","response":{"id":"resp_native","output":[{"type":"reasoning","id":"reasoning","encrypted_content":"opaque"},{"type":"function_call","id":"call-item","call_id":"call-1","name":"tool","arguments":"{}"}]}}`)
	codexAPIBridgeRead(t, bridge)
	second := `{"type":"response.create","model":"gpt-5","stream":true,"stream_id":"lane","previous_response_id":"resp_native","input":[{"type":"function_call_output","call_id":"call-1","output":"` + strings.Repeat("y", 350) + `"}]}`
	codexAPIBridgeWrite(t, bridge, second)
	require.Equal(t, "resp_http_1", gjson.GetBytes(codexAPIBridgeRead(t, bridge), "response.id").String())
	body := <-bodies
	require.False(t, gjson.GetBytes(body, "previous_response_id").Exists())
	require.Equal(t, 4, len(gjson.GetBytes(body, "input").Array()))
	require.Equal(t, 700, len(gjson.GetBytes(body, "input.0.content.0.image_url").String()))
	require.Equal(t, "reasoning", gjson.GetBytes(body, "input.1.type").String())
	require.Equal(t, "function_call", gjson.GetBytes(body, "input.2.type").String())
	require.Equal(t, "function_call_output", gjson.GetBytes(body, "input.3.type").String())
	codexAPIBridgeWrite(t, bridge, `{"type":"response.create","model":"gpt-5","stream_id":"lane","previous_response_id":"resp_http_1","input":"continue"}`)
	codexAPIBridgeRead(t, bridge)
	lastBody := <-bodies
	require.Equal(t, 6, len(gjson.GetBytes(lastBody, "input").Array()))
	require.Equal(t, "user", gjson.GetBytes(lastBody, "input.5.role").String())
	require.Equal(t, "continue", gjson.GetBytes(lastBody, "input.5.content.0.text").String())
}

func TestCodexAPIWSBridgeRejectsUnavailableOrForeignHistory(t *testing.T) {
	for _, lane := range []string{"lane", "other"} {
		t.Run(lane, func(t *testing.T) {
			bridge, native := newCodexAPIBridgeFixture(t, 100, codexAPIBridgeHTTPFixture{do: func(*http.Request, int64) (*http.Response, error) {
				t.Error("missing history reached HTTP")
				return nil, io.EOF
			}})
			bridge.history["\"lane\""] = codexAPIReplayHistory{id: "known", input: []json.RawMessage{json.RawMessage(`{"role":"user","content":"private"}`)}}
			codexAPIBridgeWrite(t, bridge, fmt.Sprintf(`{"type":"response.create","model":"gpt-5","stream_id":%q,"previous_response_id":"unknown","input":"%s"}`, lane, strings.Repeat("x", 100)))
			require.Equal(t, "previous_response_not_found", gjson.GetBytes(codexAPIBridgeRead(t, bridge), "error.code").String())
			select {
			case <-native.closed:
				t.Fatal("history error closed native transport")
			default:
			}
		})
	}
}

func TestCodexAPIWSBridgeNativeWarmupAndBusyRejections(t *testing.T) {
	bridge, native := newCodexAPIBridgeFixture(t, 300, codexAPIBridgeHTTPFixture{})
	warmup := `{"type":"response.create","generate":false,"model":"gpt-5","input":[]}`
	require.NoError(t, bridge.WriteFrame(t.Context(), coderws.MessageText, []byte(warmup)))
	require.Equal(t, warmup, string(<-native.written))
	codexAPIBridgeWrite(t, bridge, `{"type":"response.create","model":"gpt-5","stream_id":"other","input":"`+strings.Repeat("x", 400)+`"}`)
	require.Equal(t, "bridge_busy", gjson.GetBytes(codexAPIBridgeRead(t, bridge), "error.code").String())
	bridge.mu.Lock()
	require.Len(t, bridge.pending, 1, "local rejection must not finalize a native turn")
	bridge.mu.Unlock()
	native.read <- []byte(`{"type":"response.completed","response":{"id":"warmup","output":[]}}`)
	codexAPIBridgeRead(t, bridge)
	codexAPIBridgeWrite(t, bridge, `{"type":"response.create","generate":false,"model":"gpt-5","input":"`+strings.Repeat("x", 400)+`"}`)
	require.Equal(t, "unsupported_warmup", gjson.GetBytes(codexAPIBridgeRead(t, bridge), "error.code").String())
}

func TestCodexAPIWSBridgeCancellationAndDisconnect(t *testing.T) {
	started := make(chan struct{})
	canceled := make(chan struct{})
	bridge, _ := newCodexAPIBridgeFixture(t, 1, codexAPIBridgeHTTPFixture{do: func(r *http.Request, _ int64) (*http.Response, error) {
		close(started)
		<-r.Context().Done()
		close(canceled)
		return nil, r.Context().Err()
	}})
	require.NoError(t, bridge.WriteFrame(t.Context(), coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-5","stream_id":"lane","input":[]}`)))
	<-started
	require.NoError(t, bridge.WriteFrame(t.Context(), coderws.MessageText, []byte(`{"type":"response.cancel","stream_id":"lane"}`)))
	require.Equal(t, "response.incomplete", gjson.GetBytes(codexAPIBridgeRead(t, bridge), "type").String())
	<-canceled
	require.NoError(t, bridge.Close())
	_, _, err := bridge.ReadFrame(t.Context())
	require.ErrorIs(t, err, context.Canceled)
}

func TestCodexAPIWSBridgeReplayBudgetInvalidatesIncompleteHistory(t *testing.T) {
	bridge, _ := newCodexAPIBridgeFixture(t, 1, codexAPIBridgeHTTPFixture{})
	bridge.history["a"] = codexAPIReplayHistory{id: "a", input: []json.RawMessage{make([]byte, CodexAPIResponsesBodyLimit+1)}}
	bridge.pending["b"] = &codexAPIBridgeTurn{complete: true, input: []json.RawMessage{json.RawMessage(`{}`)}}
	bridge.enforceReplayBudget()
	require.Empty(t, bridge.history)
	require.False(t, bridge.pending["b"].complete)
	require.Empty(t, bridge.pending["b"].input)
}

func TestCodexAPIWSBridgeRetriesOnlyGatewayPermitRelease(t *testing.T) {
	count := 0
	bridge, _ := newCodexAPIBridgeFixture(t, 1, codexAPIBridgeHTTPFixture{do: func(*http.Request, int64) (*http.Response, error) {
		count++
		if count == 1 {
			return &http.Response{StatusCode: 429, Body: io.NopCloser(strings.NewReader(`{"error":{"code":"gateway_busy","message":"busy"}}`))}, nil
		}
		return codexAPIBridgeResponse("retried", `[]`), nil
	}})
	codexAPIBridgeWrite(t, bridge, `{"type":"response.create","model":"gpt-5","input":[]}`)
	require.Equal(t, "retried", gjson.GetBytes(codexAPIBridgeRead(t, bridge), "response.id").String())
	require.Equal(t, 2, count)
}

func TestCodexAPIWSBridgeThresholdAndDisabledRoutes(t *testing.T) {
	payload := []byte(`{"type":"response.create","model":"gpt-5","input":[]}`)
	for _, delta := range []int{-1, 0, 1} {
		t.Run(fmt.Sprint(delta), func(t *testing.T) {
			bridge, native := newCodexAPIBridgeFixture(t, int64(len(payload)+delta), codexAPIBridgeHTTPFixture{do: func(*http.Request, int64) (*http.Response, error) {
				return codexAPIBridgeResponse("bounded", `[]`), nil
			}})
			require.NoError(t, bridge.WriteFrame(t.Context(), coderws.MessageText, payload))
			if delta > 0 {
				require.Equal(t, payload, <-native.written)
			} else {
				require.Equal(t, "bounded", gjson.GetBytes(codexAPIBridgeRead(t, bridge), "response.id").String())
			}
			require.Same(t, native, bridge.service.CodexAPIWebSocketTransport(t.Context(), native, bridge.account, bridge.incoming, "/v1/realtime"))
			bridge.service.cfg.Gateway.OpenAIWS.HTTPBridgeEnabled = false
			require.Same(t, native, bridge.service.CodexAPIWebSocketTransport(t.Context(), native, bridge.account, bridge.incoming, "/v1/responses"))
		})
	}
}

func TestCodexAPIWSBridgeRequiresSuccessfulCompleteOutput(t *testing.T) {
	for _, terminal := range []string{
		`{"type":"response.failed","response":{"id":"resp","output":[]}}`,
		`{"type":"response.incomplete","response":{"id":"resp","output":[]}}`,
		`{"type":"response.completed","response":{"id":"resp"}}`,
	} {
		t.Run(gjson.Get(terminal, "type").String(), func(t *testing.T) {
			bridge, native := newCodexAPIBridgeFixture(t, 200, codexAPIBridgeHTTPFixture{})
			require.NoError(t, bridge.WriteFrame(t.Context(), coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-5","input":[]}`)))
			native.read <- []byte(terminal)
			codexAPIBridgeRead(t, bridge)
			codexAPIBridgeWrite(t, bridge, `{"type":"response.create","model":"gpt-5","previous_response_id":"resp","input":"`+strings.Repeat("x", 250)+`"}`)
			require.Equal(t, "previous_response_not_found", gjson.GetBytes(codexAPIBridgeRead(t, bridge), "error.code").String())
		})
	}
}

func TestCodexAPIWSBridgeNativeUnknownContinuationAndLaneIsolation(t *testing.T) {
	bridge, native := newCodexAPIBridgeFixture(t, 500, codexAPIBridgeHTTPFixture{})
	request := `{"type":"response.create","model":"gpt-5","stream_id":"a","previous_response_id":"outside","input":[]}`
	require.NoError(t, bridge.WriteFrame(t.Context(), coderws.MessageText, []byte(request)))
	require.Equal(t, request, string(<-native.written))
	native.read <- []byte(`{"type":"response.completed","stream_id":"a","response":{"id":"resp_external","output":[]}}`)
	codexAPIBridgeRead(t, bridge)
	bridge.mu.Lock()
	require.Empty(t, bridge.history)
	bridge.history[`"a"`] = codexAPIReplayHistory{id: "known", input: []json.RawMessage{json.RawMessage(`{"role":"user","content":"private"}`)}}
	bridge.mu.Unlock()
	codexAPIBridgeWrite(t, bridge, `{"type":"response.create","model":"gpt-5","stream_id":"b","previous_response_id":"known","input":"`+strings.Repeat("x", 600)+`"}`)
	require.Equal(t, "previous_response_not_found", gjson.GetBytes(codexAPIBridgeRead(t, bridge), "error.code").String())
	other, _ := newCodexAPIBridgeFixture(t, 1, codexAPIBridgeHTTPFixture{})
	codexAPIBridgeWrite(t, other, `{"type":"response.create","model":"gpt-5","stream_id":"a","previous_response_id":"known","input":[]}`)
	require.Equal(t, "previous_response_not_found", gjson.GetBytes(codexAPIBridgeRead(t, other), "error.code").String())
}

func TestCodexAPIWSBridgeHTTPModeRejectsWarmupAndReleasesOnDisconnect(t *testing.T) {
	started := make(chan context.Context, 1)
	count := 0
	bridge, _ := newCodexAPIBridgeFixture(t, 1, codexAPIBridgeHTTPFixture{do: func(r *http.Request, _ int64) (*http.Response, error) {
		count++
		if count == 1 {
			return codexAPIBridgeResponse("first", `[]`), nil
		}
		started <- r.Context()
		<-r.Context().Done()
		return nil, r.Context().Err()
	}})
	codexAPIBridgeWrite(t, bridge, `{"type":"response.create","model":"gpt-5","input":[]}`)
	codexAPIBridgeRead(t, bridge)
	codexAPIBridgeWrite(t, bridge, `{"type":"response.create","model":"gpt-5","generate":false,"input":[]}`)
	require.Equal(t, "unsupported_warmup", gjson.GetBytes(codexAPIBridgeRead(t, bridge), "error.code").String())
	codexAPIBridgeWrite(t, bridge, `{"type":"response.create","model":"gpt-5","input":[]}`)
	requestContext := <-started
	require.NoError(t, bridge.Close())
	select {
	case <-requestContext.Done():
	case <-time.After(time.Second):
		t.Fatal("HTTP request retained after connection close")
	}
	require.Equal(t, 2, count)
}

func TestCodexAPIWSBridgeRetainsCustomAndUnpairedToolCalls(t *testing.T) {
	bodies := make(chan []byte, 1)
	bridge, _ := newCodexAPIBridgeFixture(t, 1, codexAPIBridgeHTTPFixture{do: func(r *http.Request, _ int64) (*http.Response, error) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		bodies <- body
		return codexAPIBridgeResponse("next", `[]`), nil
	}})
	bridge.history[`"a"`] = codexAPIReplayHistory{id: "known", input: []json.RawMessage{
		json.RawMessage(`{"type":"custom_tool_call","id":"c","call_id":"custom","name":"custom","input":"run"}`),
		json.RawMessage(`{"type":"function_call","id":"f","call_id":"pending","name":"pending","arguments":"{}"}`),
	}}
	codexAPIBridgeWrite(t, bridge, `{"type":"response.create","model":"gpt-5","stream_id":"\u0061","previous_response_id":"known","input":[{"type":"custom_tool_call_output","call_id":"custom","output":"ok"}]}`)
	event := codexAPIBridgeRead(t, bridge)
	require.Equal(t, "next", gjson.GetBytes(event, "response.id").String())
	items := gjson.GetBytes(<-bodies, "input").Array()
	require.Len(t, items, 3)
	require.Equal(t, "custom_tool_call", items[0].Get("type").String())
	require.Equal(t, "pending", items[1].Get("call_id").String())
	require.Equal(t, "custom_tool_call_output", items[2].Get("type").String())
}

func TestCodexAPIWSBridgeRejectsOpaqueItemReferences(t *testing.T) {
	bridge, _ := newCodexAPIBridgeFixture(t, 1, codexAPIBridgeHTTPFixture{})
	codexAPIBridgeWrite(t, bridge, `{"type":"response.create","model":"gpt-5","input":[{"type":"item_reference","id":"unobserved"}]}`)
	require.Equal(t, "incomplete_replay", gjson.GetBytes(codexAPIBridgeRead(t, bridge), "error.code").String())
}

func TestCodexAPIWSBridgeForwardsTerminalAfterBareError(t *testing.T) {
	bridge, _ := newCodexAPIBridgeFixture(t, 1, codexAPIBridgeHTTPFixture{do: func(*http.Request, int64) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("data: {\"type\":\"error\",\"error\":{\"message\":\"recoverable\"}}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"final\",\"output\":[],\"usage\":{\"input_tokens\":5}}}\n\n"))}, nil
	}})
	codexAPIBridgeWrite(t, bridge, `{"type":"response.create","model":"gpt-5","input":[]}`)
	require.Equal(t, "error", gjson.GetBytes(codexAPIBridgeRead(t, bridge), "type").String())
	terminal := codexAPIBridgeRead(t, bridge)
	require.Equal(t, "response.completed", gjson.GetBytes(terminal, "type").String())
	require.EqualValues(t, 5, gjson.GetBytes(terminal, "response.usage.input_tokens").Int())
}

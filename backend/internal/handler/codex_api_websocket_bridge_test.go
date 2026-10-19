package handler

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestCodexAPIWebSocketLargeFirstTurnUsesHTTP(t *testing.T) {
	var nativePermit atomic.Bool
	native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()
		nativePermit.Store(true)
		defer nativePermit.Store(false)
		_, _, _ = conn.Read(r.Context())
	}))
	defer native.Close()
	requests := make(chan *http.Request, 1)
	upstream := codexAPIHandlerUpstream{do: func(r *http.Request) (*http.Response, error) {
		if nativePermit.Load() {
			return &http.Response{StatusCode: http.StatusTooManyRequests, Body: io.NopCloser(strings.NewReader(`{"error":{"code":"gateway_busy","message":"fixture concurrency limit is one"}}`))}, nil
		}
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.Equal(t, 17<<20, len(gjson.GetBytes(body, "input.0.content.0.image_url").String()))
		require.False(t, gjson.GetBytes(body, "previous_response_id").Exists())
		requests <- r
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_large\",\"output\":[]}}\n\n"))}, nil
	}}
	router, _ := newCodexAPITimingFixture(t, native.URL, upstream, func(cfg *config.Config) { cfg.Gateway.OpenAIWS.HTTPBridgeEnabled = true })
	server := httptest.NewServer(router)
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	client, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/v1/responses", nil)
	require.NoError(t, err)
	defer func() { _ = client.CloseNow() }()
	payload := []byte(`{"type":"response.create","model":"gpt-5.1","stream":true,"input":[{"role":"user","content":[{"type":"input_image","image_url":"` + strings.Repeat("x", 17<<20) + `"}]}]}`)
	require.NoError(t, client.Write(ctx, coderws.MessageText, payload))
	_, frame, err := client.Read(ctx)
	require.NoError(t, err)
	require.Equal(t, "resp_large", gjson.GetBytes(frame, "response.id").String())
	select {
	case request := <-requests:
		require.Equal(t, "/backend-api/codex/responses", request.URL.Path)
		require.Equal(t, "Bearer test-key", request.Header.Get("Authorization"))
	default:
		t.Fatal("large Responses request did not reach HTTP")
	}
}

func TestCodexAPIWebSocketHTTPBridgePreservesUsageAndFirstTokenTiming(t *testing.T) {
	native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()
		_, _, _ = conn.Read(r.Context())
	}))
	defer native.Close()
	upstream := codexAPIHandlerUpstream{do: func(*http.Request) (*http.Response, error) {
		reader, writer := io.Pipe()
		go func() {
			defer func() { _ = writer.Close() }()
			for _, frame := range []string{
				`{"type":"response.created","response":{"id":"resp_bridge_usage"}}`,
				`{"type":"response.output_text.delta","delta":"hello"}`,
				`{"type":"response.completed","response":{"id":"resp_bridge_usage","model":"gpt-5.1","output":[],"usage":{"input_tokens":12,"output_tokens":3}}}`,
			} {
				time.Sleep(40 * time.Millisecond)
				if _, err := io.WriteString(writer, "data: "+frame+"\n\n"); err != nil {
					return
				}
			}
		}()
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: reader}, nil
	}}
	router, logs := newCodexAPITimingFixture(t, native.URL, upstream, func(cfg *config.Config) {
		cfg.Gateway.OpenAIWS.HTTPBridgeEnabled = true
		cfg.Gateway.OpenAIWS.HTTPBridgeThresholdBytes = 1
	})
	server := httptest.NewServer(router)
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	client, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/v1/responses", nil)
	require.NoError(t, err)
	defer func() { _ = client.CloseNow() }()
	time.Sleep(100 * time.Millisecond)
	started := time.Now()
	require.NoError(t, client.Write(ctx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-5.1","input":[]}`)))
	for range 3 {
		_, _, err := client.Read(ctx)
		require.NoError(t, err)
	}
	select {
	case log := <-logs:
		require.Equal(t, 12, log.InputTokens)
		require.Equal(t, 3, log.OutputTokens)
		require.NotNil(t, log.FirstTokenMs)
		require.NotNil(t, log.DurationMs)
		require.GreaterOrEqual(t, *log.FirstTokenMs, 80)
		require.Less(t, *log.FirstTokenMs, *log.DurationMs)
		require.LessOrEqual(t, int64(*log.DurationMs), time.Since(started).Milliseconds())
	case <-ctx.Done():
		t.Fatal("bridged usage was not recorded")
	}
}

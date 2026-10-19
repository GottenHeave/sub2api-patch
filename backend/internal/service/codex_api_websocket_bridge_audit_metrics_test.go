package service

import (
	"context"
	"strings"
	"testing"
	"time"

	openaiwsv2 "github.com/Wei-Shaw/sub2api/internal/service/openai_ws_v2"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestCodexAPIWSBridgeAuditLocalRejectionPreservesActiveTiming(t *testing.T) {
	t.Run("before_created", func(t *testing.T) { testCodexAPILocalRejectionTiming(t, true) })
	t.Run("after_created", func(t *testing.T) { testCodexAPILocalRejectionTiming(t, false) })
}

func testCodexAPILocalRejectionTiming(t *testing.T, beforeCreated bool) {
	bridge, native := newCodexAPIBridgeFixture(t, 500, codexAPIBridgeHTTPFixture{})
	client := &codexAPIBridgeNativeFixture{read: make(chan []byte, 8), written: make(chan []byte, 8), closed: make(chan struct{})}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	turns := make(chan openaiwsv2.RelayTurnResult, 8)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = openaiwsv2.Relay(ctx, client, bridge, nil, openaiwsv2.RelayOptions{
			FirstMessageSent: true,
			OnTurnComplete: func(turn openaiwsv2.RelayTurnResult) {
				turns <- turn
			},
		})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("relay did not release on cancellation")
		}
	})
	client.read <- []byte(`{"type":"response.create","model":"gpt-5","input":[]}`)
	<-native.written
	created := func() {
		native.read <- []byte(`{"type":"response.created","response":{"id":"active-turn"}}`)
		require.Equal(t, "response.created", gjson.GetBytes(<-client.written, "type").String())
	}
	token := func() {
		native.read <- []byte(`{"type":"response.output_text.delta","response_id":"active-turn","delta":"token"}`)
		require.Equal(t, "response.output_text.delta", gjson.GetBytes(<-client.written, "type").String())
	}
	if !beforeCreated {
		created()
	}
	time.Sleep(30 * time.Millisecond)
	if !beforeCreated {
		token()
	}
	client.read <- []byte(`{"type":"response.create","model":"rejected-model","stream_id":"other","input":"` + strings.Repeat("x", 600) + `"}`)
	rejection := <-client.written
	require.Equal(t, "bridge_busy", gjson.GetBytes(rejection, "error.code").String())
	if beforeCreated {
		created()
		token()
	}
	native.read <- []byte(`{"type":"response.completed","response":{"id":"active-turn","output":[],"usage":{"input_tokens":9,"output_tokens":1}}}`)
	require.Equal(t, "response.completed", gjson.GetBytes(<-client.written, "type").String())
	for {
		select {
		case turn := <-turns:
			if turn.TerminalEventType != "response.completed" {
				continue
			}
			require.Equal(t, "active-turn", turn.RequestID)
			require.Equal(t, "gpt-5", turn.RequestModel)
			require.Equal(t, 9, turn.Usage.InputTokens)
			require.Equal(t, 1, turn.Usage.OutputTokens)
			require.NotNil(t, turn.FirstTokenMs, "a connection-local rejection must not settle the active upstream turn")
			require.GreaterOrEqual(t, *turn.FirstTokenMs, 30)
			require.GreaterOrEqual(t, turn.Duration, 30*time.Millisecond)
			return
		case <-ctx.Done():
			t.Fatal("active upstream turn did not complete")
		}
	}
}

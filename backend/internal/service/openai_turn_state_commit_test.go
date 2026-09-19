package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type turnStateFirstWriteObserver struct {
	gin.ResponseWriter
	onFirstWrite func()
}

func (w *turnStateFirstWriteObserver) Write(data []byte) (int, error) {
	n, err := w.ResponseWriter.Write(data)
	if w.onFirstWrite != nil {
		callback := w.onFirstWrite
		w.onFirstWrite = nil
		callback()
	}
	return n, err
}

func TestTurnStateOwnershipStreamingRecordsAtDelivery(t *testing.T) {
	svc := &OpenAIGatewayService{cfg: &config.Config{}}
	a := turnStateOwnershipAccount(101, "account-a")
	b := turnStateOwnershipAccount(102, "account-b")
	c, recorder := newTurnStateTestContext(t, 7, "live-session")
	called := false
	c.Writer = &turnStateFirstWriteObserver{ResponseWriter: c.Writer, onFirstWrite: func() {
		called = true
		require.Equal(t, "live-state-a", recorder.Result().Header.Get(openAIWSTurnStateHeader))
		nextRequest, _ := newTurnStateTestContext(t, 7, "live-session")
		outbound := http.Header{}
		outbound.Set(openAIWSTurnStateHeader, "live-state-a")
		svc.guardOpenAICodexTurnStateEcho(nextRequest, b, outbound)
		require.Empty(t, outbound.Get(openAIWSTurnStateHeader), "delivered state must be guarded while the stream is still running")
	}}
	upstream := http.Header{}
	upstream.Set(openAIWSTurnStateHeader, "live-state-a")
	upstream.Set("Content-Type", "text/event-stream")
	stream := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_live\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n"
	resp := &http.Response{StatusCode: http.StatusOK, Header: upstream, Body: io.NopCloser(strings.NewReader(stream))}
	_, err := svc.handleStreamingResponsePassthrough(context.Background(), resp, c, a, time.Now(), "", "")
	require.NoError(t, err)
	require.True(t, called)
}

func TestTurnStateOwnershipLocalBodyErrorPreservesDeliveredProvenance(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.UpstreamResponseReadMaxBytes = 1
	svc := &OpenAIGatewayService{cfg: cfg}
	a := turnStateOwnershipAccount(101, "account-a")
	b := turnStateOwnershipAccount(102, "account-b")
	c, recorder := newTurnStateTestContext(t, 7, "local-error-session")
	svc.noteOpenAICodexTurnStateProvenance(c, a, "delivered-state-a")
	upstream := http.Header{}
	upstream.Set(openAIWSTurnStateHeader, "undelivered-state-b")
	resp := &http.Response{StatusCode: http.StatusOK, Header: upstream, Body: io.NopCloser(strings.NewReader(`{"id":"too-large"}`))}
	_, err := svc.handleNonStreamingResponsePassthrough(context.Background(), resp, c, b, "", "")
	require.Error(t, err)
	require.Empty(t, recorder.Result().Header.Get(openAIWSTurnStateHeader))
	outbound := http.Header{}
	outbound.Set(openAIWSTurnStateHeader, "delivered-state-a")
	svc.guardOpenAICodexTurnStateEcho(c, b, outbound)
	require.Empty(t, outbound.Get(openAIWSTurnStateHeader))
}

func TestTurnStateOwnershipCommitWriter(t *testing.T) {
	for _, method := range []string{"write", "write_string", "flush", "write_header_now"} {
		t.Run(method, func(t *testing.T) {
			svc := &OpenAIGatewayService{}
			a := turnStateOwnershipAccount(101, "account-a")
			b := turnStateOwnershipAccount(102, "account-b")
			c, recorder := newTurnStateTestContext(t, 7, "writer-session")
			original := c.Writer
			restore := svc.observeOpenAICodexTurnStateCommit(c, a)
			c.Writer.Header().Set(openAIWSTurnStateHeader, "emitted-a")
			c.Writer.WriteHeader(http.StatusAccepted)
			require.False(t, c.Writer.Written(), "Gin WriteHeader only selects status")
			switch method {
			case "write":
				_, err := c.Writer.Write([]byte("data"))
				require.NoError(t, err)
			case "write_string":
				_, err := c.Writer.WriteString("data")
				require.NoError(t, err)
			case "flush":
				c.Writer.Flush()
			case "write_header_now":
				c.Writer.WriteHeaderNow()
			}
			require.Equal(t, http.StatusAccepted, recorder.Code)
			require.Equal(t, "emitted-a", recorder.Result().Header.Get(openAIWSTurnStateHeader))
			c.Writer.Header().Set(openAIWSTurnStateHeader, "late-header-mutation")
			c.Writer.Flush()
			h := http.Header{}
			h.Set(openAIWSTurnStateHeader, "emitted-a")
			svc.guardOpenAICodexTurnStateEcho(c, b, h)
			require.Empty(t, h.Get(openAIWSTurnStateHeader))
			h.Set(openAIWSTurnStateHeader, "late-header-mutation")
			svc.guardOpenAICodexTurnStateEcho(c, b, h)
			require.Equal(t, "late-header-mutation", h.Get(openAIWSTurnStateHeader))
			restore()
			require.Same(t, original, c.Writer)
		})
	}
}

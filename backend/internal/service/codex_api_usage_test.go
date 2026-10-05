package service

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"context"
	"io"
	"testing"
	"testing/synctest"
	"time"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/require"
)

func TestCodexAPIUsageObserverJSON(t *testing.T) {
	observer := NewCodexAPIUsageObserver("application/json", "")
	body := []byte(`{"id":"response","model":"upstream-model","usage":{"input_tokens":12,"output_tokens":3,"input_tokens_details":{"cached_tokens":5}}}`)
	original := bytes.Clone(body)
	_, err := observer.Write(body)
	require.NoError(t, err)
	require.Equal(t, original, body)
	result, ok := observer.Result()
	require.True(t, ok)
	require.Equal(t, 12, result.Usage.InputTokens)
	require.Equal(t, 3, result.Usage.OutputTokens)
	require.Equal(t, 5, result.Usage.CacheReadInputTokens)
	require.Equal(t, "upstream-model", result.UpstreamResponseModel)
}

func TestCodexAPIUsageObserverImagesAndCompressedResource(t *testing.T) {
	observer := NewCodexAPIUsageObserver("application/json", "")
	_, _ = observer.Write([]byte(`{"data":[{"b64_json":"a"},{"url":"https://example.test/image"}],"usage":{"input_tokens":2,"output_tokens":3}}`))
	result, ok := observer.Result()
	require.True(t, ok)
	require.Equal(t, 2, result.ImageCount)
	var body bytes.Buffer
	w := gzip.NewWriter(&body)
	_, err := w.Write([]byte(`{"id":"file-owned"}`))
	require.NoError(t, err)
	require.NoError(t, w.Close())
	observer = NewCodexAPIUsageObserver("application/json", "gzip")
	_, _ = observer.Write(body.Bytes())
	require.Equal(t, "file-owned", observer.ResourceID())
	_, ok = observer.Result()
	require.False(t, ok)
}

func TestCodexAPIUsageObserverPerCallBilling(t *testing.T) {
	for _, completed := range []bool{true, false} {
		observer := NewCodexAPIUsageObserver("application/json", "")
		_, _ = observer.Write([]byte(`{"results":[]}`))
		result, ok := observer.ResultForEndpoint("/v1/alpha/search", completed)
		require.Equal(t, completed, ok)
		if ok {
			require.Equal(t, 1, result.WebSearchCalls)
			require.Equal(t, OpenAIUsage{}, result.Usage)
		}
	}
	observer := NewCodexAPIUsageObserver("application/json", "")
	_, _ = observer.Write([]byte(`{"data":[{"b64_json":"actual-output"}]}`))
	result, ok := observer.Result()
	require.True(t, ok)
	require.Equal(t, 1, result.ImageCount)
	require.Equal(t, OpenAIUsage{}, result.Usage)
}

func TestCodexAPIUsageObserverSSE(t *testing.T) {
	observer := NewCodexAPIUsageObserver("text/event-stream; charset=utf-8", "")
	body := "data: {\"type\":\"response.created\",\"response\":{\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\r\n\r\n" +
		"event: response.completed\r\ndata: {\"response\":{\"usage\":\r\ndata: {\"input_tokens\":20,\"output_tokens\":4}}}\r\n\r\n"
	for _, b := range []byte(body) {
		_, err := observer.Write([]byte{b})
		require.NoError(t, err)
	}
	result, ok := observer.Result()
	require.True(t, ok)
	require.Equal(t, 20, result.Usage.InputTokens)
	require.Equal(t, 4, result.Usage.OutputTokens)
}

func TestCodexAPIUsageObserverFirstTokenModes(t *testing.T) {
	type timingCase struct {
		name     string
		event    string
		semantic int
		visible  int
	}
	for _, tc := range []timingCase{
		{"text", `{"type":"response.output_text.delta","delta":"hello"}`, 50, 50},
		{"reasoning", `{"type":"response.reasoning_summary_text.delta","delta":"thinking"}`, 50, 50},
		{"tool", `{"type":"response.function_call_arguments.delta","delta":"{}"}`, 50, 50},
		{"structure", `{"type":"response.output_item.added","item":{"type":"message","content":[]}}`, 50, 200},
		{"empty delta", `{"type":"response.output_text.delta","delta":""}`, 50, 200},
		{"metadata", `{"type":"response.in_progress"}`, 200, 200},
	} {
		for _, mode := range []string{OpenAITTFTModeSemantic, OpenAITTFTModeVisible} {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					observer := (&OpenAIGatewayService{}).NewCodexAPIUsageObserver(context.Background(), "text/event-stream", "", time.Now())
					observer.ttftMode = mode
					_, _ = observer.Write([]byte(": ping\n\ndata: {\"type\":\"response.created\"}\n\n"))
					time.Sleep(50 * time.Millisecond)
					_, _ = observer.Write([]byte("data: " + tc.event + "\n\n"))
					time.Sleep(150 * time.Millisecond)
					_, _ = observer.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"output\":[{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"hello\"}]}],\"usage\":{\"input_tokens\":2,\"output_tokens\":1}}}\n\n"))
					time.Sleep(time.Second)
					result, ok := observer.Result()
					require.True(t, ok)
					require.NotNil(t, result.FirstTokenMs)
					want := tc.semantic
					if mode == OpenAITTFTModeVisible {
						want = tc.visible
					}
					require.Equal(t, want, *result.FirstTokenMs)
				})
			})
		}
	}
}

func TestCodexAPIUsageObserverDoesNotInventFirstToken(t *testing.T) {
	for _, contentType := range []string{"application/json", "text/event-stream"} {
		t.Run(contentType, func(t *testing.T) {
			observer := (&OpenAIGatewayService{}).NewCodexAPIUsageObserver(context.Background(), contentType, "", time.Now().Add(-time.Second))
			body := `{"type":"response.failed","response":{"usage":{"input_tokens":2,"output_tokens":0}}}`
			if contentType == "text/event-stream" {
				body = "data: " + body + "\n\n"
			}
			_, _ = observer.Write([]byte(body))
			result, ok := observer.Result()
			require.True(t, ok)
			require.Nil(t, result.FirstTokenMs)
		})
	}
}

func TestCodexAPIUsageObserverBindsStreamingResourceBeforeDelivery(t *testing.T) {
	observer := NewCodexAPIUsageObserver("text/event-stream", "")
	var bound string
	observer.OnResource(func(id string) { bound = id })
	_, err := observer.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"response-owned\"}}\n\n"))
	require.NoError(t, err)
	require.Equal(t, "response-owned", bound)
	_, ok := observer.Result()
	require.False(t, ok)
}

func TestCodexAPIUsageObserverMissingUsage(t *testing.T) {
	for _, body := range []string{`{}`, `{"usage":{}}`, `{"usage":{"input_tokens":"bad","output_tokens":0}}`, `{"usage":{"input_tokens":-1,"output_tokens":2}}`} {
		t.Run(body, func(t *testing.T) {
			observer := NewCodexAPIUsageObserver("application/json", "")
			_, _ = observer.Write([]byte(body))
			_, ok := observer.Result()
			require.False(t, ok)
		})
	}
}

func TestCodexAPIUsageObserverCompressed(t *testing.T) {
	var body bytes.Buffer
	w := gzip.NewWriter(&body)
	_, err := w.Write([]byte(`{"usage":{"input_tokens":2,"output_tokens":0}}`))
	require.NoError(t, err)
	require.NoError(t, w.Close())
	for _, corrupt := range []bool{false, true} {
		observer := NewCodexAPIUsageObserver("application/json", "gzip")
		data := bytes.Clone(body.Bytes())
		if corrupt {
			data[len(data)-1] ^= 1
		}
		_, _ = observer.Write(data)
		result, ok := observer.Result()
		require.Equal(t, !corrupt, ok)
		if ok {
			require.Equal(t, 2, result.Usage.InputTokens)
		}
	}
}

func TestCodexAPIUsageObserverLimits(t *testing.T) {
	observer := NewCodexAPIUsageObserver("application/json", "")
	_, _ = observer.Write(bytes.Repeat([]byte{' '}, codexAPIObservationLimit+1))
	_, _ = observer.Write([]byte(`{"usage":{"input_tokens":1,"output_tokens":1}}`))
	_, ok := observer.Result()
	require.False(t, ok)
	require.Empty(t, observer.buffer)

	observer = NewCodexAPIUsageObserver("text/event-stream", "")
	_, _ = observer.Write(append([]byte("data: "), bytes.Repeat([]byte{'x'}, codexAPIObservationLimit)...))
	_, _ = observer.Write([]byte("\r\n\r\ndata: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":8,\"output_tokens\":2}}}\r\n\r\n"))
	result, ok := observer.Result()
	require.True(t, ok)
	require.Equal(t, 8, result.Usage.InputTokens)
}

func TestCodexAPIUsageObserverEncodings(t *testing.T) {
	for _, encoding := range []string{"gzip", "deflate", "br", "zstd"} {
		t.Run(encoding, func(t *testing.T) {
			var body bytes.Buffer
			var writer io.WriteCloser
			switch encoding {
			case "gzip":
				writer = gzip.NewWriter(&body)
			case "deflate":
				writer = zlib.NewWriter(&body)
			case "br":
				writer = brotli.NewWriter(&body)
			case "zstd":
				var err error
				writer, err = zstd.NewWriter(&body, zstd.WithEncoderConcurrency(1))
				require.NoError(t, err)
			}
			_, err := io.WriteString(writer, "data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":7,\"output_tokens\":2}}}\n\n")
			require.NoError(t, err)
			require.NoError(t, writer.Close())
			observer := (&OpenAIGatewayService{}).NewCodexAPIUsageObserver(context.Background(), "text/event-stream", encoding, time.Now().Add(-time.Second))
			_, _ = observer.Write(body.Bytes())
			result, ok := observer.Result()
			require.True(t, ok)
			require.Equal(t, 7, result.Usage.InputTokens)
			require.Nil(t, result.FirstTokenMs, "decoding buffered bytes cannot recover their event arrival times")
		})
	}
}

func TestCodexAPIUsageObserverOversizeTailRetainsUsage(t *testing.T) {
	observer := NewCodexAPIUsageObserver("text/event-stream", "")
	_, _ = observer.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":8,\"output_tokens\":2}}}\n\n"))
	_, _ = observer.Write(append([]byte("data: "), bytes.Repeat([]byte{'x'}, codexAPIObservationLimit)...))
	result, ok := observer.Result()
	require.True(t, ok)
	require.Equal(t, 8, result.Usage.InputTokens)
}

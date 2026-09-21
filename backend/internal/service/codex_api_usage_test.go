package service

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"io"
	"testing"

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
			observer := NewCodexAPIUsageObserver("text/event-stream", encoding)
			_, _ = observer.Write(body.Bytes())
			result, ok := observer.Result()
			require.True(t, ok)
			require.Equal(t, 7, result.Usage.InputTokens)
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

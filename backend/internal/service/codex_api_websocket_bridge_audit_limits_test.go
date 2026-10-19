package service

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestCodexAPIWSBridgeAuditResponseLimitCannotEmitTruncatedTerminal(t *testing.T) {
	bridge, _ := newCodexAPIBridgeFixture(t, 1, codexAPIBridgeHTTPFixture{do: func(*http.Request, int64) (*http.Response, error) {
		padding := ":" + strings.Repeat(" ", int(CodexAPIResponsesBodyLimit)-128) + "\n"
		terminal := "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"truncated\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"" + strings.Repeat("x", 300) + "\"}]}]}}\n\n"
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(io.MultiReader(strings.NewReader(padding), strings.NewReader(terminal)))}, nil
	}})
	codexAPIBridgeWrite(t, bridge, `{"type":"response.create","stream":true,"model":"gpt-5","input":[]}`)
	event := codexAPIBridgeRead(t, bridge)
	require.True(t, gjson.ValidBytes(event), "bounded read forwarded an invalid partial SSE event: %s", event)
	require.Equal(t, "response.failed", gjson.GetBytes(event, "type").String(), "response byte overflow must not report successful completion")
}

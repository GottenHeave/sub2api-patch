package service

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIOutboundMarkersInstructions(t *testing.T) {
	t.Run("image bridge", func(t *testing.T) {
		body := map[string]any{
			"model": "gpt-5.4", "instructions": "Caller instructions",
			"tools": []any{map[string]any{"type": "image_generation"}},
		}
		require.True(t, applyCodexImageGenerationBridgeInstructions(body))
		require.False(t, applyCodexImageGenerationBridgeInstructions(body))
		encoded, err := marshalOpenAIUpstreamJSON(body)
		require.NoError(t, err)
		require.NotContains(t, strings.ToLower(string(encoded)), "sub2api")
		require.Contains(t, body["instructions"], "Caller instructions")
		require.Contains(t, body["instructions"], "image_generation")
		require.NotContains(t, body["instructions"], "<")
		require.Equal(t, "Caller instructions\n\n"+codexImageGenerationBridgeText, body["instructions"])
	})
	t.Run("spark transform", func(t *testing.T) {
		body := map[string]any{"model": "gpt-5.3-codex-spark", "instructions": "Caller instructions"}
		result := applyCodexOAuthTransform(body, true, false)
		require.NoError(t, result.Error)
		require.False(t, applyCodexSparkImageUnsupportedInstructions(body))
		encoded, err := marshalOpenAIUpstreamJSON(body)
		require.NoError(t, err)
		require.NotContains(t, strings.ToLower(string(encoded)), "sub2api")
		require.Contains(t, body["instructions"], "does not support image generation")
		require.NotContains(t, body["instructions"], "<")
		require.Equal(t, "Caller instructions\n\n"+codexSparkImageUnsupportedText, body["instructions"])
	})
}

func TestOpenAIOutboundMarkersTodoGuard(t *testing.T) {
	input := json.RawMessage(`[{"type":"message","role":"user","content":"Hello"}]`)
	req := &apicompat.ResponsesRequest{Input: input}
	require.True(t, appendOpenAICompatClaudeCodeTodoGuard(req))
	require.False(t, appendOpenAICompatClaudeCodeTodoGuard(req))
	require.NotContains(t, strings.ToLower(string(req.Input)), "sub2api")
	require.Equal(t, "developer", gjson.GetBytes(req.Input, "0.role").String())
	require.Contains(t, string(req.Input), "in_progress")
	require.Equal(t, openAICompatClaudeCodeTodoGuardText, gjson.GetBytes(req.Input, "0.content.0.text").String())

	var items []any
	require.NoError(t, json.Unmarshal(input, &items))
	body := map[string]any{"input": items}
	require.True(t, appendOpenAICompatClaudeCodeTodoGuardToRequestBody(body))
	require.False(t, appendOpenAICompatClaudeCodeTodoGuardToRequestBody(body))
	encoded, err := marshalOpenAIUpstreamJSON(body)
	require.NoError(t, err)
	require.NotContains(t, strings.ToLower(string(encoded)), "sub2api")
	require.JSONEq(t, string(req.Input), gjson.GetBytes(encoded, "input").Raw)
	require.True(t, isOpenAICompatMessagesBridgeBody(encoded))
	require.True(t, isOpenAICompatMessagesBridgeRequestBody(body))
}

func TestOpenAIOutboundMarkersReservedToolRoundTrip(t *testing.T) {
	body := []byte(`{"tools":[{"type":"function","name":"python"}],"tool_choice":{"type":"function","name":"python"},"input":[{"type":"function_call","name":"python","call_id":"call_1"}]}`)
	aliased, reverse, changed, err := aliasOpenAIOAuthReservedToolNamesBody(body)
	require.NoError(t, err)
	require.True(t, changed)
	require.NotContains(t, strings.ToLower(string(aliased)), "sub2api")
	name := gjson.GetBytes(aliased, "tools.0.name").String()
	require.NotEqual(t, "python", name)
	require.Equal(t, "namespace__pi", name)
	require.Equal(t, name, gjson.GetBytes(aliased, "tool_choice.name").String())
	require.Equal(t, name, gjson.GetBytes(aliased, "input.0.name").String())
	response, err := json.Marshal(map[string]any{"output": []any{map[string]any{"type": "function_call", "name": name}}})
	require.NoError(t, err)
	restored := restoreCodexToolNamesInJSON(response, reverse)
	require.Equal(t, "python", gjson.GetBytes(restored, "output.0.name").String())
}

func TestOpenAIOutboundMarkersPreserveCallerText(t *testing.T) {
	const callerText = "Discuss sub2api and python__sub2api without changing this text."
	body := map[string]any{
		"model": "gpt-5.4", "instructions": callerText,
		"tools": []any{map[string]any{"type": "image_generation"}},
	}
	require.True(t, applyCodexImageGenerationBridgeInstructions(body))
	encoded, err := marshalOpenAIUpstreamJSON(body)
	require.NoError(t, err)
	require.Contains(t, gjson.GetBytes(encoded, "instructions").String(), callerText)
}

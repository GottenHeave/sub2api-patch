package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHasOpenAICodexExplicitSystemPromptBody(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{
			name: "top-level system_prompt",
			body: `{"model":"gpt-5.4","system_prompt":"Use the client's instructions.","input":"hello"}`,
			want: true,
		},
		{
			name: "responses system input",
			body: `{"model":"gpt-5.4","input":[{"type":"message","role":"system","content":"Use the client's instructions."}]}`,
			want: true,
		},
		{
			name: "empty system_prompt",
			body: `{"model":"gpt-5.4","system_prompt":"  ","input":"hello"}`,
			want: false,
		},
		{
			name: "no system prompt",
			body: `{"model":"gpt-5.4","input":"hello"}`,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, hasOpenAICodexExplicitSystemPromptBody([]byte(tt.body)))
		})
	}
}

func TestApplyInstructionsPreservesExplicitSystemPrompt(t *testing.T) {
	const systemPrompt = "Use the client's instructions exactly once."
	reqBody := map[string]any{
		"model":         "gpt-5.4",
		"system_prompt": systemPrompt,
	}

	modified := applyInstructions(reqBody, false)

	require.False(t, modified)
	require.Equal(t, systemPrompt, reqBody["system_prompt"])
	_, hasInstructions := reqBody["instructions"]
	require.False(t, hasInstructions)
}

func TestApplyInstructionsDoesNotAddDefaultForResponsesSystemInput(t *testing.T) {
	const systemPrompt = "Use the client's instructions exactly once."
	reqBody := map[string]any{
		"model": "gpt-5.4",
		"input": []any{
			map[string]any{
				"type":    "message",
				"role":    "system",
				"content": systemPrompt,
			},
		},
	}

	modified := applyInstructions(reqBody, false)

	require.False(t, modified)
	_, hasInstructions := reqBody["instructions"]
	require.False(t, hasInstructions)
}

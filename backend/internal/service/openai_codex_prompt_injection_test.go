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
			name: "structured top-level system_prompt",
			body: `{"model":"gpt-5.4","system_prompt":{"type":"text","text":"Use the client's instructions."},"input":"hello"}`,
			want: true,
		},
		{
			name: "nested structured top-level system_prompt",
			body: `{"model":"gpt-5.4","system_prompt":{"content":[{"type":"text","text":"Use the client's instructions."}]},"input":"hello"}`,
			want: true,
		},
		{
			name: "responses system input",
			body: `{"model":"gpt-5.4","input":[{"type":"message","role":"system","content":"Use the client's instructions."}]}`,
			want: true,
		},
		{
			name: "structured responses system input",
			body: `{"model":"gpt-5.4","input":[{"type":"message","role":"system","content":{"type":"text","text":"Use the client's instructions."}}]}`,
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

func TestApplyInstructionsPreservesStructuredSystemPrompts(t *testing.T) {
	const systemPrompt = "Use the client's structured instructions exactly once."
	tests := []struct {
		name    string
		reqBody map[string]any
	}{
		{
			name: "top-level text object",
			reqBody: map[string]any{
				"model": "gpt-5.4",
				"system_prompt": map[string]any{
					"type": "text",
					"text": systemPrompt,
				},
			},
		},
		{
			name: "top-level nested content object",
			reqBody: map[string]any{
				"model": "gpt-5.4",
				"system_prompt": map[string]any{
					"content": []any{
						map[string]any{"type": "text", "text": systemPrompt},
					},
				},
			},
		},
		{
			name: "system input content object",
			reqBody: map[string]any{
				"model": "gpt-5.4",
				"input": []any{
					map[string]any{
						"type": "message",
						"role": "system",
						"content": map[string]any{
							"type": "text",
							"text": systemPrompt,
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			modified := applyInstructions(tt.reqBody, false)

			require.False(t, modified)
			_, hasInstructions := tt.reqBody["instructions"]
			require.False(t, hasInstructions)
		})
	}
}

func TestApplyCodexOAuthTransformPromotesStructuredSystemInput(t *testing.T) {
	const systemPrompt = "Use the client's structured instructions exactly once."
	reqBody := map[string]any{
		"model": "gpt-5.4",
		"input": []any{
			map[string]any{
				"type": "message",
				"role": "system",
				"content": map[string]any{
					"type": "text",
					"text": systemPrompt,
				},
			},
		},
	}

	applyCodexOAuthTransformWithOptions(reqBody, codexOAuthTransformOptions{})

	require.Equal(t, systemPrompt, reqBody["instructions"])
	input, ok := reqBody["input"].([]any)
	require.True(t, ok)
	require.Len(t, input, 1)
	message, ok := input[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "developer", message["role"])
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

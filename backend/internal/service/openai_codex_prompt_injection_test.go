package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAICodexPromptPresenceTable(t *testing.T) {
	const prompt = "Follow the caller's instructions."
	tests := []struct {
		name string
		body string
		want bool
	}{
		{name: "non-empty top-level string", body: `{"system_prompt":"` + prompt + `"}`, want: true},
		{name: "non-empty top-level array", body: `{"system_prompt":[{"type":"text","text":"` + prompt + `"}]}`, want: true},
		{name: "non-empty top-level structured object", body: `{"system_prompt":{"type":"text","text":"` + prompt + `"}}`, want: true},
		{name: "nested structured object", body: `{"system_prompt":{"content":[{"type":"text","text":"` + prompt + `"}]}}`, want: true},
		{name: "system input string", body: `{"input":[{"type":"message","role":"system","content":"` + prompt + `"}]}`, want: true},
		{name: "system input array", body: `{"input":[{"type":"message","role":"system","content":[{"type":"input_text","text":"` + prompt + `"}]}]}`, want: true},
		{name: "system input structured object", body: `{"input":[{"type":"message","role":"system","content":{"type":"text","text":"` + prompt + `"}}]}`, want: true},
		{name: "developer input string", body: `{"input":[{"type":"message","role":"developer","content":"` + prompt + `"}]}`, want: true},
		{name: "developer input array", body: `{"input":[{"type":"message","role":"developer","content":[{"type":"input_text","text":"` + prompt + `"}]}]}`, want: true},
		{name: "developer input structured object", body: `{"input":[{"type":"message","role":"developer","content":{"type":"text","text":"` + prompt + `"}}]}`, want: true},
		{name: "missing", body: `{}`, want: false},
		{name: "null", body: `{"system_prompt":null}`, want: false},
		{name: "whitespace-only string", body: `{"system_prompt":"  \t\n"}`, want: false},
		{name: "empty array", body: `{"system_prompt":[]}`, want: false},
		{name: "empty object", body: `{"system_prompt":{}}`, want: false},
		{name: "empty system input content", body: `{"input":[{"type":"message","role":"system","content":[]}]}`, want: false},
		{name: "empty developer input content", body: `{"input":[{"type":"message","role":"developer","content":[]}]}`, want: false},
		{name: "whitespace developer input content", body: `{"input":[{"type":"message","role":"developer","content":" \t\n"}]}`, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := []byte(tt.body)
			original := append([]byte(nil), body...)
			require.Equal(t, tt.want, hasOpenAICodexExplicitSystemPromptBody(body))
			require.Equal(t, original, body)
			var reqBody map[string]any
			require.NoError(t, json.Unmarshal(body, &reqBody))
			require.Equal(t, tt.want, hasOpenAICodexExplicitSystemPrompt(reqBody))
		})
	}
}

func TestApplyInstructionsUsesPromptPresenceTable(t *testing.T) {
	const prompt = "Follow the caller's instructions."
	tests := []struct {
		name       string
		reqBody    map[string]any
		wantInject bool
	}{
		{name: "missing", reqBody: map[string]any{"model": "gpt-5.4"}, wantInject: true},
		{name: "string", reqBody: map[string]any{"model": "gpt-5.4", "system_prompt": prompt}, wantInject: false},
		{name: "array", reqBody: map[string]any{"model": "gpt-5.4", "system_prompt": []any{map[string]any{"type": "text", "text": prompt}}}, wantInject: false},
		{name: "structured object", reqBody: map[string]any{"model": "gpt-5.4", "system_prompt": map[string]any{"type": "text", "text": prompt}}, wantInject: false},
		{name: "responses system input", reqBody: map[string]any{"model": "gpt-5.4", "input": []any{map[string]any{"type": "message", "role": "system", "content": prompt}}}, wantInject: false},
		{name: "responses developer input", reqBody: map[string]any{"model": "gpt-5.4", "input": []any{map[string]any{"type": "message", "role": "developer", "content": prompt}}}, wantInject: false},
		{name: "empty responses developer input", reqBody: map[string]any{"model": "gpt-5.4", "input": []any{map[string]any{"type": "message", "role": "developer", "content": "  "}}}, wantInject: true},
		{name: "null", reqBody: map[string]any{"model": "gpt-5.4", "system_prompt": nil}, wantInject: true},
		{name: "whitespace", reqBody: map[string]any{"model": "gpt-5.4", "system_prompt": "  "}, wantInject: true},
		{name: "empty array", reqBody: map[string]any{"model": "gpt-5.4", "system_prompt": []any{}}, wantInject: true},
		{name: "empty object", reqBody: map[string]any{"model": "gpt-5.4", "system_prompt": map[string]any{}}, wantInject: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before, err := json.Marshal(tt.reqBody)
			require.NoError(t, err)

			modified := applyInstructions(tt.reqBody, false)
			if tt.wantInject {
				require.True(t, modified)
				require.NotEmpty(t, tt.reqBody["instructions"])
				return
			}

			require.False(t, modified)
			require.NotContains(t, tt.reqBody, "instructions")
			after, err := json.Marshal(tt.reqBody)
			require.NoError(t, err)
			require.JSONEq(t, string(before), string(after))
		})
	}
}

func TestApplyCodexOAuthTransformPreservesStructuredSystemInput(t *testing.T) {
	const prompt = "Follow the caller's structured instructions."
	reqBody := map[string]any{
		"model": "gpt-5.4",
		"input": []any{map[string]any{
			"type": "message",
			"role": "system",
			"content": map[string]any{
				"type": "text",
				"text": prompt,
			},
		}},
	}

	result := applyCodexOAuthTransformWithOptions(reqBody, codexOAuthTransformOptions{})
	require.NoError(t, result.Error)
	require.Equal(t, prompt, reqBody["instructions"])
	input, ok := reqBody["input"].([]any)
	require.True(t, ok)
	require.Len(t, input, 1)
	message, ok := input[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "developer", message["role"])
}

func TestOpenAIGatewayForwardPreservesRawSystemPrompt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const prompt = "Use the caller's raw system prompt."
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}}
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = false
	svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream}
	account := &Account{
		ID: 1, Name: "openai-apikey", Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Concurrency: 1,
		Credentials: map[string]any{"api_key": "sk-test", "base_url": "https://example.com"},
		Extra:       map[string]any{"use_responses_api": true},
	}
	for _, tt := range []struct {
		name       string
		body       string
		promptPath string
	}{
		{"system prompt", `{"model":"gpt-5.4","stream":false,"system_prompt":{"type":"text","text":"` + prompt + `"},"input":[{"type":"message","content":"hi"}]}`, "system_prompt.text"},
		{"developer input", `{"model":"gpt-5.4","stream":false,"input":[{"type":"message","role":"developer","content":"` + prompt + `"},{"type":"message","role":"user","content":"hi"}]}`, "input.0.content"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			upstream.resp.Body = io.NopCloser(strings.NewReader(`{"usage":{"input_tokens":1,"output_tokens":1}}`))
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(nil))
			SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)

			result, err := svc.Forward(context.Background(), c, account, []byte(tt.body))
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, prompt, gjson.GetBytes(upstream.lastBody, tt.promptPath).String())
			require.False(t, gjson.GetBytes(upstream.lastBody, "instructions").Exists())
		})
	}
}

func TestOpenAIGatewayPassthroughPreservesRawSystemPrompt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const prompt = "Use the caller's passthrough prompt."
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
	}}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
	account := &Account{
		ID: 2, Name: "openai-oauth", Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 1,
		Credentials: map[string]any{"access_token": "oauth-token", "chatgpt_account_id": "chatgpt-acc"},
		Extra:       map[string]any{"openai_passthrough": true, "openai_oauth_responses_websockets_v2_mode": OpenAIWSIngressModeOff},
		Status:      StatusActive, Schedulable: true, RateMultiplier: f64p(1),
	}
	for _, tt := range []struct {
		name       string
		body       string
		promptPath string
	}{
		{"system prompt", `{"model":"gpt-5.4","stream":true,"system_prompt":[{"type":"text","text":"` + prompt + `"}],"input":[{"type":"text","text":"hi"}]}`, "system_prompt.0.text"},
		{"developer input", `{"model":"gpt-5.4","stream":true,"input":[{"type":"message","role":"developer","content":"` + prompt + `"},{"type":"message","role":"user","content":"hi"}]}`, "input.0.content"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			upstream.resp.Body = io.NopCloser(strings.NewReader(strings.Join([]string{
				`data: {"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}}`,
				"", "data: [DONE]", "",
			}, "\n")))
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(nil))
			c.Request.Header.Set("User-Agent", "codex_cli_rs/0.98.0")

			result, err := svc.Forward(context.Background(), c, account, []byte(tt.body))
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, prompt, gjson.GetBytes(upstream.lastBody, tt.promptPath).String())
			require.False(t, gjson.GetBytes(upstream.lastBody, "instructions").Exists())
		})
	}
}

func TestForwardAsChatCompletionsPreservesResponsesShapeSystemPrompt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const prompt = "Use the caller's Responses-shaped prompt."
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"test stop"}}`)),
	}}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
	account := &Account{
		ID: 3, Name: "openai-oauth", Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 1,
		Credentials: map[string]any{"access_token": "oauth-token", "chatgpt_account_id": "chatgpt-acc"},
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(nil))

	body := []byte(`{"model":"gpt-5.4","stream":false,"system_prompt":{"content":[{"type":"text","text":"` + prompt + `"}]},"input":[{"type":"message","role":"system","content":{"type":"text","text":"` + prompt + `"}},{"type":"message","role":"user","content":"hi"}]}`)
	result, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "")
	require.Error(t, err)
	require.Nil(t, result)
	require.Equal(t, prompt, gjson.GetBytes(upstream.lastBody, "system_prompt.content.0.text").String())
	require.Equal(t, prompt, gjson.GetBytes(upstream.lastBody, "instructions").String())
	require.Equal(t, "developer", gjson.GetBytes(upstream.lastBody, "input.0.role").String())
	require.Equal(t, prompt, gjson.GetBytes(upstream.lastBody, "input.0.content.text").String())
}

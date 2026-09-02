package service

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIRealtimeRESTMultipartMapsSessionAndPreservesSDP(t *testing.T) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	sdp := "v=0\r\no=- 123 2 IN IP4 127.0.0.1\r\n"
	require.NoError(t, writer.WriteField("sdp", sdp))
	part, err := writer.CreatePart(textproto.MIMEHeader{
		"Content-Disposition": {`form-data; name="session"`},
		"Content-Type":        {"application/json"},
	})
	require.NoError(t, err)
	_, err = io.WriteString(part, `{"type":"realtime","model":"client-realtime","instructions":"keep me"}`)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/realtime/calls?intent=quicksilver&architecture=avas", bytes.NewReader(body.Bytes()))
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())
	c.Request.Header.Set("OpenAI-Alpha", "quicksilver=v1")
	c.Request.Header.Set("X-Session-Id", "session-123")
	parsed, err := ParseOpenAIRealtimeRESTRequest(c, body.Bytes())
	require.NoError(t, err)
	require.Equal(t, "client-realtime", parsed.Model)
	require.Equal(t, "session.model", parsed.ScheduleModelPath)
	_, _, unchangedBody, err := buildOpenAIRealtimeRESTForwardBody(parsed, nil, nil)
	require.NoError(t, err)
	require.Equal(t, body.Bytes(), unchangedBody)
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{
		"api_key": "test-key", "model_mapping": map[string]any{"channel-realtime": "gpt-realtime"},
	}}
	upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusCreated,
		Header: http.Header{"Content-Type": {"application/sdp"}}, Body: io.NopCloser(bytes.NewBufferString(sdp))}}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
	result, err := svc.ForwardRealtimeREST(context.Background(), c, account, parsed, map[string]string{"session.model": "channel-realtime"})
	require.NoError(t, err)
	require.Equal(t, "client-realtime", result.Model)
	require.Equal(t, "gpt-realtime", result.UpstreamModel)
	require.Equal(t, "quicksilver", upstream.lastReq.URL.Query().Get("intent"))
	require.Equal(t, "avas", upstream.lastReq.URL.Query().Get("architecture"))
	require.Equal(t, "quicksilver=v1", upstream.lastReq.Header.Get("OpenAI-Alpha"))
	require.Equal(t, "session-123", upstream.lastReq.Header.Get("X-Session-Id"))
	require.Equal(t, writer.FormDataContentType(), upstream.lastReq.Header.Get("Content-Type"))
	reader := multipart.NewReader(bytes.NewReader(upstream.lastBody), writer.Boundary())
	sdpPart, err := reader.NextPart()
	require.NoError(t, err)
	sdpBody, err := io.ReadAll(sdpPart)
	require.NoError(t, err)
	require.Equal(t, sdp, string(sdpBody))
	sessionPart, err := reader.NextPart()
	require.NoError(t, err)
	require.Equal(t, "application/json", sessionPart.Header.Get("Content-Type"))
	sessionBody, err := io.ReadAll(sessionPart)
	require.NoError(t, err)
	require.JSONEq(t, `{"type":"realtime","model":"gpt-realtime","instructions":"keep me"}`, string(sessionBody))
}

func TestParseOpenAIRealtimeRESTRequestMalformedMultipart(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/realtime/calls", nil)
	c.Request.Header.Set("Content-Type", "multipart/form-data; boundary=test")
	_, err := ParseOpenAIRealtimeRESTRequest(c, []byte("--test\r\nContent-Disposition: form-data; name=\"session\"\r\n\r\n{"))
	require.Error(t, err)
}

func TestOpenAIRealtimeRESTCallControlSharesCallSessionHash(t *testing.T) {
	accept := &OpenAIRealtimeRESTRequest{CallID: "call_123", Endpoint: "/v1/realtime/calls/call_123/accept", Model: "gpt-realtime"}
	hangup := &OpenAIRealtimeRESTRequest{CallID: "call_123", Endpoint: "/v1/realtime/calls/call_123/hangup"}
	require.Equal(t, OpenAIRealtimeCallSessionHash("call_123"), accept.StickySessionSeed())
	require.Equal(t, accept.StickySessionSeed(), hangup.StickySessionSeed())
}

func TestParseOpenAIRealtimeRESTRequest_AcceptAction(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/realtime/calls/call_123/accept", bytes.NewReader(nil))

	parsed, err := ParseOpenAIRealtimeRESTRequest(c, []byte(`{"type":"realtime","model":"gpt-realtime"}`))

	require.NoError(t, err)
	require.Equal(t, "call_123", parsed.CallID)
	require.Equal(t, "gpt-realtime", parsed.Model)
}

func TestParseOpenAIRealtimeRESTRequest_ClientSecretsTranscriptionModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/realtime/client_secrets", bytes.NewReader(nil))

	parsed, err := ParseOpenAIRealtimeRESTRequest(c, []byte(`{"session":{"type":"transcription","audio":{"input":{"transcription":{"model":"gpt-4o-transcribe"}}}}}`))

	require.NoError(t, err)
	require.Equal(t, "/v1/realtime/client_secrets", parsed.Endpoint)
	require.Equal(t, "gpt-4o-transcribe", parsed.Model)
	require.Equal(t, "session.audio.input.transcription.model", parsed.ScheduleModelPath)
	require.Equal(t, []OpenAIRealtimeRESTModelRef{
		{Path: "session.audio.input.transcription.model", Value: "gpt-4o-transcribe"},
	}, parsed.ModelRefs)
}

func TestParseOpenAIRealtimeRESTRequest_PrefixedPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/realtime/calls/call_123/reject", bytes.NewReader(nil))

	parsed, err := ParseOpenAIRealtimeRESTRequest(c, []byte(`{"status_code":603}`))

	require.NoError(t, err)
	require.Equal(t, "/v1/realtime/calls/call_123/reject", parsed.Endpoint)
	require.Equal(t, "call_123", parsed.CallID)
	require.Equal(t, "reject", parsed.Action)
}

func TestOpenAIGatewayService_ForwardRealtimeREST_AcceptActionAPIKeyURLBodyAndHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"type":"realtime","model":"gpt-realtime","instructions":"answer calls"}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/realtime/calls/call_123/accept", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid_call"}},
		Body:       io.NopCloser(bytes.NewReader([]byte(`{"id":"call_123"}`))),
	}}
	svc := &OpenAIGatewayService{
		cfg:          &config.Config{},
		httpUpstream: upstream,
	}
	account := &Account{
		ID:          123,
		Name:        "acc",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key": "sk-test",
		},
		Status:      StatusActive,
		Schedulable: true,
	}
	parsed := &OpenAIRealtimeRESTRequest{CallID: "call_123", Endpoint: "/v1/realtime/calls/call_123/accept", Body: body, Model: "gpt-realtime", ScheduleModelPath: "model", ModelRefs: []OpenAIRealtimeRESTModelRef{{Path: "model", Value: "gpt-realtime"}}}

	result, err := svc.ForwardRealtimeREST(context.Background(), c, account, parsed, nil)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, upstream.lastReq)
	require.Equal(t, http.MethodPost, upstream.lastReq.Method)
	require.Equal(t, "https://api.openai.com/v1/realtime/calls/call_123/accept", upstream.lastReq.URL.String())
	require.Equal(t, "Bearer sk-test", upstream.lastReq.Header.Get("Authorization"))
	require.JSONEq(t, string(body), string(upstream.lastBody))
	require.JSONEq(t, `{"id":"call_123"}`, rec.Body.String())
}

func TestOpenAIGatewayService_ForwardRealtimeREST_AcceptActionOAuthURLBodyAndHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"type":"realtime","model":"client-realtime"}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/realtime/calls/call_123/accept", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("originator", "codex_cli_rs")

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid_call_oauth"}},
		Body:       io.NopCloser(bytes.NewReader([]byte(`{"id":"call_123"}`))),
	}}
	svc := &OpenAIGatewayService{
		cfg:          &config.Config{},
		httpUpstream: upstream,
	}
	account := &Account{
		ID:          123,
		Name:        "acc",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token":       "oauth-token",
			"chatgpt_account_id": "chatgpt-acc",
			"model_mapping": map[string]any{
				"client-realtime": "gpt-realtime",
			},
		},
		Status:      StatusActive,
		Schedulable: true,
	}
	parsed := &OpenAIRealtimeRESTRequest{CallID: "call_123", Endpoint: "/v1/realtime/calls/call_123/accept", Body: body, Model: "client-realtime", ScheduleModelPath: "model", ModelRefs: []OpenAIRealtimeRESTModelRef{{Path: "model", Value: "client-realtime"}}}

	result, err := svc.ForwardRealtimeREST(context.Background(), c, account, parsed, nil)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, upstream.lastReq)
	require.Equal(t, "https://api.openai.com/v1/realtime/calls/call_123/accept", upstream.lastReq.URL.String())
	require.Equal(t, "Bearer oauth-token", upstream.lastReq.Header.Get("Authorization"))
	require.Equal(t, "chatgpt-acc", upstream.lastReq.Header.Get("chatgpt-account-id"))
	require.Equal(t, "codex_cli_rs", upstream.lastReq.Header.Get("originator"))
	require.Equal(t, "gpt-realtime", gjson.GetBytes(upstream.lastBody, "model").String())
	require.JSONEq(t, `{"id":"call_123"}`, rec.Body.String())
}

func TestOpenAIGatewayService_ForwardRealtimeREST_OAuthCallBackendRequest(t *testing.T) {
	const sdp = "v=0\r\no=- 123 2 IN IP4 127.0.0.1\r\n"
	var multipartBody bytes.Buffer
	writer := multipart.NewWriter(&multipartBody)
	require.NoError(t, writer.WriteField("sdp", sdp))
	require.NoError(t, writer.WriteField("session", `{"type":"realtime","model":"client-realtime"}`))
	require.NoError(t, writer.Close())
	type requestCase struct {
		name        string
		contentType string
		body        []byte
	}
	cases := []requestCase{
		{name: "multipart", contentType: writer.FormDataContentType(), body: multipartBody.Bytes()},
		{name: "json", contentType: "application/json", body: []byte(`{"sdp":"v=0\r\no=- 123 2 IN IP4 127.0.0.1\r\n","session":{"type":"realtime","model":"client-realtime"}}`)},
		{name: "sdp", contentType: "application/sdp", body: []byte(sdp)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/realtime/calls?intent=quicksilver&architecture=avas", bytes.NewReader(tc.body))
			c.Request.Header.Set("Content-Type", tc.contentType)
			c.Request.Header.Set("OpenAI-Alpha", "quicksilver=v1")
			c.Request.Header.Set("X-Session-Id", "session-123")
			c.Request.Header.Set("X-Oai-Attestation", "attestation-test")
			c.Request.Header.Set("originator", "codex_cli_rs")
			parsed, err := ParseOpenAIRealtimeRESTRequest(c, tc.body)
			require.NoError(t, err)
			account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{
				"access_token": "oauth-token", "chatgpt_account_id": "chatgpt-acc",
				"model_mapping": map[string]any{"channel-realtime": "gpt-realtime"},
			}}
			upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusCreated,
				Header: http.Header{"Content-Type": {"application/sdp"}}, Body: io.NopCloser(bytes.NewBufferString(sdp))}}
			svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
			_, err = svc.ForwardRealtimeREST(context.Background(), c, account, parsed, map[string]string{"session.model": "channel-realtime"})
			require.NoError(t, err)
			require.Equal(t, "https://chatgpt.com/backend-api/codex/realtime/calls", upstream.lastReq.URL.Scheme+"://"+upstream.lastReq.URL.Host+upstream.lastReq.URL.Path)
			require.Equal(t, "quicksilver", upstream.lastReq.URL.Query().Get("intent"))
			require.Equal(t, "avas", upstream.lastReq.URL.Query().Get("architecture"))
			require.Equal(t, "Bearer oauth-token", upstream.lastReq.Header.Get("Authorization"))
			require.Equal(t, "chatgpt-acc", upstream.lastReq.Header.Get("chatgpt-account-id"))
			require.Equal(t, "codex_cli_rs", upstream.lastReq.Header.Get("originator"))
			require.Equal(t, "quicksilver=v1", upstream.lastReq.Header.Get("OpenAI-Alpha"))
			require.Equal(t, "session-123", upstream.lastReq.Header.Get("X-Session-Id"))
			require.Equal(t, "attestation-test", upstream.lastReq.Header.Get("X-Oai-Attestation"))
			if tc.name == "sdp" {
				require.Equal(t, "application/sdp", upstream.lastReq.Header.Get("Content-Type"))
				require.Equal(t, tc.body, upstream.lastBody)
			} else {
				require.Equal(t, "application/json", upstream.lastReq.Header.Get("Content-Type"))
				require.Equal(t, sdp, gjson.GetBytes(upstream.lastBody, "sdp").String())
				require.Equal(t, "gpt-realtime", gjson.GetBytes(upstream.lastBody, "session.model").String())
			}
		})
	}
}

func TestOpenAIGatewayService_ForwardRealtimeREST_ClientSecretMapsTranscriptionModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"session":{"type":"transcription","audio":{"input":{"transcription":{"model":"client-transcribe"}}}}}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/realtime/client_secrets", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid_secret"}},
		Body:       io.NopCloser(bytes.NewReader([]byte(`{"value":"secret"}`))),
	}}
	svc := &OpenAIGatewayService{
		cfg:          &config.Config{},
		httpUpstream: upstream,
	}
	account := &Account{
		ID:          123,
		Name:        "acc",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key": "sk-test",
			"model_mapping": map[string]any{
				"gpt-4o-mini-transcribe": "gpt-4o-transcribe",
			},
		},
		Status:      StatusActive,
		Schedulable: true,
	}
	parsed, err := ParseOpenAIRealtimeRESTRequest(c, body)
	require.NoError(t, err)

	result, err := svc.ForwardRealtimeREST(
		context.Background(),
		c,
		account,
		parsed,
		map[string]string{"session.audio.input.transcription.model": "gpt-4o-mini-transcribe"},
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "https://api.openai.com/v1/realtime/client_secrets", upstream.lastReq.URL.String())
	require.Equal(t, "gpt-4o-transcribe", gjson.GetBytes(upstream.lastBody, "session.audio.input.transcription.model").String())
	require.Equal(t, "client-transcribe", result.Model)
	require.Equal(t, "gpt-4o-transcribe", result.UpstreamModel)
}

func TestOpenAIGatewayService_ForwardRealtimeREST_TranslationMapsSessionAndTranscriptionModels(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"session":{"model":"client-translate","audio":{"input":{"transcription":{"model":"client-transcribe"}}}}}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/realtime/translations/client_secrets", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid_translation"}},
		Body:       io.NopCloser(bytes.NewReader([]byte(`{"value":"secret"}`))),
	}}
	svc := &OpenAIGatewayService{
		cfg:          &config.Config{},
		httpUpstream: upstream,
	}
	account := &Account{
		ID:          123,
		Name:        "acc",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key": "sk-test",
			"model_mapping": map[string]any{
				"mapped-translate":  "gpt-realtime-translate",
				"mapped-transcribe": "gpt-4o-transcribe",
			},
		},
		Status:      StatusActive,
		Schedulable: true,
	}
	parsed, err := ParseOpenAIRealtimeRESTRequest(c, body)
	require.NoError(t, err)

	result, err := svc.ForwardRealtimeREST(
		context.Background(),
		c,
		account,
		parsed,
		map[string]string{
			"session.model": "mapped-translate",
			"session.audio.input.transcription.model": "mapped-transcribe",
		},
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "https://api.openai.com/v1/realtime/translations/client_secrets", upstream.lastReq.URL.String())
	require.Equal(t, "gpt-realtime-translate", gjson.GetBytes(upstream.lastBody, "session.model").String())
	require.Equal(t, "gpt-4o-transcribe", gjson.GetBytes(upstream.lastBody, "session.audio.input.transcription.model").String())
	require.Equal(t, "client-translate", result.Model)
	require.Equal(t, "gpt-realtime-translate", result.UpstreamModel)
}

func TestOpenAIGatewayService_ForwardRealtimeREST_TranslationCallsMapsModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"client-translate-call","sdp":"v=0"}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/realtime/translations/calls", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/sdp")

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid_translation_call"}},
		Body:       io.NopCloser(bytes.NewReader([]byte(`{"id":"call_123"}`))),
	}}
	svc := &OpenAIGatewayService{
		cfg:          &config.Config{},
		httpUpstream: upstream,
	}
	account := &Account{
		ID:          123,
		Name:        "acc",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token":       "oauth-token",
			"chatgpt_account_id": "chatgpt-acc",
			"model_mapping": map[string]any{
				"mapped-translate-call": "gpt-realtime-translate",
			},
		},
		Status:      StatusActive,
		Schedulable: true,
	}
	parsed, err := ParseOpenAIRealtimeRESTRequest(c, body)
	require.NoError(t, err)
	require.Equal(t, "/v1/realtime/translations/calls", parsed.Endpoint)
	require.Equal(t, "client-translate-call", parsed.Model)
	require.Equal(t, "model", parsed.ScheduleModelPath)

	result, err := svc.ForwardRealtimeREST(
		context.Background(),
		c,
		account,
		parsed,
		map[string]string{"model": "mapped-translate-call"},
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "https://api.openai.com/v1/realtime/translations/calls", upstream.lastReq.URL.String())
	require.Equal(t, "application/sdp", upstream.lastReq.Header.Get("Content-Type"))
	require.Equal(t, "Bearer oauth-token", upstream.lastReq.Header.Get("Authorization"))
	require.Equal(t, "chatgpt-acc", upstream.lastReq.Header.Get("chatgpt-account-id"))
	require.Equal(t, "gpt-realtime-translate", gjson.GetBytes(upstream.lastBody, "model").String())
	require.Equal(t, "client-translate-call", result.Model)
	require.Equal(t, "gpt-realtime-translate", result.UpstreamModel)
}

func TestOpenAIGatewayService_ForwardRealtimeREST_TranslationOAuthHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"session":{"model":"client-translate"}}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/realtime/translations/client_secrets", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("originator", "codex_cli_rs")

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid_translation_oauth"}},
		Body:       io.NopCloser(bytes.NewReader([]byte(`{"value":"secret"}`))),
	}}
	svc := &OpenAIGatewayService{
		cfg:          &config.Config{},
		httpUpstream: upstream,
	}
	account := &Account{
		ID:          123,
		Name:        "acc",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token":       "oauth-token",
			"chatgpt_account_id": "chatgpt-acc",
			"model_mapping": map[string]any{
				"client-translate": "gpt-realtime-translate",
			},
		},
		Status:      StatusActive,
		Schedulable: true,
	}
	parsed, err := ParseOpenAIRealtimeRESTRequest(c, body)
	require.NoError(t, err)

	result, err := svc.ForwardRealtimeREST(context.Background(), c, account, parsed, nil)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "https://api.openai.com/v1/realtime/translations/client_secrets", upstream.lastReq.URL.String())
	require.Equal(t, "Bearer oauth-token", upstream.lastReq.Header.Get("Authorization"))
	require.Equal(t, "chatgpt-acc", upstream.lastReq.Header.Get("chatgpt-account-id"))
	require.Equal(t, "codex_cli_rs", upstream.lastReq.Header.Get("originator"))
	require.Equal(t, "gpt-realtime-translate", gjson.GetBytes(upstream.lastBody, "session.model").String())
	require.Equal(t, "client-translate", result.Model)
	require.Equal(t, "gpt-realtime-translate", result.UpstreamModel)
}

func TestOpenAIGatewayService_ForwardRealtimeREST_CallControlAllowsNoModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"target_uri":"tel:+15551234567"}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/realtime/calls/call_123/refer", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid_refer"}},
		Body:       io.NopCloser(bytes.NewReader([]byte(`{"id":"call_123"}`))),
	}}
	svc := &OpenAIGatewayService{
		cfg:          &config.Config{},
		httpUpstream: upstream,
	}
	account := &Account{
		ID:          123,
		Name:        "acc",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key": "sk-test",
		},
		Status:      StatusActive,
		Schedulable: true,
	}
	parsed, err := ParseOpenAIRealtimeRESTRequest(c, body)
	require.NoError(t, err)

	result, err := svc.ForwardRealtimeREST(context.Background(), c, account, parsed, nil)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "https://api.openai.com/v1/realtime/calls/call_123/refer", upstream.lastReq.URL.String())
	require.JSONEq(t, string(body), string(upstream.lastBody))
	require.Empty(t, result.Model)
	require.Empty(t, result.UpstreamModel)
}

func TestBuildOpenAIRealtimeRESTURL_AcceptAction(t *testing.T) {
	require.Equal(t, "https://example.test/v1/realtime/calls/call_123/accept", buildOpenAIRealtimeRESTURL("https://example.test/v1", "/v1/realtime/calls/call_123/accept"))
}

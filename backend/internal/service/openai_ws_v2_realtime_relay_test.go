package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type realtimeRelayHandshake struct {
	url     string
	headers http.Header
}

type realtimeRelayCaptureDialer struct {
	conn       *stagedPassthroughConn
	handshakes chan realtimeRelayHandshake
}

func (d *realtimeRelayCaptureDialer) Dial(_ context.Context, target string, headers http.Header, _ string) (openAIWSClientConn, int, http.Header, error) {
	d.handshakes <- realtimeRelayHandshake{url: target, headers: headers.Clone()}
	return d.conn, http.StatusSwitchingProtocols, http.Header{}, nil
}

func TestRealtimeRelayPreservesFramesAndSessionModelAliases(t *testing.T) {
	type relayCase struct {
		name       string
		firstFrame string
		oauth      bool
	}
	for _, tc := range []relayCase{
		{name: "query_model"},
		{name: "session_model", firstFrame: `{"type":"session.update","session":{"model":"voice-alias","instructions":"Listen"}}`},
		{name: "oauth_call_sideband", oauth: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			upstream := newStagedPassthroughConn()
			cfg := passthroughLifecycleConfig()
			cfg.Gateway.OpenAIWS.Enabled = false
			cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = false
			account := passthroughLifecycleAccount()
			account.Credentials["model_mapping"] = map[string]any{
				"voice-alias": "gpt-realtime",
				"next-alias":  "gpt-realtime-next",
			}
			token := "sk-test"
			path := "/v1/realtime"
			if tc.oauth {
				account.Type = AccountTypeOAuth
				account.Credentials["chatgpt_account_id"] = "account-test"
				token = "oauth-test"
				path += "?call_id=rtc_test&intent=quicksilver"
			}
			svc := newPassthroughLifecycleService(cfg, upstream)
			dialer := &realtimeRelayCaptureDialer{conn: upstream, handshakes: make(chan realtimeRelayHandshake, 1)}
			svc.openaiWSPassthroughDialer = dialer
			requestedModels := make(chan string, 8)
			turnResults := make(chan *OpenAIForwardResult, 8)
			hooks := &OpenAIWSIngressHooks{
				InitialRequestModel: "voice-alias",
				MapRequestModel: func(_ int, model string) (string, error) {
					return model, nil
				},
				BeforeRequest: func(_ int, _ []byte, model string) error {
					requestedModels <- model
					return nil
				},
				AfterTurn: func(_ int, result *OpenAIForwardResult, err error) {
					if err == nil && result != nil {
						turnResults <- result
					}
				},
			}
			serverErr := make(chan error, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := coderws.Accept(w, r, nil)
				if err != nil {
					serverErr <- err
					return
				}
				defer func() { _ = conn.CloseNow() }()
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = r.WithContext(ctx)
				serverErr <- svc.ProxyRealtimeWebSocketFromClient(ctx, c, conn, account, token, []byte(tc.firstFrame), "voice-alias", "/v1/realtime", hooks)
			}))
			defer server.Close()
			client, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+path, &coderws.DialOptions{HTTPHeader: http.Header{
				"Openai-Alpha":      []string{"realtime=v2"},
				"X-Oai-Attestation": []string{"attestation-test"},
			}})
			require.NoError(t, err)
			defer func() { _ = client.CloseNow() }()
			select {
			case handshake := <-dialer.handshakes:
				require.Equal(t, "Bearer "+token, handshake.headers.Get("Authorization"))
				require.Equal(t, "realtime=v2", handshake.headers.Get("OpenAI-Alpha"))
				require.Equal(t, "attestation-test", handshake.headers.Get("x-oai-attestation"))
				if tc.oauth {
					require.Equal(t, "wss://api.openai.com/v1/realtime?call_id=rtc_test&intent=quicksilver", handshake.url)
					require.Equal(t, "account-test", handshake.headers.Get("ChatGPT-Account-ID"))
				} else {
					require.Equal(t, "wss://api.openai.com/v1/realtime?model=gpt-realtime", handshake.url)
				}
			case <-time.After(time.Second):
				t.Fatal("realtime upstream handshake was not attempted")
			}
			if tc.firstFrame != "" {
				first := requirePassthroughUpstreamWrite(t, upstream, time.Second)
				require.JSONEq(t, `{"type":"session.update","session":{"model":"gpt-realtime","instructions":"Listen"}}`, string(first))
			}
			upstream.Send(`{"type":"session.created","session":{"model":"gpt-realtime"}}`)
			_, err = readPassthroughLifecycleFrame(t, client, time.Second)
			require.NoError(t, err)

			frames := []string{
				`{"type":"response.create"}`,
				`{"type":"response.create","response":{"conversation":"none","instructions":"Summarize","output_modalities":["text"]}}`,
				`{"type":"response.cancel"}`,
				`{"type":"session.update","session":{"model":"next-alias","instructions":"Continue"}}`,
				`{"type":"response.create"}`,
			}
			for _, frame := range frames {
				writeCtx, cancelWrite := context.WithTimeout(ctx, time.Second)
				err = client.Write(writeCtx, coderws.MessageText, []byte(frame))
				cancelWrite()
				require.NoError(t, err)
				forwarded := requirePassthroughUpstreamWrite(t, upstream, time.Second)
				if gjson.Get(frame, "type").String() == "session.update" {
					require.JSONEq(t, `{"type":"session.update","session":{"model":"gpt-realtime-next","instructions":"Continue"}}`, string(forwarded))
				} else {
					require.Equal(t, frame, string(forwarded))
				}
			}
			for _, expected := range []string{"voice-alias", "voice-alias", "next-alias", "next-alias"} {
				select {
				case model := <-requestedModels:
					require.Equal(t, expected, model)
				case <-time.After(time.Second):
					t.Fatal("request hook did not observe the client model alias")
				}
			}
			for _, responseID := range []string{"resp_explicit", "resp_server_vad"} {
				upstream.Send(`{"type":"response.created","response":{"id":"` + responseID + `","model":"gpt-realtime-next"}}`)
				_, err = readPassthroughLifecycleFrame(t, client, time.Second)
				require.NoError(t, err)
				upstream.Send(`{"type":"response.done","response":{"id":"` + responseID + `","model":"gpt-realtime-next","usage":{"input_tokens":3,"output_tokens":2}}}`)
				terminal, readErr := readPassthroughLifecycleFrame(t, client, time.Second)
				require.NoError(t, readErr)
				require.Equal(t, "next-alias", gjson.GetBytes(terminal, "response.model").String())
				select {
				case result := <-turnResults:
					require.Equal(t, "next-alias", result.Model)
					require.Equal(t, "gpt-realtime-next", result.UpstreamModel)
					require.Equal(t, 3, result.Usage.InputTokens)
					require.Equal(t, 2, result.Usage.OutputTokens)
				case <-time.After(time.Second):
					t.Fatal("realtime response usage was not reported")
				}
			}
			cancel()
			_, err = readPassthroughLifecycleFrame(t, client, 3*time.Second)
			var closeErr coderws.CloseError
			require.ErrorAs(t, err, &closeErr)
			require.Equal(t, coderws.StatusGoingAway, closeErr.Code)
			require.Equal(t, "websocket request canceled", closeErr.Reason)
			select {
			case <-serverErr:
			case <-time.After(3 * time.Second):
				t.Fatal("realtime relay did not stop after cancellation")
			}
		})
	}
}

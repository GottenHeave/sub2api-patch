package service

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type codexAPIResourceCache struct {
	GatewayCache
	values map[string]int64
	err    error
}

func (c *codexAPIResourceCache) GetSessionAccountID(_ context.Context, _ int64, key string) (int64, error) {
	if c.err != nil {
		return 0, c.err
	}
	if id := c.values[key]; id > 0 {
		return id, nil
	}
	return 0, ErrStickySessionNotFound
}

func (c *codexAPIResourceCache) SetSessionAccountID(_ context.Context, _ int64, key string, id int64, _ time.Duration) error {
	c.values[key] = id
	return c.err
}

func TestCodexAPIResourceOwnership(t *testing.T) {
	cache := &codexAPIResourceCache{values: make(map[string]int64)}
	svc := &OpenAIGatewayService{cache: cache}
	group := int64(9)
	key := &APIKey{ID: 1, GroupID: &group}
	require.NoError(t, svc.BindCodexAPIResource(t.Context(), key, "file_document", 20))
	require.NoError(t, svc.BindCodexAPIResource(t.Context(), key, "call_realtime", 20))
	body := []byte(`{"input":[{"content":[{"type":"input_file","file_id":"file_document"}]}]}`)
	id, err := svc.CodexAPIResourceAccount(t.Context(), key, body, "call_realtime")
	require.NoError(t, err)
	require.Equal(t, int64(20), id)
	_, err = svc.CodexAPIResourceAccount(t.Context(), &APIKey{ID: 2, GroupID: &group}, body, "")
	require.Error(t, err)
	require.NoError(t, svc.BindCodexAPIResource(t.Context(), key, "response_other", 21))
	_, err = svc.CodexAPIResourceAccount(t.Context(), key, []byte(`{"previous_response_id":"response_other","file_id":"file_document"}`), "")
	require.Error(t, err)
	_, err = svc.CodexAPIResourceAccount(t.Context(), key, nil, "unowned_call")
	require.Error(t, err)
	cache.err = context.DeadlineExceeded
	_, err = svc.CodexAPIResourceAccount(t.Context(), key, body, "")
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestCodexAPIImageMultipartInspectionKeepsWireBytes(t *testing.T) {
	var wire bytes.Buffer
	writer := multipart.NewWriter(&wire)
	require.NoError(t, writer.WriteField("model", "future-image-model"))
	require.NoError(t, writer.WriteField("prompt", "a red square"))
	part, err := writer.CreatePart(textproto.MIMEHeader{"Content-Disposition": {`form-data; name="image"; filename="source.png"`}, "Content-Type": {"image/png"}})
	require.NoError(t, err)
	_, err = part.Write([]byte("image-bytes"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	original := bytes.Clone(wire.Bytes())
	inspection, err := InspectCodexAPIRequest("/v1/images/edits", writer.FormDataContentType(), wire.Bytes())
	require.NoError(t, err)
	require.Equal(t, "future-image-model", inspection.Model)
	require.Equal(t, ContentModerationProtocolOpenAIImages, inspection.Protocol)
	require.Equal(t, "a red square", gjson.GetBytes(inspection.Body, "prompt").String())
	require.Contains(t, string(inspection.Body), "aW1hZ2UtYnl0ZXM=")
	require.Equal(t, original, wire.Bytes())
	upstream := &codexAPITransportCapture{}
	svc := &OpenAIGatewayService{httpUpstream: upstream}
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeCodexAPI, Credentials: map[string]any{"base_url": "https://gateway.example", "api_key": "gateway"}}
	request := httptest.NewRequest("POST", "/v1/images/edits?business=original%20query", bytes.NewReader(wire.Bytes()))
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response, err := svc.RoundTripCodexAPI(t.Context(), account, request, "/v1/images/edits")
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	body, err := io.ReadAll(upstream.request.Body)
	require.NoError(t, err)
	require.Equal(t, original, body)
	require.Equal(t, writer.FormDataContentType(), upstream.request.Header.Get("Content-Type"))
	require.Equal(t, "business=original%20query", upstream.request.URL.RawQuery)
}

func TestCodexAPIWebSocketHandshakeAndRedirects(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusTemporaryRedirect} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			body := strings.Repeat("provider-owned-error ", 400)
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				require.Equal(t, "Bearer gateway", r.Header.Get("Authorization"))
				require.Empty(t, r.Header.Get("X-Codex-Gateway-Authorization"))
				w.Header().Set("Location", "/redirected")
				w.WriteHeader(status)
				_, _ = io.WriteString(w, body)
			}))
			defer server.Close()
			cfg := &config.Config{}
			cfg.Security.URLAllowlist.AllowInsecureHTTP = true
			svc := &OpenAIGatewayService{cfg: cfg}
			account := &Account{Platform: PlatformOpenAI, Type: AccountTypeCodexAPI, Credentials: map[string]any{"base_url": server.URL, "api_key": "gateway"}}
			request := httptest.NewRequest("GET", "/v1/responses", nil)
			request.Header.Set("Upgrade", "websocket")
			request.Header.Set("Connection", "Upgrade")
			request.Header.Set("Authorization", "Bearer downstream")
			request.Header.Set("X-Codex-Gateway-Authorization", "Bearer unrelated-key")
			conn, response, err := svc.DialCodexAPI(t.Context(), account, request, "/v1/responses")
			require.Error(t, err)
			require.Nil(t, conn)
			require.Equal(t, status, response.StatusCode)
			got, err := io.ReadAll(response.Body)
			require.NoError(t, err)
			require.NoError(t, response.Body.Close())
			require.Equal(t, body, string(got))
			require.Equal(t, 1, calls)
		})
	}
}

func TestCodexAPIWebSocketPreservesFramesAndQuery(t *testing.T) {
	seen := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.URL.RawQuery + "|" + r.Header.Get("Authorization") + "|" + r.Header.Get("Sec-WebSocket-Protocol")
		conn, err := coderws.Accept(w, r, &coderws.AcceptOptions{Subprotocols: []string{"responses"}})
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()
		for range 2 {
			kind, body, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			if err := conn.Write(r.Context(), kind, body); err != nil {
				return
			}
		}
		_ = conn.Close(coderws.StatusNormalClosure, "finished")
	}))
	defer server.Close()
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	svc := &OpenAIGatewayService{cfg: cfg}
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeCodexAPI, Credentials: map[string]any{"base_url": server.URL, "api_key": "gateway"}}
	request := httptest.NewRequest("GET", "/v1/responses?business=a%20b&api_key=downstream", nil)
	request.Header.Set("Upgrade", "websocket")
	request.Header.Set("Connection", "Upgrade")
	request.Header.Set("Sec-WebSocket-Protocol", "responses, openai-insecure-api-key.downstream")
	conn, _, err := svc.DialCodexAPI(t.Context(), account, request, "/v1/responses")
	require.NoError(t, err)
	defer func() { _ = conn.CloseNow() }()
	require.Equal(t, "business=a%20b|Bearer gateway|responses", <-seen)
	for _, kind := range []coderws.MessageType{coderws.MessageText, coderws.MessageBinary} {
		payload := []byte("original-payload")
		require.NoError(t, conn.Write(t.Context(), kind, payload))
		gotKind, got, err := conn.Read(t.Context())
		require.NoError(t, err)
		require.Equal(t, kind, gotKind)
		require.Equal(t, payload, got)
	}
}

package service

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type codexAPITransportCapture struct {
	HTTPUpstream
	request *http.Request
}

func (c *codexAPITransportCapture) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	c.request = req
	return &http.Response{StatusCode: http.StatusAccepted, Body: io.NopCloser(strings.NewReader("upstream"))}, nil
}

func TestCodexAPITransportPreservesCallerPayloadAndReplacesCredentials(t *testing.T) {
	for _, body := range [][]byte{[]byte(" {\"model\":\"unknown-model\", \"tools\":[{\"name\":\"namespace__pi\"}]}\n"), {0x28, 0xb5, 0x2f, 0xfd, 0, 1}} {
		t.Run(http.DetectContentType(body), func(t *testing.T) {
			upstream := &codexAPITransportCapture{}
			svc := &OpenAIGatewayService{httpUpstream: upstream}
			account := &Account{Platform: PlatformOpenAI, Type: AccountTypeCodexAPI, Credentials: map[string]any{"base_url": "https://gateway.example/prefix/v1", "api_key": "gateway-key"}}
			incoming := httptest.NewRequest(http.MethodPost, "/v1/responses?business=a%20b&key=local&business=c%2Fd&api_key=local", bytes.NewReader(body))
			incoming.Header.Set("Authorization", "Bearer local-key")
			incoming.Header.Set("X-Codex-Gateway-Authorization", "Bearer stale-gateway-key")
			incoming.Header.Set("X-Api-Key", "local-key")
			incoming.Header.Set("X-Goog-Api-Key", "local-key")
			incoming.Header.Set("Cookie", "session=local")
			incoming.Header.Set("Proxy-Authorization", "local-proxy")
			incoming.Header.Set("Connection", "X-Connection-Secret")
			incoming.Header.Set("X-Connection-Secret", "local")
			incoming.Header.Set("Content-Encoding", "zstd")
			incoming.Header["X-Business"] = []string{"one", "two"}
			resp, err := svc.RoundTripCodexAPI(t.Context(), account, incoming, "/backend-api/codex/responses")
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())
			out := upstream.request
			got, err := io.ReadAll(out.Body)
			require.NoError(t, err)
			require.Equal(t, body, got)
			require.Equal(t, incoming.Method, out.Method)
			require.Equal(t, incoming.ContentLength, out.ContentLength)
			require.Equal(t, "/prefix/backend-api/codex/responses", out.URL.Path)
			require.Equal(t, "business=a%20b&business=c%2Fd", out.URL.RawQuery)
			require.Equal(t, "Bearer gateway-key", out.Header.Get("Authorization"))
			for _, name := range []string{"X-Codex-Gateway-Authorization", "X-Api-Key", "X-Goog-Api-Key", "Cookie", "Proxy-Authorization", "Connection", "X-Connection-Secret"} {
				require.Empty(t, out.Header.Get(name), name)
			}
			require.ElementsMatch(t, incoming.Header.Values("X-Business"), out.Header.Values("X-Business"))
			require.Equal(t, incoming.Header.Get("Content-Encoding"), out.Header.Get("Content-Encoding"))
			require.Equal(t, "Bearer local-key", incoming.Header.Get("Authorization"))
			require.True(t, HTTPUpstreamRedirectsDisabled(out.Context()))
			require.Equal(t, HTTPUpstreamProfileRaw, HTTPUpstreamProfileFromContext(out.Context()))
		})
	}
}

func TestCodexAPITransportRejectsUnsupportedEndpoint(t *testing.T) {
	svc := &OpenAIGatewayService{}
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeCodexAPI}
	_, err := svc.RoundTripCodexAPI(t.Context(), account, httptest.NewRequest(http.MethodGet, "/", nil), "//other.example/secrets")
	require.Error(t, err)
}

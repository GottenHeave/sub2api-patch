package routes

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type codexAPIRoutesRepository struct {
	service.AccountRepository
	members []service.Account
}

func (r codexAPIRoutesRepository) ListAllWithFilters(context.Context, string, string, string, string, int64, string) ([]service.Account, error) {
	return r.members, nil
}

func (r *pinnedModelsRoutesRepository) ListAllWithFilters(context.Context, string, string, string, string, int64, string) ([]service.Account, error) {
	return []service.Account{r.account}, nil
}

func newOrdinaryRoutesOpenAIHandler(cfg *config.Config) *handler.OpenAIGatewayHandler {
	s := service.NewOpenAIGatewayService(codexAPIRoutesRepository{}, nil, nil, nil, nil, nil, nil, cfg, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	return handler.NewOpenAIGatewayHandler(s, nil, nil, nil, nil, nil, nil, nil, cfg)
}

type codexAPIRoutesUpstream struct {
	service.HTTPUpstream
	do func(*http.Request) (*http.Response, error)
}

func (u codexAPIRoutesUpstream) Do(r *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return u.do(r)
}

func TestCodexAPIGatewayRoutesDispatchBeforeTransformations(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{RunMode: config.RunModeSimple, Gateway: config.GatewayConfig{MaxBodySize: 1 << 20, TextMaxBodySize: 1 << 20}}
	account := service.Account{ID: 1, Platform: service.PlatformOpenAI, Type: service.AccountTypeCodexAPI, Status: service.StatusActive, Schedulable: true, Credentials: map[string]any{"api_key": "upstream", "base_url": "https://codex.example"}}
	repo := codexAPIRoutesRepository{members: []service.Account{account}}
	wire := new(bytes.Buffer)
	encoder := gzip.NewWriter(wire)
	_, err := encoder.Write([]byte(`{ "model": "upstream-only-model", "future_field": true }`))
	require.NoError(t, err)
	require.NoError(t, encoder.Close())
	var expectedPath string
	calls := 0
	upstream := codexAPIRoutesUpstream{do: func(r *http.Request) (*http.Response, error) {
		calls++
		require.Equal(t, expectedPath, r.URL.Path)
		require.Equal(t, "Bearer upstream", r.Header.Get("Authorization"))
		if r.Method == http.MethodPost {
			body, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			require.Equal(t, wire.Bytes(), body)
			require.Equal(t, "gzip", r.Header.Get("Content-Encoding"))
		}
		return &http.Response{StatusCode: http.StatusUnprocessableEntity, Header: http.Header{"Etag": {"original"}}, Body: io.NopCloser(strings.NewReader("upstream owns this decision"))}, nil
	}}
	s := service.NewOpenAIGatewayService(repo, nil, nil, nil, nil, nil, nil, cfg, nil, nil, nil, nil, nil, upstream, nil, nil, nil, nil, nil, nil, nil, nil)
	billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billing.Stop)
	h := &handler.Handlers{Gateway: &handler.GatewayHandler{}, OpenAIGateway: handler.NewOpenAIGatewayHandler(s, service.NewConcurrencyService(nil), billing, nil, nil, nil, nil, nil, cfg), AsyncImage: handler.NewAsyncImageHandler(nil, nil)}
	group := &service.Group{ID: 1, Platform: service.PlatformOpenAI, ModelAllowlist: service.GroupModelAllowlist{Enabled: true, Models: []string{"different-local-model"}}}
	router := gin.New()
	RegisterGatewayRoutes(router, h, servermiddleware.APIKeyAuthMiddleware(func(c *gin.Context) {
		if c.GetHeader("Authorization") != "Bearer local" {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		c.Set(string(servermiddleware.ContextKeyAPIKey), &service.APIKey{GroupID: &group.ID, Group: group})
		c.Set(string(servermiddleware.ContextKeyUser), servermiddleware.AuthSubject{})
		c.Next()
	}), nil, nil, nil, nil, nil, cfg)
	for _, test := range []struct{ method, path, target string }{
		{"POST", "/v1/responses", "/v1/responses"},
		{"POST", "/responses", "/v1/responses"},
		{"POST", "/backend-api/codex/responses", "/backend-api/codex/responses"},
		{"POST", "/responses/compact", "/v1/responses/compact"},
		{"GET", "/v1/models", "/v1/models"},
		{"GET", "/models?client_version=any", "/v1/models"},
		{"GET", "/v1/models?client_version=any", "/v1/models"},
		{"GET", "/backend-api/codex/models", "/backend-api/codex/models"},
	} {
		expectedPath = test.target
		r := httptest.NewRequest(test.method, test.path, bytes.NewReader(wire.Bytes()))
		r.Header.Set("Authorization", "Bearer local")
		r.Header.Set("Content-Encoding", "gzip")
		r.Header.Set("originator", "codex_cli_rs")
		r.Header.Set("User-Agent", "codex_cli_rs/1.0")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code, test.path+": "+w.Body.String())
		require.Equal(t, "upstream owns this decision", w.Body.String())
	}
	before := calls
	for _, path := range []string{"/v1/responses", "/responses", "/backend-api/codex/responses", "/antigravity/models", "/antigravity/v1/models"} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.Header.Set("Authorization", "Bearer local")
		r.Header.Set("Connection", "Upgrade")
		r.Header.Set("Upgrade", "websocket")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		require.Equal(t, http.StatusNotFound, w.Code, path)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/models", nil))
	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.Equal(t, before, calls)
}

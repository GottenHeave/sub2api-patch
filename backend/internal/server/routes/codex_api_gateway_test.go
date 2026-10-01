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

type codexAPIRoutesResponse struct {
	name        string
	status      int
	contentType string
	body        string
}

type codexAPIRoutesRequest struct {
	method string
	path   string
	target string
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
	_, err := encoder.Write([]byte(`{ "model": "gpt-6.1-sol", "future_field": true }`))
	require.NoError(t, err)
	require.NoError(t, encoder.Close())
	var expectedPath string
	var response codexAPIRoutesResponse
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
		return &http.Response{StatusCode: response.status, Header: http.Header{"Etag": {"original"}, "Content-Type": {response.contentType}}, Body: io.NopCloser(strings.NewReader(response.body))}, nil
	}}
	// Keep shared error handling available: unintended state writes must reach
	// the repository stub rather than being skipped for a nil rate-limit service.
	rateLimits := service.NewRateLimitService(repo, nil, cfg, nil, nil)
	s := service.NewOpenAIGatewayService(repo, nil, nil, nil, nil, nil, nil, cfg, nil, nil, nil, rateLimits, nil, upstream, nil, nil, nil, nil, nil, nil, nil, nil)
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
	routes := []codexAPIRoutesRequest{
		{"POST", "/v1/responses", "/v1/responses"},
		{"POST", "/responses", "/v1/responses"},
		{"POST", "/backend-api/codex/responses", "/backend-api/codex/responses"},
		{"POST", "/responses/compact", "/v1/responses/compact"},
		{"POST", "/v1/responses/compact", "/v1/responses/compact"},
		{"POST", "/backend-api/codex/responses/compact", "/v1/responses/compact"},
		{"GET", "/v1/models", "/v1/models"},
		{"GET", "/models?client_version=any", "/v1/models"},
		{"GET", "/v1/models?client_version=any", "/v1/models"},
		{"GET", "/backend-api/codex/models", "/backend-api/codex/models"},
	}
	for _, result := range []codexAPIRoutesResponse{
		{"plan gated", http.StatusBadRequest, "application/json", `{"detail":"The 'gpt-6.1-sol' model is not supported when using Codex with a ChatGPT account."}`},
		{"nested plan gated", http.StatusBadRequest, "application/json", `{"error":{"message":"The 'gpt-6.1-sol' model is not supported when using Codex with a ChatGPT account."}}`},
		{"invalid parameter", http.StatusBadRequest, "application/json", `{"error":{"type":"invalid_request_error","message":"Unsupported parameter: future_field"}}`},
		{"unknown model", http.StatusNotFound, "application/json", `{"error":{"code":"model_not_found","message":"Model not found"}}`},
		{"rate limited", http.StatusTooManyRequests, "application/json", `{"error":{"type":"rate_limit_error","message":"Try again later"}}`},
		{"unavailable", http.StatusServiceUnavailable, "application/json", `{"error":{"type":"server_error","message":"Temporarily unavailable"}}`},
		{"validation", http.StatusUnprocessableEntity, "text/plain", "upstream owns this decision"},
		{"stream failure", http.StatusOK, "text/event-stream", ": original comment\n\nevent: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"error\":{\"message\":\"The 'gpt-6.1-sol' model is not supported when using Codex with a ChatGPT account.\"}}}\n\n"},
		{"success after errors", http.StatusOK, "application/json", `{"id":"upstream-response","status":"completed","output":[]}`},
	} {
		t.Run(result.name, func(t *testing.T) {
			response = result
			for _, route := range routes {
				t.Run(route.method+route.path, func(t *testing.T) {
					expectedPath = route.target
					for range 2 {
						before := calls
						r := httptest.NewRequest(route.method, route.path, bytes.NewReader(wire.Bytes()))
						r.Header.Set("Authorization", "Bearer local")
						r.Header.Set("Content-Encoding", "gzip")
						r.Header.Set("originator", "codex_cli_rs")
						r.Header.Set("User-Agent", "codex_cli_rs/1.0")
						w := httptest.NewRecorder()
						router.ServeHTTP(w, r)
						require.Equal(t, result.status, w.Code, w.Body.String())
						require.Equal(t, result.body, w.Body.String())
						require.Equal(t, result.contentType, w.Header().Get("Content-Type"))
						require.Equal(t, "original", w.Header().Get("ETag"))
						require.Equal(t, before+1, calls, "each request must reach upstream exactly once, including after an error")
					}
				})
			}
		})
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

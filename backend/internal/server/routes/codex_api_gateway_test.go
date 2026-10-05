package routes

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	coderws "github.com/coder/websocket"
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

type codexAPIRouteCache struct {
	service.GatewayCache
	mu        sync.Mutex
	resources map[string]int64
}

type codexAPIRouteConcurrency struct {
	service.ConcurrencyCache
	mu     sync.Mutex
	active int
}

func (s *codexAPIRouteConcurrency) AcquireAccountSlot(_ context.Context, _ int64, limit int, _ string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active >= limit {
		return false, nil
	}
	s.active++
	return true, nil
}

func (s *codexAPIRouteConcurrency) ReleaseAccountSlot(_ context.Context, _ int64, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active--
	return nil
}

func (s *codexAPIRouteCache) GetSessionAccountID(_ context.Context, _ int64, key string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id := s.resources[key]; id != 0 {
		return id, nil
	}
	return 0, service.ErrStickySessionNotFound
}

func (s *codexAPIRouteCache) SetSessionAccountID(_ context.Context, _ int64, key string, id int64, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resources[key] = id
	return nil
}

func newCodexAPIRoutesFixture(t *testing.T, accounts []service.Account, upstream service.HTTPUpstream, cache service.GatewayCache) (*gin.Engine, *service.Group) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{RunMode: config.RunModeSimple, Gateway: config.GatewayConfig{MaxBodySize: 1 << 20, TextMaxBodySize: 1 << 20}}
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	repo := codexAPIRoutesRepository{members: accounts}
	rateLimits := service.NewRateLimitService(repo, nil, cfg, nil, nil)
	s := service.NewOpenAIGatewayService(repo, nil, nil, nil, nil, nil, cache, cfg, nil, nil, nil, rateLimits, nil, upstream, nil, nil, nil, nil, nil, nil, nil, nil)
	billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billing.Stop)
	slots := &codexAPIRouteConcurrency{}
	t.Cleanup(func() {
		require.Eventually(t, func() bool { slots.mu.Lock(); defer slots.mu.Unlock(); return slots.active == 0 }, 5*time.Second, 10*time.Millisecond)
	})
	h := &handler.Handlers{Gateway: &handler.GatewayHandler{}, OpenAIGateway: handler.NewOpenAIGatewayHandler(s, service.NewConcurrencyService(slots), billing, nil, nil, nil, nil, nil, cfg), AsyncImage: handler.NewAsyncImageHandler(nil, nil)}
	group := &service.Group{ID: 1, Platform: service.PlatformOpenAI, AllowImageGeneration: true, ModelAllowlist: service.GroupModelAllowlist{Enabled: true, Models: []string{"different-local-model"}}}
	router := gin.New()
	RegisterGatewayRoutes(router, h, servermiddleware.APIKeyAuthMiddleware(func(c *gin.Context) {
		if c.GetHeader("Authorization") != "Bearer local" {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		c.Set(string(servermiddleware.ContextKeyAPIKey), &service.APIKey{ID: 1, GroupID: &group.ID, Group: group})
		c.Set(string(servermiddleware.ContextKeyUser), servermiddleware.AuthSubject{})
		c.Next()
	}), nil, nil, nil, nil, nil, cfg)
	return router, group
}

func TestCodexAPIGatewayRoutesDispatchBeforeTransformations(t *testing.T) {
	gin.SetMode(gin.TestMode)
	account := service.Account{ID: 1, Platform: service.PlatformOpenAI, Type: service.AccountTypeCodexAPI, Status: service.StatusActive, Schedulable: true, Credentials: map[string]any{"api_key": "upstream", "base_url": "https://codex.example"}}
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
	router, _ := newCodexAPIRoutesFixture(t, []service.Account{account}, upstream, nil)
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
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		require.Equal(t, http.StatusNotFound, w.Code, path)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/models", nil))
	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.Equal(t, before, calls)
}

func TestCodexAPISubscriptionRouteInventory(t *testing.T) {
	account := service.Account{ID: 1, Platform: service.PlatformOpenAI, Type: service.AccountTypeCodexAPI, Status: service.StatusActive, Schedulable: true, Credentials: map[string]any{"api_key": "upstream", "base_url": "https://codex.example"}}
	upstream := codexAPIRoutesUpstream{do: func(r *http.Request) (*http.Response, error) {
		require.Equal(t, "Bearer upstream", r.Header.Get("Authorization"))
		return &http.Response{StatusCode: http.StatusUnprocessableEntity, Header: http.Header{"Content-Type": {"text/plain"}}, Body: io.NopCloser(strings.NewReader("provider-owned error"))}, nil
	}}
	router, group := newCodexAPIRoutesFixture(t, []service.Account{account}, upstream, nil)
	for _, route := range service.CodexAPIEndpointRoutes() {
		if route.Method != http.MethodPost {
			continue
		}
		t.Run(route.Method+route.Path, func(t *testing.T) {
			r := httptest.NewRequest(route.Method, route.Path, strings.NewReader(`{"model":"future-model","future_field":true}`))
			r.Header.Set("Authorization", "Bearer local")
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			require.Equal(t, http.StatusUnprocessableEntity, w.Code, w.Body.String())
			require.Equal(t, "provider-owned error", w.Body.String())
		})
	}
	group.AllowImageGeneration = false
	r := httptest.NewRequest("POST", "/codex/images/generations", strings.NewReader(`{"model":"future-model"}`))
	r.Header.Set("Authorization", "Bearer local")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	require.Equal(t, http.StatusForbidden, w.Code)
	for _, path := range []string{"/v1/files", "/codex/guardian", "/backend-api/codex/images/edits"} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("POST", path, nil))
		require.Equal(t, http.StatusUnauthorized, w.Code)
	}
	ordinary, _ := newCodexAPIRoutesFixture(t, []service.Account{{Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey}}, nil, nil)
	w = httptest.NewRecorder()
	r = httptest.NewRequest("POST", "/codex/guardian", nil)
	r.Header.Set("Authorization", "Bearer local")
	ordinary.ServeHTTP(w, r)
	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestCodexAPIRoutesWebSocketRelay(t *testing.T) {
	seen := make(chan string, 1)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.URL.Path + "?" + r.URL.RawQuery + "|" + r.Header.Get("Authorization")
		conn, err := coderws.Accept(w, r, nil)
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
		_ = conn.Close(coderws.StatusNormalClosure, "complete")
	}))
	defer provider.Close()
	account := service.Account{ID: 1, Platform: service.PlatformOpenAI, Type: service.AccountTypeCodexAPI, Status: service.StatusActive, Schedulable: true, Concurrency: 1, Credentials: map[string]any{"api_key": "upstream", "base_url": provider.URL}}
	router, _ := newCodexAPIRoutesFixture(t, []service.Account{account}, nil, nil)
	server := httptest.NewServer(router)
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	conn, _, err := coderws.Dial(ctx, server.URL+"/codex/responses?future=a%20b&api_key=local", &coderws.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer local"}}})
	require.NoError(t, err)
	defer func() { _ = conn.CloseNow() }()
	require.Equal(t, "/backend-api/codex/responses?future=a%20b|Bearer upstream", <-seen)
	blocked := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/v1/models", nil)
	request.Header.Set("Authorization", "Bearer local")
	router.ServeHTTP(blocked, request)
	require.Equal(t, http.StatusServiceUnavailable, blocked.Code)
	for _, kind := range []coderws.MessageType{coderws.MessageText, coderws.MessageBinary} {
		payload := []byte(`{ "type":"response.create", "model":"future-model", "namespace":"original" }`)
		require.NoError(t, conn.Write(ctx, kind, payload))
		gotKind, got, err := conn.Read(ctx)
		require.NoError(t, err)
		require.Equal(t, kind, gotKind)
		require.Equal(t, payload, got)
	}
}

func TestCodexAPICallSidebandKeepsAccountAndOwner(t *testing.T) {
	cache := &codexAPIRouteCache{resources: make(map[string]int64)}
	account := service.Account{ID: 1, Platform: service.PlatformOpenAI, Type: service.AccountTypeCodexAPI, Status: service.StatusActive, Schedulable: true, Credentials: map[string]any{"api_key": "upstream", "base_url": "https://codex.example"}}
	calls := 0
	upstream := codexAPIRoutesUpstream{do: func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: http.StatusCreated, Header: http.Header{"Location": {"/v1/live/call_created"}}, Body: io.NopCloser(strings.NewReader("sdp-answer"))}, nil
	}}
	router, _ := newCodexAPIRoutesFixture(t, []service.Account{account}, upstream, cache)
	r := httptest.NewRequest("POST", "/v1/realtime/calls", strings.NewReader(`{"sdp":"offer","session":{"model":"future-model"}}`))
	r.Header.Set("Authorization", "Bearer local")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	require.Equal(t, "sdp-answer", w.Body.String())
	require.Equal(t, "/v1/live/call_created", w.Header().Get("Location"))
	require.Len(t, cache.resources, 1)
	r = httptest.NewRequest("GET", "/v1/realtime?call_id=unowned", nil)
	r.Header.Set("Authorization", "Bearer local")
	r.Header.Set("Connection", "Upgrade")
	r.Header.Set("Upgrade", "websocket")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, r)
	require.Equal(t, http.StatusConflict, w.Code)
	require.Equal(t, 1, calls)
}

func TestCodexAPIFileReferenceKeepsUploadAccount(t *testing.T) {
	cache := &codexAPIRouteCache{resources: make(map[string]int64)}
	account := service.Account{ID: 2, Platform: service.PlatformOpenAI, Type: service.AccountTypeCodexAPI, Status: service.StatusActive, Schedulable: true, Credentials: map[string]any{"api_key": "file-owner", "base_url": "https://codex.example"}}
	upstream := codexAPIRoutesUpstream{do: func(r *http.Request) (*http.Response, error) {
		require.Equal(t, "Bearer file-owner", r.Header.Get("Authorization"))
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"id":"file-uploaded"}`))}, nil
	}}
	router, _ := newCodexAPIRoutesFixture(t, []service.Account{account}, upstream, cache)
	r := httptest.NewRequest("POST", "/v1/files", strings.NewReader("original multipart bytes"))
	r.Header.Set("Authorization", "Bearer local")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Equal(t, `{"id":"file-uploaded"}`, w.Body.String())
	require.Len(t, cache.resources, 1)
	other := account
	other.ID = 1
	other.Credentials = map[string]any{"api_key": "wrong-owner", "base_url": "https://codex.example"}
	router, _ = newCodexAPIRoutesFixture(t, []service.Account{other, account}, upstream, cache)
	r = httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"future-model","input":[{"file_id":"file-uploaded"}]}`))
	r.Header.Set("Authorization", "Bearer local")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
}

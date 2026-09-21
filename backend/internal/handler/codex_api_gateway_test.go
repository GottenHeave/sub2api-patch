package handler

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type codexAPIHandlerUpstream struct {
	service.HTTPUpstream
	do func(*http.Request) (*http.Response, error)
}

func (r *gatewayModelsAccountRepoStub) ListAllWithFilters(ctx context.Context, _, _, _, _ string, groupID int64, _ string) ([]service.Account, error) {
	return r.ListByGroup(ctx, groupID)
}

func (u codexAPIHandlerUpstream) Do(r *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return u.do(r)
}

func TestCodexAPIModelsUpstreamAndCancellationReleaseSlots(t *testing.T) {
	for _, cancelRequest := range []bool{false, true} {
		t.Run(map[bool]string{false: "upstream error", true: "canceled"}[cancelRequest], func(t *testing.T) {
			groupID := int64(1)
			account := service.Account{ID: 2, Platform: service.PlatformOpenAI, Type: service.AccountTypeCodexAPI, Status: service.StatusActive, Schedulable: true, Concurrency: 1, Credentials: map[string]any{"base_url": "https://codex.example", "api_key": "upstream-key"}}
			repo := &gatewayModelsAccountRepoStub{byGroup: map[int64][]service.Account{groupID: {account}}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			upstream := codexAPIHandlerUpstream{do: func(r *http.Request) (*http.Response, error) {
				calls++
				require.Equal(t, "/backend-api/codex/models", r.URL.Path)
				require.Equal(t, "Bearer upstream-key", r.Header.Get("Authorization"))
				if cancelRequest {
					cancel()
					return nil, r.Context().Err()
				}
				return &http.Response{StatusCode: http.StatusForbidden, Header: http.Header{"Etag": {"native-tag"}}, Body: io.NopCloser(strings.NewReader("upstream catalog denied"))}, nil
			}}
			gateway := service.NewOpenAIGatewayService(repo, nil, nil, nil, nil, nil, nil, &config.Config{RunMode: config.RunModeSimple}, nil, nil, nil, nil, nil, upstream, nil, nil, nil, nil, nil, nil, nil, nil)
			cache := &helperConcurrencyCacheStub{userSeq: []bool{true}, accountSeq: []bool{true}}
			h := &OpenAIGatewayHandler{gatewayService: gateway, concurrencyHelper: NewConcurrencyHelper(service.NewConcurrencyService(cache), SSEPingFormatComment, 0)}
			writer := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(writer)
			c.Request = httptest.NewRequest(http.MethodGet, "/backend-api/codex/models?client_version=client", nil).WithContext(ctx)
			c.Request.Header.Set("Authorization", "Bearer downstream-key")
			c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{ID: 1, GroupID: &groupID, Group: &service.Group{Platform: service.PlatformOpenAI}})
			c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 1, Concurrency: 1})
			h.CodexAPIDispatch(c)
			require.Equal(t, 1, calls)
			require.Equal(t, 1, cache.userReleaseCalls)
			require.Equal(t, 1, cache.accountReleaseCalls)
			if !cancelRequest {
				require.Equal(t, http.StatusForbidden, writer.Code)
				require.Equal(t, "upstream catalog denied", writer.Body.String())
				require.Equal(t, "native-tag", writer.Header().Get("ETag"))
			}
		})
	}
}

func TestCodexAPIDispatchGroupBoundary(t *testing.T) {
	for _, test := range []struct {
		name      string
		members   []service.Account
		path      string
		status    int
		continued bool
	}{
		{"ordinary", []service.Account{{Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey}}, "/v1/chat/completions", http.StatusNoContent, true},
		{"disabled dedicated unsupported", []service.Account{{Platform: service.PlatformOpenAI, Type: service.AccountTypeCodexAPI, Status: "disabled"}}, "/v1/chat/completions", http.StatusNotFound, false},
		{"disabled dedicated websocket", []service.Account{{Platform: service.PlatformOpenAI, Type: service.AccountTypeCodexAPI, Status: "disabled"}}, "/responses", http.StatusNotFound, false},
		{"mixed", []service.Account{{Platform: service.PlatformOpenAI, Type: service.AccountTypeCodexAPI}, {Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey}}, "/models", http.StatusServiceUnavailable, false},
		{"disabled models", []service.Account{{Platform: service.PlatformOpenAI, Type: service.AccountTypeCodexAPI, Status: "disabled"}}, "/models", http.StatusServiceUnavailable, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := &gatewayModelsAccountRepoStub{byGroup: map[int64][]service.Account{1: test.members}}
			gateway := service.NewOpenAIGatewayService(repo, nil, nil, nil, nil, nil, nil, &config.Config{RunMode: config.RunModeSimple}, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
			cache := &helperConcurrencyCacheStub{userSeq: []bool{true}}
			h := &OpenAIGatewayHandler{gatewayService: gateway, concurrencyHelper: NewConcurrencyHelper(service.NewConcurrencyService(cache), SSEPingFormatComment, 0)}
			engine := gin.New()
			groupID := int64(1)
			engine.Use(func(c *gin.Context) {
				c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{ID: 1, GroupID: &groupID, Group: &service.Group{Platform: service.PlatformOpenAI}})
				c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 1, Concurrency: 1})
			}, h.CodexAPIDispatch)
			continued := false
			engine.GET(test.path, func(c *gin.Context) { continued = true; c.Status(http.StatusNoContent) })
			writer := httptest.NewRecorder()
			engine.ServeHTTP(writer, httptest.NewRequest(http.MethodGet, test.path, nil))
			require.Equal(t, test.status, writer.Code)
			require.Equal(t, test.continued, continued)
			require.Equal(t, cache.userAcquireCalls, cache.userReleaseCalls)
		})
	}
}

func TestCodexAPITarget(t *testing.T) {
	for _, test := range []struct{ method, path, originator, target string }{
		{"POST", "/v1/responses", "", "/v1/responses"},
		{"POST", "/responses", "codex_cli_rs", "/v1/responses"},
		{"POST", "/v1/responses", "codex_cli_rs", "/v1/responses"},
		{"POST", "/backend-api/codex/responses", "", "/backend-api/codex/responses"},
		{"POST", "/backend-api/codex/responses/compact", "", "/v1/responses/compact"},
		{"GET", "/v1/models", "", "/v1/models"},
		{"GET", "/models?client_version=1", "", "/v1/models"},
		{"GET", "/v1/models?client_version=1", "codex_cli_rs", "/v1/models"},
		{"GET", "/backend-api/codex/models", "", "/backend-api/codex/models"},
		{"GET", "/responses", "", ""},
		{"POST", "/v1/chat/completions", "", ""},
		{"POST", "/responses/unknown", "", ""},
	} {
		t.Run(test.method+test.path+test.originator, func(t *testing.T) {
			r := httptest.NewRequest(test.method, test.path, nil)
			r.Header.Set("originator", test.originator)
			r.Header.Set("User-Agent", "codex_cli_rs/1.0")
			require.Equal(t, test.target, codexAPITarget(r))
		})
	}
}

func TestRelayCodexAPIResponsePreservesUpstream(t *testing.T) {
	for _, test := range []struct {
		status int
		body   string
	}{
		{http.StatusOK, ": upstream comment\n\nevent: novel\ndata: {\"namespace\":\"original\"}\n\n"},
		{http.StatusUnprocessableEntity, "{ \"error\": \"upstream decision\", \"extra\": true }"},
	} {
		writer := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(writer)
		response := &http.Response{StatusCode: test.status, Header: http.Header{
			"Etag": {"upstream-tag"}, "Connection": {"X-Hop"}, "X-Hop": {"remove"},
		}, Body: io.NopCloser(strings.NewReader(test.body))}
		require.NoError(t, relayCodexAPIResponse(c, response))
		require.Equal(t, test.status, writer.Code)
		require.Equal(t, test.body, writer.Body.String())
		require.Equal(t, "upstream-tag", writer.Header().Get("ETag"))
		require.Empty(t, writer.Header().Get("X-Hop"))
	}
}

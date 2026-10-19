package handler

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/klauspost/compress/zstd"
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

type codexAPIEndpointCase struct {
	method    string
	path      string
	websocket bool
	target    string
}

func TestCodexAPISubscriptionEndpoints(t *testing.T) {
	for _, test := range []codexAPIEndpointCase{
		{"POST", "/v1/images/generations", false, "/v1/images/generations"},
		{"POST", "/codex/images/edits", false, "/backend-api/codex/images/edits"},
		{"POST", "/v1/files", false, "/v1/files"},
		{"POST", "/v1/audio/transcriptions", false, "/v1/audio/transcriptions"},
		{"POST", "/transcribe", false, "/v1/audio/transcriptions"},
		{"POST", "/backend-api/transcribe", false, "/v1/audio/transcriptions"},
		{"POST", "/v1/alpha/search", false, "/v1/alpha/search"},
		{"POST", "/backend-api/codex/memories/trace_summarize", false, "/backend-api/codex/memories/trace_summarize"},
		{"POST", "/codex/guardian", false, "/backend-api/codex/guardian"},
		{"GET", "/codex/guardian-classifier", true, "/backend-api/codex/guardian-classifier"},
		{"GET", "/v1/responses", true, "/v1/responses"},
		{"GET", "/codex/models", false, "/backend-api/codex/models"},
		{"POST", "/codex/responses/compact", false, "/v1/responses/compact"},
		{"POST", "/v1/realtime/calls", false, "/v1/realtime/calls"},
		{"POST", "/backend-api/codex/live", false, "/backend-api/codex/live"},
		{"GET", "/v1/realtime?call_id=call_example", true, "/v1/realtime"},
		{"GET", "/codex/live/call_example", true, "/backend-api/codex/live/call_example"},
		{"GET", "/v1/realtime", true, ""},
		{"GET", "/v1/live", true, ""},
		{"POST", "/v1/live/sessions", false, ""},
		{"GET", "/v1/live/sessions/session_example/attach", true, ""},
		{"POST", "/platform/files", false, ""},
		{"POST", "/auth/oauth/token", false, ""},
		{"GET", "/backend-api/wham/usage", false, ""},
		{"POST", "/codex/alpha/notes/v2/write_file", false, ""},
		{"GET", "/backend-api/ps/plugins/list", false, ""},
		{"GET", "/v1/files/file_example", false, ""},
		{"DELETE", "/v1/files/file_example", false, ""},
		{"POST", "/v1/images/edits/async", false, ""},
		{"POST", "/v1/realtime/client_secrets", false, ""},
	} {
		t.Run(test.method+test.path, func(t *testing.T) {
			r := httptest.NewRequest(test.method, test.path, nil)
			if test.websocket {
				r.Header.Set("Connection", "Upgrade")
				r.Header.Set("Upgrade", "websocket")
			}
			require.Equal(t, test.target, codexAPITarget(r))
		})
	}
}

func newCodexAPITimingFixture(t *testing.T, baseURL string, upstream service.HTTPUpstream, configure ...func(*config.Config)) (*gin.Engine, <-chan *service.UsageLog) {
	t.Helper()
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Default.RateMultiplier = 1
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	for _, apply := range configure {
		apply(cfg)
	}
	account := service.Account{ID: 2, Platform: service.PlatformOpenAI, Type: service.AccountTypeCodexAPI, Status: service.StatusActive, Schedulable: true, Concurrency: 1, Credentials: map[string]any{"base_url": baseURL, "api_key": "test-key"}}
	group := &service.Group{ID: 1, Platform: service.PlatformOpenAI, RateMultiplier: 1}
	repo := &gatewayModelsAccountRepoStub{byGroup: map[int64][]service.Account{group.ID: {account}}}
	usage := &openAIWSUsageHandlerUsageLogRepoStub{created: make(chan *service.UsageLog, 4)}
	billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billing.Stop)
	gateway := service.NewOpenAIGatewayService(repo, usage, nil, nil, nil, nil, nil, cfg, nil, nil, service.NewBillingService(cfg, nil), nil, billing, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil)
	slots := &helperConcurrencyCacheStub{userSeq: []bool{true}, accountSeq: []bool{true}}
	h := &OpenAIGatewayHandler{cfg: cfg, gatewayService: gateway, billingCacheService: billing, concurrencyHelper: NewConcurrencyHelper(service.NewConcurrencyService(slots), SSEPingFormatComment, 0)}
	router := gin.New()
	for _, path := range []string{"/v1/responses", "/codex/responses", "/backend-api/codex/responses", "/v1/responses/compact", "/v1/images/generations"} {
		router.Any(path, func(c *gin.Context) {
			c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{ID: 1, GroupID: &group.ID, Group: group, User: &service.User{ID: 1}})
			c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 1, Concurrency: 1})
		}, h.CodexAPIDispatch)
	}
	return router, usage.created
}

func TestCodexAPIRequestBodyLimits(t *testing.T) {
	for _, test := range []struct {
		name       string
		path       string
		size       int
		compressed bool
		status     int
	}{
		{"large public responses", "/v1/responses", (16 << 20) + 1, false, http.StatusUnprocessableEntity},
		{"large native alias", "/codex/responses", (16 << 20) + 1, false, http.StatusUnprocessableEntity},
		{"large native responses", "/backend-api/codex/responses", (16 << 20) + 1, false, http.StatusUnprocessableEntity},
		{"exact wire ceiling", "/v1/responses", 64 << 20, false, http.StatusUnprocessableEntity},
		{"exceeds wire ceiling", "/v1/responses", (64 << 20) + 1, false, http.StatusRequestEntityTooLarge},
		{"exact decoded ceiling", "/backend-api/codex/responses", 64 << 20, true, http.StatusUnprocessableEntity},
		{"exceeds decoded ceiling", "/backend-api/codex/responses", (64 << 20) + 1, true, http.StatusBadRequest},
		{"compact keeps wire ceiling", "/v1/responses/compact", (16 << 20) + 1, false, http.StatusRequestEntityTooLarge},
		{"compact keeps decoded ceiling", "/v1/responses/compact", (16 << 20) + 1, true, http.StatusBadRequest},
		{"images keep wire ceiling", "/v1/images/generations", (16 << 20) + 1, false, http.StatusRequestEntityTooLarge},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := bytes.Repeat([]byte{'x'}, test.size)
			copy(body, `{"model":"gpt-5.1","input":"`)
			copy(body[len(body)-2:], `"}`)
			encoding := ""
			if test.compressed {
				encoder, err := zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1))
				require.NoError(t, err)
				body = encoder.EncodeAll(body, nil)
				require.NoError(t, encoder.Close())
				encoding = "zstd"
			}
			calls := 0
			upstream := codexAPIHandlerUpstream{do: func(request *http.Request) (*http.Response, error) {
				calls++
				forwarded, err := io.ReadAll(request.Body)
				require.NoError(t, err)
				require.True(t, bytes.Equal(body, forwarded), "forwarding must preserve the original wire bytes")
				require.Equal(t, encoding, request.Header.Get("Content-Encoding"))
				return &http.Response{StatusCode: http.StatusUnprocessableEntity, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("fixture upstream"))}, nil
			}}
			router, _ := newCodexAPITimingFixture(t, "https://codex.example", upstream)
			request := httptest.NewRequest(http.MethodPost, test.path, bytes.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Content-Encoding", encoding)
			writer := httptest.NewRecorder()
			router.ServeHTTP(writer, request)
			require.Equal(t, test.status, writer.Code, writer.Body.String())
			if test.status == http.StatusUnprocessableEntity {
				require.Equal(t, 1, calls)
			} else {
				require.Zero(t, calls)
			}
		})
	}
}

func TestCodexAPIStreamingUsageRecordsFirstToken(t *testing.T) {
	chunks := []string{
		": heartbeat\n\n",
		"data: {\"type\":\"response.created\"}\n\n",
		"event: response.output_text.delta\ndata: {\"delta\":",
		"\"hello\"}\n\n",
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\" again\"}\n\n",
		"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_http\",\"usage\":{\"input_tokens\":12,\"output_tokens\":3}}}\n\n",
	}
	upstream := codexAPIHandlerUpstream{do: func(*http.Request) (*http.Response, error) {
		time.Sleep(50 * time.Millisecond)
		reader, writer := io.Pipe()
		go func() {
			defer func() { _ = writer.Close() }()
			for _, chunk := range chunks {
				time.Sleep(100 * time.Millisecond)
				if _, err := io.WriteString(writer, chunk); err != nil {
					return
				}
			}
		}()
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: reader}, nil
	}}
	router, logs := newCodexAPITimingFixture(t, "https://codex.example", upstream)
	synctest.Test(t, func(t *testing.T) {
		writer := httptest.NewRecorder()
		router.ServeHTTP(writer, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.1","stream":true}`)))
		require.Equal(t, http.StatusOK, writer.Code)
		require.Equal(t, strings.Join(chunks, ""), writer.Body.String())
		select {
		case log := <-logs:
			require.NotNil(t, log.FirstTokenMs)
			require.Equal(t, 450, *log.FirstTokenMs)
			require.EqualValues(t, 650, *log.DurationMs)
			require.Equal(t, 12, log.InputTokens)
			require.Equal(t, 3, log.OutputTokens)
		default:
			t.Fatal("stream usage was not recorded")
		}
	})
}

func TestCodexAPIWebSocketUsageRecordsPerTurnTiming(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer func() { _ = conn.CloseNow() }()
		for turn := range 3 {
			if _, _, err := conn.Read(r.Context()); err != nil {
				return
			}
			id := fmt.Sprintf("resp_%d", turn)
			frames := []string{fmt.Sprintf(`{"type":"response.created","response":{"id":%q}}`, id)}
			if turn < 2 {
				frames = append(frames, `{"type":"response.output_text.delta","item_id":"msg_1","delta":"hello"}`)
			}
			frames = append(frames, fmt.Sprintf(`{"type":"response.completed","response":{"id":%q,"model":"gpt-5.1","usage":{"input_tokens":12,"output_tokens":3}}}`, id))
			for _, frame := range frames {
				time.Sleep(40 * time.Millisecond)
				if err := conn.Write(r.Context(), coderws.MessageText, []byte(frame)); err != nil {
					return
				}
			}
		}
		_ = conn.Close(coderws.StatusNormalClosure, "complete")
	}))
	defer upstream.Close()
	router, logs := newCodexAPITimingFixture(t, upstream.URL, nil)
	server := httptest.NewServer(router)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/v1/responses", nil)
	require.NoError(t, err)
	defer func() { _ = client.CloseNow() }()
	for turn := range 3 {
		time.Sleep(250 * time.Millisecond)
		start := time.Now()
		require.NoError(t, client.Write(ctx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-5.1","input":[]}`)))
		frameCount := 3
		if turn == 2 {
			frameCount = 2
		}
		for range frameCount {
			kind, _, err := client.Read(ctx)
			require.NoError(t, err)
			require.Equal(t, coderws.MessageText, kind)
		}
		elapsed := time.Since(start).Milliseconds()
		select {
		case log := <-logs:
			require.NotNil(t, log.DurationMs)
			require.LessOrEqual(t, int64(*log.DurationMs), elapsed, "turn duration includes connection idle time")
			if turn < 2 {
				require.NotNil(t, log.FirstTokenMs)
				require.GreaterOrEqual(t, *log.FirstTokenMs, 80)
				require.Less(t, *log.FirstTokenMs, *log.DurationMs)
			} else {
				require.Nil(t, log.FirstTokenMs, "metadata-only turn must not inherit the preceding token time")
			}
			require.Equal(t, 12, log.InputTokens)
			require.Equal(t, 3, log.OutputTokens)
		case <-ctx.Done():
			t.Fatal("WebSocket usage was not recorded")
		}
	}
}

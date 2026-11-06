package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestAccountUsageCodexAPIQueriesGateway(t *testing.T) {
	account := newCodexModelsAPIKeyTestAccount("https://gateway.example/prefix/v1")
	account.Type = AccountTypeCodexAPI
	calls := 0
	gateway := newCodexModelsAPIKeyTestService(&codexModelsHTTPUpstreamStub{do: func(req *http.Request, _ string, id int64, concurrency int) (*http.Response, error) {
		calls++
		require.Equal(t, account.ID, id)
		require.Equal(t, account.Concurrency, concurrency)
		require.Equal(t, http.MethodGet, req.Method)
		require.Equal(t, "https://gateway.example/prefix/backend-api/wham/usage", req.URL.String())
		require.Equal(t, "Bearer sk-upstream", req.Header.Get("Authorization"))
		require.Empty(t, req.Header.Get("ChatGPT-Account-ID"))
		require.True(t, HTTPUpstreamRedirectsDisabled(req.Context()))
		require.Equal(t, HTTPUpstreamProfileRaw, HTTPUpstreamProfileFromContext(req.Context()))
		deadline, ok := req.Context().Deadline()
		require.True(t, ok)
		require.WithinDuration(t, time.Now().Add(openaiQuotaUpstreamTimeout), deadline, time.Second)
		return ordinaryModelsUpstreamResponse(`{"email":"private@example.com","rate_limit":{"allowed":true,"primary_window":{"used_percent":23,"limit_window_seconds":18000,"reset_after_seconds":3600,"reset_at":1793970000},"secondary_window":null},"additional_rate_limits":[{"limit_name":"Codex Spark","metered_feature":"spark","rate_limit":{"primary_window":{"used_percent":67,"limit_window_seconds":604800,"reset_after_seconds":7200,"reset_at":1793973600}}}]}`), nil
	}})
	svc := ProvideAccountUsageService(codexAPIConnectionTestRepository{account: account}, nil, nil, nil, nil, nil, nil, nil, NewUsageCache(), nil, nil, gateway)
	usage, err := svc.GetUsage(t.Context(), account.ID)
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	body, err := json.Marshal(usage)
	require.NoError(t, err)
	quota := gjson.GetBytes(body, "codex_api_quota")
	require.Equal(t, float64(23), quota.Get("rate_limit.primary_window.used_percent").Float())
	require.Equal(t, int64(18000), quota.Get("rate_limit.primary_window.limit_window_seconds").Int())
	require.Equal(t, int64(3600), quota.Get("rate_limit.primary_window.reset_after_seconds").Int())
	require.Equal(t, int64(1793970000), quota.Get("rate_limit.primary_window.reset_at").Int())
	require.False(t, quota.Get("rate_limit.secondary_window").Exists())
	require.Equal(t, float64(67), quota.Get("additional_rate_limits.0.rate_limit.primary_window.used_percent").Float())
	require.Positive(t, quota.Get("fetched_at").Int())
	require.NotContains(t, string(body), "private@example.com")
	require.Nil(t, usage.FiveHour)
	require.Nil(t, usage.SevenDay)

	_, err = gateway.RoundTripCodexAPI(t.Context(), account, httptest.NewRequest(http.MethodGet, "/backend-api/wham/usage", nil), "/backend-api/wham/usage")
	require.Error(t, err, "admin usage must not extend the public forwarding policy")
	require.Equal(t, 1, calls)
}

func TestAccountUsageCodexAPIFailures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		err    error
		want   string
	}{
		{name: "unauthorized", status: http.StatusUnauthorized, body: "private response", want: "HTTP 401"},
		{name: "rate limited", status: http.StatusTooManyRequests, want: "HTTP 429"},
		{name: "redirect", status: http.StatusTemporaryRedirect, want: "HTTP 307"},
		{name: "invalid JSON", status: http.StatusOK, body: "not JSON", want: "invalid Codex API usage response"},
		{name: "oversized", status: http.StatusOK, body: strings.Repeat(" ", (1<<20)+1), want: "invalid Codex API usage response"},
		{name: "transport", err: errors.New("private transport details"), want: "codex API usage request failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gateway := newCodexModelsAPIKeyTestService(&codexModelsHTTPUpstreamStub{do: func(_ *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
				if tc.err != nil {
					return nil, tc.err
				}
				resp := ordinaryModelsUpstreamResponse(tc.body)
				resp.StatusCode = tc.status
				return resp, nil
			}})
			account := newCodexModelsAPIKeyTestAccount("https://gateway.example")
			account.Type = AccountTypeCodexAPI
			svc := ProvideAccountUsageService(codexAPIConnectionTestRepository{account: account}, nil, nil, nil, nil, nil, nil, nil, NewUsageCache(), nil, nil, gateway)
			usage, err := svc.GetUsage(context.Background(), account.ID)
			require.ErrorContains(t, err, tc.want)
			require.Nil(t, usage)
			require.NotContains(t, err.Error(), "private")
		})
	}
}

func TestAccountUsageCodexAPIMissingWindows(t *testing.T) {
	for _, body := range []string{`{}`, `{"rate_limit":{"primary_window":null,"secondary_window":null}}`} {
		t.Run(body, func(t *testing.T) {
			gateway := newCodexModelsAPIKeyTestService(&codexModelsHTTPUpstreamStub{do: func(_ *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
				return ordinaryModelsUpstreamResponse(body), nil
			}})
			account := newCodexModelsAPIKeyTestAccount("https://gateway.example")
			account.Type = AccountTypeCodexAPI
			svc := ProvideAccountUsageService(codexAPIConnectionTestRepository{account: account}, nil, nil, nil, nil, nil, nil, nil, NewUsageCache(), nil, nil, gateway)
			usage, err := svc.GetUsage(t.Context(), account.ID)
			require.NoError(t, err)
			require.NotNil(t, usage.CodexAPIQuota)
			if usage.CodexAPIQuota.RateLimit != nil {
				require.Nil(t, usage.CodexAPIQuota.RateLimit.PrimaryWindow)
				require.Nil(t, usage.CodexAPIQuota.RateLimit.SecondaryWindow)
			}
			require.Nil(t, usage.FiveHour)
			require.Nil(t, usage.SevenDay)
		})
	}
}

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

func (s *AccountUsageService) getCodexAPIUsage(ctx context.Context, account *Account) (*UsageInfo, error) {
	gateway := s.openaiGatewayService
	if gateway == nil || gateway.httpUpstream == nil {
		return nil, fmt.Errorf("codex API usage service is unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, openaiQuotaUpstreamTimeout)
	defer cancel()
	incoming, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost/backend-api/wham/usage", nil)
	if err != nil {
		return nil, err
	}
	req, err := gateway.buildCodexAPIUpstreamRequest(ctx, account, incoming, incoming.URL.Path)
	if err != nil {
		return nil, fmt.Errorf("invalid Codex API usage configuration")
	}
	proxyURL := ""
	if account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	resp, err := gateway.httpUpstream.Do(req, proxyURL, account.ID, account.Concurrency)
	if err != nil {
		return nil, fmt.Errorf("codex API usage request failed")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("codex API usage returned HTTP %d", resp.StatusCode)
	}
	const maxBody = 1 << 20
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil || len(body) > maxBody {
		return nil, fmt.Errorf("invalid Codex API usage response")
	}
	var payload OpenAIQuotaUsage
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("invalid Codex API usage response")
	}
	now := time.Now()
	return &UsageInfo{
		UpdatedAt: &now,
		CodexAPIQuota: &OpenAIQuotaUsage{
			RateLimit:            payload.RateLimit,
			AdditionalRateLimits: payload.AdditionalRateLimits,
			FetchedAt:            now.Unix(),
		},
	}, nil
}

package service

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// RoundTripCodexAPI leaves protocol interpretation to the configured gateway.
func (s *OpenAIGatewayService) RoundTripCodexAPI(ctx context.Context, account *Account, incoming *http.Request, targetPath string) (*http.Response, error) {
	if account == nil || !account.IsCodexAPI() || incoming == nil || incoming.URL == nil {
		return nil, fmt.Errorf("invalid Codex API request")
	}
	switch targetPath {
	case "/v1/responses", "/backend-api/codex/responses", "/v1/responses/compact", "/v1/models", "/backend-api/codex/models":
	default:
		return nil, fmt.Errorf("unsupported Codex API endpoint")
	}
	base, err := s.validateUpstreamBaseURL(strings.TrimSpace(account.GetCredential("base_url")))
	if err != nil {
		return nil, err
	}
	target, err := url.Parse(base)
	if err != nil || target.User != nil || target.RawQuery != "" || target.Fragment != "" {
		return nil, fmt.Errorf("invalid Codex API base URL")
	}
	token := strings.TrimSpace(account.GetCredential("api_key"))
	if token == "" {
		return nil, fmt.Errorf("missing Codex API gateway key")
	}
	escapedPath := strings.TrimSuffix(strings.TrimRight(target.EscapedPath(), "/"), "/v1") + targetPath
	target.Path, err = url.PathUnescape(escapedPath)
	if err != nil {
		return nil, fmt.Errorf("invalid Codex API base path")
	}
	target.RawPath = escapedPath
	target.RawQuery = codexAPIQuery(incoming.URL.RawQuery)
	ctx = WithHTTPUpstreamRedirectsDisabled(WithHTTPUpstreamProfile(ctx, HTTPUpstreamProfileRaw))
	req, err := http.NewRequestWithContext(ctx, incoming.Method, target.String(), incoming.Body)
	if err != nil {
		return nil, err
	}
	req.ContentLength = incoming.ContentLength
	req.Header = incoming.Header.Clone()
	StripCodexAPIHopHeaders(req.Header)
	for _, name := range []string{"Authorization", "X-Codex-Gateway-Authorization", "X-Api-Key", "X-Goog-Api-Key", "Cookie"} {
		req.Header.Del(name)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	// Suppress Go's synthetic User-Agent when the caller sent none.
	if _, ok := req.Header["User-Agent"]; !ok {
		req.Header["User-Agent"] = []string{""}
	}
	proxyURL := ""
	if account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	return s.httpUpstream.Do(req, proxyURL, account.ID, account.Concurrency)
}

// StripCodexAPIHopHeaders removes fields that apply only to a single connection.
func StripCodexAPIHopHeaders(header http.Header) {
	for _, value := range header.Values("Connection") {
		for _, name := range strings.Split(value, ",") {
			header.Del(strings.TrimSpace(name))
		}
	}
	for _, name := range []string{"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade"} {
		header.Del(name)
	}
}

func codexAPIQuery(raw string) string {
	parts := strings.Split(raw, "&")
	kept := parts[:0]
	for _, part := range parts {
		key, _, _ := strings.Cut(part, "=")
		decoded, err := url.QueryUnescape(key)
		if err == nil && (decoded == "key" || decoded == "api_key") {
			continue
		}
		kept = append(kept, part)
	}
	return strings.Join(kept, "&")
}

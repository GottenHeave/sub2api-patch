package service

import (
	"net/http"
	"strings"
)

type CodexAPIEndpointRoute struct {
	Method string
	Path   string
}

// Only inference and subscription resource adapters belong to dedicated groups.
// Account management and caller-owned upstream credentials are never forwarded.
func CodexAPITarget(r *http.Request) string {
	if r == nil || r.URL == nil {
		return ""
	}
	path := r.URL.Path
	native := false
	for _, prefix := range []string{"/backend-api/codex", "/codex"} {
		if strings.HasPrefix(path, prefix+"/") {
			path = strings.TrimPrefix(path, prefix)
			native = true
			break
		}
	}
	if !native {
		path = strings.TrimPrefix(path, "/v1")
	}
	target := "/v1" + path
	if native {
		target = "/backend-api/codex" + path
	}
	switch r.Method {
	case http.MethodPost:
		switch path {
		case "/responses", "/images/generations", "/images/edits", "/alpha/search", "/realtime/calls", "/live":
			return target
		case "/responses/compact":
			return "/v1/responses/compact"
		case "/guardian", "/guardian-classifier", "/memories/trace_summarize":
			if native {
				return target
			}
		case "/files", "/audio/transcriptions":
			if !native {
				return target
			}
		case "/transcribe", "/backend-api/transcribe":
			if !native {
				return "/v1/audio/transcriptions"
			}
		}
	case http.MethodGet:
		if path == "/models" {
			return target
		}
		if !CodexAPIWebSocketRequest(r) {
			return ""
		}
		switch path {
		case "/responses":
			return target
		case "/guardian", "/guardian-classifier":
			if native {
				return target
			}
		case "/realtime", "/live":
			if validCodexAPICallID(r.URL.Query().Get("call_id")) {
				return target
			}
		default:
			if strings.HasPrefix(path, "/live/") && validCodexAPICallID(strings.TrimPrefix(path, "/live/")) {
				return target
			}
		}
	}
	return ""
}

func CodexAPIWebSocketRequest(r *http.Request) bool {
	if r == nil || r.Method != http.MethodGet || !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		return false
	}
	for _, token := range strings.Split(r.Header.Get("Connection"), ",") {
		if strings.EqualFold(strings.TrimSpace(token), "upgrade") {
			return true
		}
	}
	return false
}

func validCodexAPICallID(id string) bool {
	return len(id) > 0 && len(id) <= 256 && id != "sessions" && !strings.ContainsAny(id, "/\\?#\x00 \t\r\n") && id != "." && id != ".."
}

func CodexAPICallID(r *http.Request) string {
	if r == nil || r.URL == nil || !CodexAPIWebSocketRequest(r) {
		return ""
	}
	if i := strings.Index(r.URL.Path, "/live/"); i >= 0 {
		return strings.TrimPrefix(r.URL.Path[i:], "/live/")
	}
	return r.URL.Query().Get("call_id")
}

// The router uses the same inventory; existing ordinary routes keep their handlers.
func CodexAPIEndpointRoutes() []CodexAPIEndpointRoute {
	var routes []CodexAPIEndpointRoute
	for _, prefix := range []string{"", "/v1", "/codex", "/backend-api/codex"} {
		for _, path := range []string{"/responses", "/images/generations", "/images/edits", "/alpha/search", "/realtime/calls", "/live"} {
			routes = append(routes, CodexAPIEndpointRoute{http.MethodPost, prefix + path})
		}
		for _, path := range []string{"/responses", "/models", "/realtime", "/live", "/live/:call_id"} {
			routes = append(routes, CodexAPIEndpointRoute{http.MethodGet, prefix + path})
		}
		if prefix == "/codex" || prefix == "/backend-api/codex" {
			for _, path := range []string{"/guardian", "/guardian-classifier", "/memories/trace_summarize"} {
				routes = append(routes, CodexAPIEndpointRoute{http.MethodPost, prefix + path})
				if path != "/memories/trace_summarize" {
					routes = append(routes, CodexAPIEndpointRoute{http.MethodGet, prefix + path})
				}
			}
			if prefix == "/codex" {
				routes = append(routes, CodexAPIEndpointRoute{http.MethodPost, prefix + "/responses/compact"})
			}
		} else {
			routes = append(routes, CodexAPIEndpointRoute{http.MethodPost, prefix + "/files"}, CodexAPIEndpointRoute{http.MethodPost, prefix + "/audio/transcriptions"})
		}
	}
	return append(routes, CodexAPIEndpointRoute{http.MethodPost, "/transcribe"}, CodexAPIEndpointRoute{http.MethodPost, "/backend-api/transcribe"})
}

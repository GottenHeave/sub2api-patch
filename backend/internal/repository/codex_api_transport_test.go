package repository

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestRawUpstreamPreservesCompressedResponse(t *testing.T) {
	plain := []byte("event: upstream.event\ndata: {\"extra\":true}\n\n")
	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	_, err := zw.Write(plain)
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/raw" {
			require.Empty(t, r.Header.Get("Accept-Encoding"))
		}
		w.Header().Set("Content-Encoding", "gzip")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write(compressed.Bytes())
	}))
	defer server.Close()
	upstream := NewHTTPUpstream(nil)
	for _, raw := range []bool{false, true, false, true} {
		ctx := t.Context()
		path := "/ordinary"
		if raw {
			ctx = service.WithHTTPUpstreamProfile(ctx, service.HTTPUpstreamProfileRaw)
			path = "/raw"
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+path, nil)
		require.NoError(t, err)
		resp, err := upstream.Do(req, "", 1, 1)
		require.NoError(t, err)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		require.Equal(t, http.StatusBadRequest, resp.StatusCode)
		if raw {
			require.Equal(t, compressed.Bytes(), body)
			require.Equal(t, "gzip", resp.Header.Get("Content-Encoding"))
		} else {
			require.Equal(t, plain, body)
		}
	}
}

func TestRawUpstreamCancellationAndRedirect(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cancel" {
			close(started)
			<-r.Context().Done()
			return
		}
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/must-not-follow", http.StatusTemporaryRedirect)
			return
		}
		t.Error("unexpected redirected request")
	}))
	defer server.Close()
	ctx := service.WithHTTPUpstreamRedirectsDisabled(service.WithHTTPUpstreamProfile(t.Context(), service.HTTPUpstreamProfileRaw))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/redirect", nil)
	require.NoError(t, err)
	upstream := NewHTTPUpstream(nil)
	resp, err := upstream.Do(req, "", 1, 1)
	require.NoError(t, err)
	require.Equal(t, http.StatusTemporaryRedirect, resp.StatusCode)
	require.NoError(t, resp.Body.Close())
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	req, err = http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/cancel", nil)
	require.NoError(t, err)
	go func() {
		select {
		case <-started:
			cancel()
		case <-ctx.Done():
		}
	}()
	_, err = upstream.Do(req, "", 1, 1)
	require.ErrorIs(t, err, context.Canceled)
}

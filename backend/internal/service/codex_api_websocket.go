package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	coderws "github.com/coder/websocket"
)

var codexAPIProxyClients = &coderOpenAIWSClientDialer{proxyClients: make(map[string]*openAIWSProxyClientEntry)}

type codexAPIWSHTTPTransport struct {
	transport http.RoundTripper
	rejected  *http.Response
}

func (t *codexAPIWSHTTPTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.transport.RoundTrip(req)
	if err == nil && resp.StatusCode != http.StatusSwitchingProtocols {
		// Dial cancels its handshake context on return and retains only 1 KiB of
		// errors. Finish reading under that context before handing off the bytes.
		body, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		copy := *resp
		copy.Body = io.NopCloser(bytes.NewReader(body))
		t.rejected = &copy
		resp.Body = http.NoBody
		if readErr != nil {
			return nil, readErr
		}
	}
	return resp, err
}

func (s *OpenAIGatewayService) DialCodexAPI(ctx context.Context, account *Account, incoming *http.Request, target string) (*coderws.Conn, *http.Response, error) {
	req, err := s.buildCodexAPIRequest(ctx, account, incoming, target)
	if err != nil {
		return nil, nil, err
	}
	client := *http.DefaultClient
	if account.Proxy != nil {
		proxyClient, err := codexAPIProxyClients.proxyHTTPClient(account.Proxy.URL())
		if err != nil {
			return nil, nil, err
		}
		client = *proxyClient
	}
	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	if httpTransport, ok := transport.(*http.Transport); ok {
		clone := httpTransport.Clone()
		clone.DisableCompression = true
		defer clone.CloseIdleConnections()
		transport = clone
	}
	capture := &codexAPIWSHTTPTransport{transport: transport}
	client.Transport = capture
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	client.Timeout = 30 * time.Second
	for name := range req.Header {
		if strings.HasPrefix(strings.ToLower(name), "sec-websocket-") {
			req.Header.Del(name)
		}
	}
	var protocols []string
	for _, protocol := range strings.Split(incoming.Header.Get("Sec-WebSocket-Protocol"), ",") {
		if protocol = strings.TrimSpace(protocol); protocol != "" && !strings.HasPrefix(protocol, "openai-insecure-api-key.") {
			protocols = append(protocols, protocol)
		}
	}
	conn, response, err := coderws.Dial(ctx, req.URL.String(), &coderws.DialOptions{
		HTTPClient:   &client,
		HTTPHeader:   req.Header,
		Subprotocols: protocols,
	})
	if capture.rejected != nil {
		return nil, capture.rejected, err
	}
	if conn != nil {
		conn.SetReadLimit(openAIWSMessageReadLimitBytes)
	}
	return conn, response, err
}

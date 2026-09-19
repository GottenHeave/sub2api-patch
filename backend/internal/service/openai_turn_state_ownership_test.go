package service

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func turnStateOwnershipAccount(id int64, upstreamID string) *Account {
	return &Account{ID: id, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 1,
		Credentials: map[string]any{"access_token": "synthetic-token", "chatgpt_account_id": upstreamID},
		Extra:       map[string]any{"responses_websockets_v2_enabled": true}}
}

func TestTurnStateOwnershipWSGuard(t *testing.T) {
	accountA := turnStateOwnershipAccount(101, "account-a")
	accountB := turnStateOwnershipAccount(102, "account-b")
	sameCredential := turnStateOwnershipAccount(103, "account-a")
	svc := &OpenAIGatewayService{}
	c, _ := newTurnStateTestContext(t, 7, "session")
	upstream := http.Header{}
	upstream.Set(openAIWSTurnStateHeader, "state-a")
	svc.relayOpenAICodexTurnState(c, accountA, upstream)
	for _, tc := range []struct {
		name    string
		account *Account
		state   string
		want    string
	}{
		{"same", accountA, "state-a", "state-a"},
		{"same_credential_different_row", sameCredential, "state-a", "state-a"},
		{"different", accountB, "state-a", ""},
		{"unknown_blob", accountB, "unknown", "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			headers, _, err := svc.buildOpenAIWSHeaders(context.Background(), c, tc.account, "synthetic-token", OpenAIWSProtocolDecision{Transport: OpenAIUpstreamTransportResponsesWebsocketV2}, true, tc.state, "", "session", "gpt-5.2", "")
			require.NoError(t, err)
			require.Equal(t, tc.want, headers.Get(openAIWSTurnStateHeader))
			headers.Set(openAIWSTurnStateHeader, tc.state)
			svc.guardOpenAICodexTurnStateEcho(c, tc.account, headers)
			require.Equal(t, tc.want, headers.Get(openAIWSTurnStateHeader), "HTTP policy")
		})
	}
}

func TestTurnStateOwnershipSparseCredentialsAndUnknownOwner(t *testing.T) {
	a := turnStateOwnershipAccount(201, "")
	b := turnStateOwnershipAccount(202, "")
	svc := &OpenAIGatewayService{}
	c, _ := newTurnStateTestContext(t, 7, "sparse-session")
	upstream := http.Header{}
	upstream.Set(openAIWSTurnStateHeader, "sparse-state")
	svc.relayOpenAICodexTurnState(c, a, upstream)
	keyA := openAICodexTurnStateCredentialKey(c, a)
	keyB := openAICodexTurnStateCredentialKey(c, b)
	require.NotEmpty(t, keyA)
	require.NotEqual(t, keyA, keyB)
	svc.guardOpenAICodexTurnStateEcho(c, b, upstream)
	require.Empty(t, upstream.Get(openAIWSTurnStateHeader))
	store := NewOpenAIWSStateStore(nil)
	store.BindSessionTurnState(7, "sparse-session", keyA, "sparse-state", time.Minute)
	_, ok := store.GetSessionTurnState(7, "sparse-session", keyB)
	require.False(t, ok)
	store.BindSessionTurnState(7, "sparse-session", "", "unknown-owner-state", time.Minute)
	state, ok := store.GetSessionTurnState(7, "sparse-session", keyA)
	require.True(t, ok)
	require.Equal(t, "sparse-state", state)
	_, ok = store.GetSessionTurnState(7, "sparse-session", "")
	require.False(t, ok)
	require.Empty(t, openAICodexTurnStateCredentialKey(nil, nil))
	require.Empty(t, openAICodexTurnStateCredentialKey(nil, &Account{}))
}

func TestTurnStateOwnershipActualWSForwarding(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.OpenAIWS.Enabled = true
	cfg.Gateway.OpenAIWS.OAuthEnabled = true
	cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
	cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
	cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
	svc := &OpenAIGatewayService{cfg: cfg, cache: &stubGatewayCache{}, toolCorrector: NewCodexToolCorrector()}
	accountA := turnStateOwnershipAccount(101, "account-a")
	accountB := turnStateOwnershipAccount(102, "account-b")
	parentID := accountA.ID
	shadow := turnStateOwnershipAccount(103, "")
	shadow.ParentAccountID = &parentID
	svc.accountRepo = &codexAccountIdentityRepoStub{account: accountA}
	forward := func(account *Account, responseState string, events [][]byte) (http.Header, *gin.Context, error) {
		t.Helper()
		conn := &openAIWSCaptureConn{events: events}
		handshake := http.Header{}
		handshake.Set(openAIWSTurnStateHeader, responseState)
		dialer := &openAIWSCaptureDialer{conn: conn, handshake: handshake}
		pool := newOpenAIWSConnPool(cfg)
		pool.setClientDialerForTest(dialer)
		defer pool.Close()
		svc.openaiWSPool = pool
		c, _ := newTurnStateTestContext(t, 7, "session")
		_, err := svc.prepareCodexAccountIdentitySource(context.Background(), c, account)
		require.NoError(t, err)
		_, err = svc.forwardOpenAIWSV2(context.Background(), c, account,
			map[string]any{"model": "gpt-5.2", "input": []any{}, "stream": true, "store": false}, "session", "", "synthetic-token",
			OpenAIWSProtocolDecision{Transport: OpenAIUpstreamTransportResponsesWebsocketV2}, true, true, "gpt-5.2", "gpt-5.2", time.Now(), 0, "", nil)
		require.Equal(t, 1, dialer.DialCount())
		return dialer.lastHeaders, c, err
	}
	success := func() [][]byte {
		return [][]byte{[]byte(`{"type":"response.completed","response":{"id":"resp_ownership","model":"gpt-5.2","usage":{"input_tokens":1,"output_tokens":1}}}`)}
	}
	_, delivered, err := forward(accountA, "state-a", success())
	require.NoError(t, err)
	require.True(t, delivered.Writer.Written())
	require.Equal(t, "state-a", delivered.Writer.Header().Get(openAIWSTurnStateHeader))
	shadowHeaders, _, err := forward(shadow, "state-a", success())
	require.NoError(t, err)
	require.Equal(t, "state-a", shadowHeaders.Get(openAIWSTurnStateHeader), "parent and shadow share cached state")
	bHeaders, abandoned, err := forward(accountB, "state-b-abandoned", nil)
	require.Error(t, err)
	require.Empty(t, bHeaders.Get(openAIWSTurnStateHeader), "different credentials cannot load account A cache")
	require.False(t, abandoned.Writer.Written())
	require.Empty(t, abandoned.Writer.Header().Get(openAIWSTurnStateHeader), "abandoned handshake state must remain private")
	for _, tc := range []struct {
		account     *Account
		state, want string
	}{
		{accountA, "state-a", "state-a"},
		{accountB, "state-a", ""},
		{accountA, "state-b-abandoned", "state-b-abandoned"},
	} {
		c, _ := newTurnStateTestContext(t, 7, "session")
		c.Request.Header.Set(openAIWSTurnStateHeader, tc.state)
		req, err := svc.buildUpstreamRequest(context.Background(), c, tc.account, []byte(`{"model":"gpt-5.2"}`), "synthetic-token", true, "session", true)
		require.NoError(t, err)
		require.Equal(t, tc.want, req.Header.Get(openAIWSTurnStateHeader), "WS to HTTP handoff")
	}
}

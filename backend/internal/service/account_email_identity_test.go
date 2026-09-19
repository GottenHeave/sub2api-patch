package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func TestEmailIdentityClaudeUsesEmailInsteadOfRow(t *testing.T) {
	account := &Account{ID: 1, Platform: PlatformAnthropic, Type: AccountTypeOAuth, Extra: map[string]any{"email_address": "User@example.com", "account_uuid": "workspace"}}
	other := *account
	other.ID = 2
	body := []byte(`{"messages":[{"role":"user","content":"hello"}],"metadata":{"user_id":"{\"device_id\":\"client\",\"account_uuid\":\"workspace\",\"session_id\":\"7578cf37-aaca-46e4-a45c-71285d9dbb83\"}"}}`)
	identity := NewIdentityService(&identityCacheStub{})
	first, err := identity.RewriteUserIDWithMasking(context.Background(), body, account, "workspace", "client", "claude-cli/2.1.78")
	require.NoError(t, err)
	second, err := identity.RewriteUserIDWithMasking(context.Background(), body, &other, "workspace", "client", "claude-cli/2.1.78")
	require.NoError(t, err)
	require.Equal(t, string(first), string(second))
}

func TestEmailIdentityClaudeMissingEmailPreservesMetadata(t *testing.T) {
	account := &Account{ID: 1, Platform: PlatformAnthropic, Type: AccountTypeOAuth, Extra: map[string]any{"session_id_masking_enabled": true}}
	body := []byte(`{"metadata":{"user_id":"{\"device_id\":\"client\",\"account_uuid\":\"workspace\",\"session_id\":\"7578cf37-aaca-46e4-a45c-71285d9dbb83\"}"}}`)
	identity := NewIdentityService(&identityCacheStub{})
	got, err := identity.RewriteUserIDWithMasking(context.Background(), body, account, "workspace", "client", "claude-cli/2.1.78")
	require.NoError(t, err)
	require.Equal(t, string(body), string(got))
	gateway := &GatewayService{}
	require.Empty(t, gateway.buildOAuthMetadataUserID(&ParsedRequest{}, account, &Fingerprint{ClientID: "client"}))
	require.Empty(t, gateway.buildOAuthMetadataUserIDFromBody(context.Background(), account, &Fingerprint{ClientID: "client"}, []byte(`{"messages":[]}`)))
}

func TestEmailIdentityCompactMissingEmailStopsBeforeUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"access_token": "synthetic-token", "chatgpt_account_id": "workspace"}}
	upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(compactProbeSSESuccessBody))}}
	svc := &AccountTestService{httpUpstream: upstream}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/test", nil)
	err := svc.testOpenAICompactConnection(c, account, "gpt-5")
	require.Error(t, err)
	require.Nil(t, upstream.lastReq)
	require.Contains(t, recorder.Body.String(), "email")
}

func TestEmailIdentityNormalizationAndScope(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  string
	}{
		{"  User.Name+tag@EXAMPLE.COM\t", "User.Name+tag@example.com"},
		{"user@example.com", "user@example.com"},
		{"User@example.com", "User@example.com"},
	} {
		got, err := canonicalAccountEmail(tc.input)
		require.NoError(t, err)
		require.Equal(t, tc.want, got)
	}
	for _, raw := range []string{"", " ", "invalid", "@example.com", "user@", "Name <user@example.com>", "a@example.com,b@example.com", "a\r\nb@example.com"} {
		t.Run(raw, func(t *testing.T) {
			account := &Account{Platform: PlatformOpenAI, Credentials: map[string]any{"email": raw}}
			got, err := compactProbeSessionID(account)
			require.ErrorIs(t, err, errAccountEmailIdentityUnavailable)
			require.Empty(t, got)
		})
	}
	got, err := compactProbeSessionID(nil)
	require.Error(t, err)
	require.Empty(t, got)
	account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"email": "User.Name+tag@EXAMPLE.COM", "chatgpt_account_id": "workspace"}}
	seed := func(purpose string, parts ...string) string {
		value, seedErr := accountEmailIdentitySeed(account, purpose, parts...)
		require.NoError(t, seedErr)
		return value
	}
	first := seed("purpose", "client", "message")
	account.ID = 999
	account.Credentials["email"] = " User.Name+tag@example.com "
	require.Equal(t, first, seed("purpose", "client", "message"))
	for _, email := range []string{"user.Name+tag@example.com", "UserName+tag@example.com", "User.Name@example.com"} {
		account.Credentials["email"] = email
		require.NotEqual(t, first, seed("purpose", "client", "message"))
	}
	account.Credentials["email"] = "User.Name+tag@example.com"
	account.Credentials["chatgpt_account_id"] = "other-workspace"
	require.NotEqual(t, first, seed("purpose", "client", "message"))
	account.Credentials["chatgpt_account_id"] = "workspace"
	require.NotEqual(t, first, seed("other-purpose", "client", "message"))
	require.NotEqual(t, seed("purpose", "a::b", "c"), seed("purpose", "a", "b::c"))
	require.NotEqual(t, seed("purpose", "a", ""), seed("purpose", "a"))
}

func TestEmailIdentityClaudeSynthesisPaths(t *testing.T) {
	account := &Account{ID: 1, Platform: PlatformAnthropic, Type: AccountTypeOAuth, Extra: map[string]any{"email_address": "User@example.com", "account_uuid": "workspace", "org_uuid": "org"}}
	fp := &Fingerprint{ClientID: "client", UserAgent: "claude-cli/2.1.161"}
	body := []byte(`{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"hello"}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
	require.NoError(t, err)
	svc := &GatewayService{}
	firstParsed := svc.buildOAuthMetadataUserID(parsed, account, fp)
	firstRaw := svc.buildOAuthMetadataUserIDFromBody(context.Background(), account, fp, body)
	require.NotEmpty(t, firstParsed)
	require.NotEmpty(t, firstRaw)
	account.ID = 99
	require.Equal(t, firstParsed, svc.buildOAuthMetadataUserID(parsed, account, fp))
	require.Equal(t, firstRaw, svc.buildOAuthMetadataUserIDFromBody(context.Background(), account, fp, body))
	account.Extra["org_uuid"] = "other-org"
	require.NotEqual(t, firstRaw, svc.buildOAuthMetadataUserIDFromBody(context.Background(), account, fp, body))
	account.Extra["org_uuid"] = "org"
	for _, email := range []string{"", "not-an-email"} {
		account.Extra["email_address"] = email
		require.Empty(t, svc.buildOAuthMetadataUserID(parsed, account, fp))
		require.Empty(t, svc.buildOAuthMetadataUserIDFromBody(context.Background(), account, fp, body))
	}
	account.Extra = map[string]any{"email": "User@example.com", "account_uuid": "workspace", "org_uuid": "org"}
	require.Equal(t, firstRaw, svc.buildOAuthMetadataUserIDFromBody(context.Background(), account, fp, body))
	parsed.MetadataUserID = "client-original"
	require.Empty(t, svc.buildOAuthMetadataUserID(parsed, account, fp))
	require.Empty(t, svc.buildOAuthMetadataUserIDFromBody(context.Background(), account, fp, []byte(`{"metadata":{"user_id":"client-original"}}`)))
}

func TestEmailIdentityCompactUsesCredentialOwner(t *testing.T) {
	gin.SetMode(gin.TestMode)
	owner := Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"access_token": "synthetic-token", "chatgpt_account_id": "workspace"}, Extra: map[string]any{"email": "User@example.com"}}
	shadow := Account{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeOAuth, ParentAccountID: &owner.ID, Credentials: map[string]any{"email": "wrong@example.com"}, Extra: map[string]any{"email": "also-wrong@example.com", "email_address": "wrong-again@example.com"}}
	repo := &snapshotUpdateAccountRepo{stubOpenAIAccountRepo: stubOpenAIAccountRepo{accounts: []Account{owner}}}
	var first string
	for _, account := range []*Account{&owner, &shadow, &owner} {
		upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(compactProbeSSESuccessBody))}}
		svc := &AccountTestService{accountRepo: repo, httpUpstream: upstream}
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/test", nil)
		require.NoError(t, svc.testOpenAICompactConnection(c, account, "gpt-5"))
		got := upstream.lastReq.Header.Get("conversation_id")
		_, err := uuid.Parse(got)
		require.NoError(t, err)
		require.Equal(t, got, upstream.lastReq.Header.Get("session_id"))
		if first != "" {
			require.Equal(t, first, got)
		}
		first = got
		require.Equal(t, "compaction_trigger", gjson.GetBytes(upstream.lastBody, "input.1.type").String())
	}
}

func TestEmailIdentityStoredEmailSources(t *testing.T) {
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"email": "User@example.com", "chatgpt_account_id": "workspace"}}
	want, err := compactProbeSessionID(account)
	require.NoError(t, err)
	for _, tc := range []struct {
		name       string
		credential string
		extra      map[string]any
	}{
		{"credential wins", "User@example.com", map[string]any{"email": "other@example.com", "email_address": "third@example.com"}},
		{"CRS extra only", "", map[string]any{"email": "User@EXAMPLE.COM"}},
		{"extra email wins", "", map[string]any{"email": "User@example.com", "email_address": "other@example.com"}},
		{"extra address only", " ", map[string]any{"email_address": "User@example.com"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account.Credentials["email"] = tc.credential
			account.Extra = tc.extra
			got, identityErr := compactProbeSessionID(account)
			require.NoError(t, identityErr)
			require.Equal(t, want, got)
		})
	}
	account.Credentials["email"] = "invalid"
	account.Extra = map[string]any{"email": "User@example.com"}
	got, err := compactProbeSessionID(account)
	require.ErrorIs(t, err, errAccountEmailIdentityUnavailable)
	require.Empty(t, got)
}

func TestEmailIdentitySynthesisThenRewriteIsRowIndependent(t *testing.T) {
	account := &Account{ID: 1, Platform: PlatformAnthropic, Type: AccountTypeOAuth, Extra: map[string]any{
		"email_address": "User@example.com", "account_uuid": "workspace", "org_uuid": "org",
	}}
	body := []byte(`{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"hello"}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
	require.NoError(t, err)
	gateway := &GatewayService{}
	identity := NewIdentityService(&identityCacheStub{})
	fp := &Fingerprint{ClientID: "synthetic-client", UserAgent: "claude-cli/2.1.161"}
	for _, mode := range []string{"parsed", "raw"} {
		t.Run(mode, func(t *testing.T) {
			var first string
			for _, row := range []int64{1, 999} {
				account.ID = row
				metadata := gateway.buildOAuthMetadataUserID(parsed, account, fp)
				if mode == "raw" {
					metadata = gateway.buildOAuthMetadataUserIDFromBody(context.Background(), account, fp, body)
				}
				require.NotEmpty(t, metadata)
				request, setErr := sjson.SetBytes(body, "metadata.user_id", metadata)
				require.NoError(t, setErr)
				out, rewriteErr := identity.RewriteUserIDWithMasking(context.Background(), request, account, "workspace", fp.ClientID, fp.UserAgent)
				require.NoError(t, rewriteErr)
				user := ParseMetadataUserID(gjson.GetBytes(out, "metadata.user_id").String())
				require.NotNil(t, user)
				require.NotEmpty(t, user.SessionID)
				if first != "" {
					require.Equal(t, first, user.SessionID)
				}
				first = user.SessionID
			}
		})
	}
}

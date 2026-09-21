package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestCodexAPIModelsDoNotFallBackToLocalCatalog(t *testing.T) {
	svc := &availableModelsAdminService{
		stubAdminService: newStubAdminService(),
		account:          service.Account{ID: 1, Platform: service.PlatformOpenAI, Type: service.AccountTypeCodexAPI},
	}
	recorder := httptest.NewRecorder()
	setupAvailableModelsRouter(svc).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts/1/models", nil))
	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
}

func TestCodexAPIImportRequiresOpenAIAndCredentials(t *testing.T) {
	item := DataAccount{Name: "dedicated", Platform: service.PlatformOpenAI, Type: service.AccountTypeCodexAPI,
		Credentials: map[string]any{"base_url": "https://gateway.example", "api_key": "test-key"}}
	require.NoError(t, validateDataAccount(item))
	item.Platform = service.PlatformAnthropic
	require.Error(t, validateDataAccount(item))
	item.Platform = service.PlatformOpenAI
	delete(item.Credentials, "api_key")
	require.Error(t, validateDataAccount(item))
}

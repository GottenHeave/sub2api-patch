package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type codexAPIConnectionTestRepository struct {
	AccountRepository
	account *Account
}

func (r codexAPIConnectionTestRepository) GetByID(context.Context, int64) (*Account, error) {
	return r.account, nil
}

func TestCodexAPIAccountTestUsesUpstreamModelAndResponses(t *testing.T) {
	model := "upstream-only-model"
	var forwardedModel string
	gateway := newCodexModelsAPIKeyTestService(&codexModelsHTTPUpstreamStub{do: func(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
		if req.URL.Path == "/v1/models" {
			body, err := json.Marshal(map[string]any{"data": []map[string]string{{"id": model}}})
			require.NoError(t, err)
			return ordinaryModelsUpstreamResponse(string(body)), nil
		}
		require.Equal(t, "/v1/responses", req.URL.Path)
		var payload struct {
			Model string `json:"model"`
		}
		require.NoError(t, json.NewDecoder(req.Body).Decode(&payload))
		forwardedModel = payload.Model
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"))}, nil
	}})
	account := newCodexModelsAPIKeyTestAccount("https://models.example")
	account.Type = AccountTypeCodexAPI
	account.Credentials["model_mapping"] = map[string]any{model: "must-not-map"}
	svc := &AccountTestService{openaiGatewayService: gateway, accountRepo: codexAPIConnectionTestRepository{account: account}}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/test", nil)
	require.NoError(t, svc.TestAccountConnection(c, account.ID, "", "hello", ""))
	require.Equal(t, model, forwardedModel)
	require.Contains(t, recorder.Body.String(), `"success":true`)
}

func TestCodexAPIAccountModelsUseOnlyUpstream(t *testing.T) {
	for _, body := range []string{`{"data":[]}`, `{"data":[{"id":"upstream-future-model","display_name":"Upstream name"}]}`} {
		t.Run(body, func(t *testing.T) {
			gateway := newCodexModelsAPIKeyTestService(&codexModelsHTTPUpstreamStub{do: func(_ *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
				return ordinaryModelsUpstreamResponse(body), nil
			}})
			account := newCodexModelsAPIKeyTestAccount("https://models.example")
			account.Type = AccountTypeCodexAPI
			models, err := (&AccountTestService{openaiGatewayService: gateway}).FetchOpenAIAccountModels(context.Background(), account)
			require.NoError(t, err)
			if body == `{"data":[]}` {
				require.Empty(t, models)
			} else {
				require.Len(t, models, 1)
				require.Equal(t, "upstream-future-model", models[0].ID)
				require.Equal(t, "Upstream name", models[0].DisplayName)
			}
		})
	}
}

func TestCodexAPIAccountModelFailureHasNoFallback(t *testing.T) {
	gateway := newCodexModelsAPIKeyTestService(&codexModelsHTTPUpstreamStub{do: func(_ *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
		response := ordinaryModelsUpstreamResponse(`{"error":"unavailable"}`)
		response.StatusCode = http.StatusBadGateway
		return response, nil
	}})
	account := newCodexModelsAPIKeyTestAccount("https://models.example")
	account.Type = AccountTypeCodexAPI
	models, err := (&AccountTestService{openaiGatewayService: gateway}).FetchOpenAIAccountModels(context.Background(), account)
	require.Error(t, err)
	require.Empty(t, models)
}

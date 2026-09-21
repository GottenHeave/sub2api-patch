package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/gin-gonic/gin"
)

func (s *AccountTestService) fetchCodexAPIAccountModels(ctx context.Context, account *Account) ([]openai.Model, error) {
	if s == nil || s.openaiGatewayService == nil {
		return nil, errors.New("codex-api model discovery service is unavailable")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost/v1/models", nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.openaiGatewayService.RoundTripCodexAPI(ctx, account, req, "/v1/models")
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("codex-api model discovery returned HTTP %d", resp.StatusCode)
	}
	var payload struct {
		Data []openai.Model `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode Codex API models: %w", err)
	}
	return payload.Data, nil
}

func (s *AccountTestService) testCodexAPIAccountConnection(c *gin.Context, account *Account, model, prompt, mode string) error {
	if s.openaiGatewayService == nil {
		return s.sendErrorAndEnd(c, "Codex API transport is unavailable")
	}
	if normalizeAccountTestMode(mode) != AccountTestModeDefault {
		return s.sendErrorAndEnd(c, "Codex API account tests support Responses only")
	}
	if strings.TrimSpace(model) == "" {
		models, err := s.fetchCodexAPIAccountModels(c.Request.Context(), account)
		if err != nil {
			return s.sendErrorAndEnd(c, err.Error())
		}
		for _, candidate := range models {
			if candidate.ID != "" {
				model = candidate.ID
				break
			}
		}
		if model == "" {
			return s.sendErrorAndEnd(c, "Codex API returned no models")
		}
	}
	if prompt == "" {
		prompt = "hi"
	}
	body, err := json.Marshal(map[string]any{"model": model, "input": prompt, "stream": true})
	if err != nil {
		return s.sendErrorAndEnd(c, err.Error())
	}
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, "http://localhost/v1/responses", bytes.NewReader(body))
	if err != nil {
		return s.sendErrorAndEnd(c, err.Error())
	}
	req.Header.Set("Content-Type", "application/json")
	s.sendEvent(c, TestEvent{Type: "test_start", Model: model})
	resp, err := s.openaiGatewayService.RoundTripCodexAPI(c.Request.Context(), account, req, "/v1/responses")
	if err != nil {
		return s.sendErrorAndEnd(c, err.Error())
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return s.sendErrorAndEnd(c, fmt.Sprintf("Codex API returned HTTP %d", resp.StatusCode))
	}
	return s.processOpenAIStream(c, resp.Body)
}

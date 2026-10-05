package service

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/tidwall/gjson"
)

func codexAPIResourceKey(apiKeyID int64, resource string) string {
	return fmt.Sprintf("codex-api-resource:%d:%x", apiKeyID, sha256.Sum256([]byte(resource)))
}

func (s *OpenAIGatewayService) BindCodexAPIResource(ctx context.Context, key *APIKey, resource string, accountID int64) error {
	if resource == "" {
		return nil
	}
	if s.cache == nil {
		return fmt.Errorf("codex API resource cache unavailable")
	}
	return s.cache.SetSessionAccountID(ctx, derefGroupID(key.GroupID), codexAPIResourceKey(key.ID, resource), accountID, 30*24*time.Hour)
}

// File and call references cannot fail over to another subscription identity.
func (s *OpenAIGatewayService) CodexAPIResourceAccount(ctx context.Context, key *APIKey, body []byte, callID string) (int64, error) {
	resources := codexAPIReferencedResources(gjson.ParseBytes(body))
	if callID != "" {
		resources = append(resources, callID)
	}
	var accountID int64
	for _, resource := range resources {
		if s.cache == nil {
			return 0, fmt.Errorf("codex API resource cache unavailable")
		}
		id, err := s.cache.GetSessionAccountID(ctx, derefGroupID(key.GroupID), codexAPIResourceKey(key.ID, resource))
		if err != nil || id <= 0 {
			if err == nil || errors.Is(err, ErrStickySessionNotFound) {
				return 0, fmt.Errorf("codex API resource not owned by this key")
			}
			return 0, err
		}
		if accountID != 0 && accountID != id {
			return 0, fmt.Errorf("codex API resources belong to different accounts")
		}
		accountID = id
	}
	return accountID, nil
}

func codexAPIReferencedResources(root gjson.Result) []string {
	var resources []string
	if !root.IsObject() && !root.IsArray() {
		return resources
	}
	root.ForEach(func(key, value gjson.Result) bool {
		if value.Type == gjson.String && (key.String() == "file_id" || key.String() == "previous_response_id") && value.String() != "" {
			resources = append(resources, value.String())
		} else if value.IsObject() || value.IsArray() {
			resources = append(resources, codexAPIReferencedResources(value)...)
		}
		return true
	})
	return resources
}

package service

import (
	"context"
	"net/url"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

func (a *Account) IsCodexAPI() bool {
	return a != nil && a.Platform == PlatformOpenAI && a.Type == AccountTypeCodexAPI
}

// Grouped creation must roll back the account when membership is rejected.
func createAccountWithGroups(ctx context.Context, repo AccountRepository, account *Account, ids []int64) error {
	if len(ids) > 0 {
		if atomic, ok := repo.(AccountDuplicateRepository); ok {
			groups := make([]AccountGroup, len(ids))
			for i, id := range ids {
				groups[i] = AccountGroup{GroupID: id, Priority: i + 1}
			}
			return atomic.CreateWithAccountGroups(ctx, account, groups)
		}
	}
	if err := repo.Create(ctx, account); err != nil {
		return err
	}
	if len(ids) > 0 {
		return repo.BindGroups(ctx, account.ID, ids)
	}
	return nil
}

func ValidateCodexAPIAccount(platform, accountType string, credentials map[string]any) error {
	if accountType != AccountTypeCodexAPI {
		return nil
	}
	if platform != PlatformOpenAI {
		return infraerrors.BadRequest("CODEX_API_PLATFORM", "codex-api accounts require the OpenAI platform")
	}
	for _, key := range []string{"base_url", "api_key"} {
		value, _ := credentials[key].(string)
		if strings.TrimSpace(value) == "" {
			return infraerrors.BadRequest("CODEX_API_CREDENTIALS", "codex-api accounts require base_url and api_key")
		}
	}
	rawURL, _ := credentials["base_url"].(string)
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return infraerrors.BadRequest("CODEX_API_BASE_URL", "codex-api base_url must be an HTTP(S) deployment URL without userinfo, query or fragment")
	}
	return nil
}

// ValidateCodexAPIGroupMembership includes inactive accounts: re-enabling an
// account must not change whether a group uses the dedicated forwarding path.
func ValidateCodexAPIGroupMembership(group *Group, accounts []Account) error {
	hasCodex, hasOther := false, false
	for i := range accounts {
		if accounts[i].Type == AccountTypeCodexAPI {
			if accounts[i].Platform != PlatformOpenAI {
				return infraerrors.BadRequest("CODEX_API_PLATFORM", "codex-api accounts require the OpenAI platform")
			}
			hasCodex = true
		} else {
			hasOther = true
		}
	}
	if hasCodex && (hasOther || group.Platform != PlatformOpenAI || group.RequireOAuthOnly) {
		return infraerrors.BadRequest("CODEX_API_GROUP_ISOLATION", "codex-api accounts require a dedicated OpenAI group without OAuth-only restrictions")
	}
	return nil
}

func validateCodexAPIAccountGroups(ctx context.Context, accounts AccountRepository, groups GroupRepository, account *Account, groupIDs []int64) error {
	for _, id := range groupIDs {
		group, err := groups.GetByID(ctx, id)
		if err != nil {
			return err
		}
		members, err := accounts.ListAllWithFilters(ctx, "", "", "", "", id, "")
		if err != nil {
			return err
		}
		prospective := make([]Account, 0, len(members)+1)
		for _, member := range members {
			if member.ID != account.ID {
				prospective = append(prospective, member)
			}
		}
		prospective = append(prospective, *account)
		if err := ValidateCodexAPIGroupMembership(group, prospective); err != nil {
			return err
		}
	}
	return nil
}

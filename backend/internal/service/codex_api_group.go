package service

import (
	"context"
	"fmt"
	"sort"
)

// CodexAPIGroupAccounts inspects all members, including disabled accounts, so
// disabling a dedicated account cannot switch the group's protocol behavior.
func (s *OpenAIGatewayService) CodexAPIGroupAccounts(ctx context.Context, groupID int64) ([]Account, bool, error) {
	members, err := s.accountRepo.ListAllWithFilters(ctx, "", "", "", "", groupID, "")
	if err != nil {
		return nil, false, err
	}
	dedicated := false
	for i := range members {
		dedicated = dedicated || members[i].IsCodexAPI()
	}
	if !dedicated {
		return nil, false, nil
	}
	for i := range members {
		if !members[i].IsCodexAPI() {
			return nil, true, fmt.Errorf("codex API group contains other account types")
		}
	}
	priority := func(account Account) int {
		for _, membership := range account.AccountGroups {
			if membership.GroupID == groupID {
				return membership.Priority
			}
		}
		return 0
	}
	sort.SliceStable(members, func(i, j int) bool {
		a, b := priority(members[i]), priority(members[j])
		if a != b {
			return a < b
		}
		return members[i].Priority < members[j].Priority
	})
	return members, true, nil
}

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type codexAPIGroupRepo struct {
	AccountRepository
	members []Account
}

func (r codexAPIGroupRepo) ListAllWithFilters(_ context.Context, _, _, status, _ string, _ int64, _ string) ([]Account, error) {
	var members []Account
	for _, member := range r.members {
		if status == "" || member.Status == status {
			members = append(members, member)
		}
	}
	return members, nil
}

func TestCodexAPIGroupAccounts(t *testing.T) {
	dedicated := Account{Platform: PlatformOpenAI, Type: AccountTypeCodexAPI, Status: "disabled"}
	ordinary := Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	for _, test := range []struct {
		name               string
		members            []Account
		dedicated, invalid bool
	}{
		{"ordinary", []Account{ordinary}, false, false},
		{"empty", nil, false, false},
		{"disabled dedicated", []Account{dedicated}, true, false},
		{"mixed", []Account{dedicated, ordinary}, true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := &OpenAIGatewayService{accountRepo: codexAPIGroupRepo{members: test.members}}
			_, mode, err := s.CodexAPIGroupAccounts(context.Background(), 1)
			require.Equal(t, test.dedicated, mode)
			if test.invalid {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestCodexAPIGroupUsesMembershipPriority(t *testing.T) {
	first := Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeCodexAPI, Priority: 1, AccountGroups: []AccountGroup{{GroupID: 7, Priority: 10}}}
	second := Account{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeCodexAPI, Priority: 10, AccountGroups: []AccountGroup{{GroupID: 7, Priority: 1}}}
	s := &OpenAIGatewayService{accountRepo: codexAPIGroupRepo{members: []Account{first, second}}}
	members, _, err := s.CodexAPIGroupAccounts(context.Background(), 7)
	require.NoError(t, err)
	require.Equal(t, second.ID, members[0].ID)
}

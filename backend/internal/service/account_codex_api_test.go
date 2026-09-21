package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type codexAPIAtomicCreateRepository struct {
	AccountRepository
	groups []AccountGroup
	err    error
}

type codexAPIGroupMembersRepository struct {
	AccountRepository
	members []Account
}

func (r *codexAPIGroupMembersRepository) ListAllWithFilters(context.Context, string, string, string, string, int64, string) ([]Account, error) {
	return r.members, nil
}

type codexAPIGroupRepository struct {
	GroupRepository
	group *Group
}

func (r *codexAPIGroupRepository) GetByID(context.Context, int64) (*Group, error) {
	return r.group, nil
}

func TestCodexAPIGroupPreflightIncludesDisabledMembers(t *testing.T) {
	accounts := &codexAPIGroupMembersRepository{members: []Account{{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusDisabled}}}
	groups := &codexAPIGroupRepository{group: &Group{ID: 1, Platform: PlatformOpenAI}}
	err := validateCodexAPIAccountGroups(context.Background(), accounts, groups, &Account{Platform: PlatformOpenAI, Type: AccountTypeCodexAPI}, []int64{1})
	require.Error(t, err)
}

func (r *codexAPIAtomicCreateRepository) CreateWithAccountGroups(_ context.Context, _ *Account, groups []AccountGroup) error {
	r.groups = groups
	return r.err
}

func TestCodexAPIGroupedCreateUsesAtomicRepository(t *testing.T) {
	repo := &codexAPIAtomicCreateRepository{}
	ids := []int64{7, 19}
	require.NoError(t, createAccountWithGroups(context.Background(), repo, &Account{}, ids))
	require.Equal(t, []AccountGroup{{GroupID: ids[0], Priority: 1}, {GroupID: ids[1], Priority: 2}}, repo.groups)
	repo.err = errors.New("membership rejected")
	require.ErrorIs(t, createAccountWithGroups(context.Background(), repo, &Account{}, ids), repo.err)
}

func TestCodexAPIAccountValidation(t *testing.T) {
	credentials := map[string]any{"base_url": "https://gateway.example", "api_key": "test-key"}
	require.NoError(t, ValidateCodexAPIAccount(PlatformOpenAI, AccountTypeCodexAPI, credentials))
	require.Error(t, ValidateCodexAPIAccount(PlatformAnthropic, AccountTypeCodexAPI, credentials))
	require.Error(t, ValidateCodexAPIAccount(PlatformOpenAI, AccountTypeCodexAPI, nil))
	require.NoError(t, ValidateCodexAPIAccount(PlatformAnthropic, AccountTypeAPIKey, nil))
	require.True(t, canDuplicateAccountType(AccountTypeCodexAPI))
}

func TestCodexAPIGroupIsolation(t *testing.T) {
	group := &Group{Platform: PlatformOpenAI}
	codex := Account{Platform: PlatformOpenAI, Type: AccountTypeCodexAPI, Status: "inactive"}
	require.NoError(t, ValidateCodexAPIGroupMembership(group, []Account{codex}))
	for _, accountType := range []string{AccountTypeOAuth, AccountTypeAPIKey} {
		other := Account{Platform: PlatformOpenAI, Type: accountType}
		require.Error(t, ValidateCodexAPIGroupMembership(group, []Account{other, codex}))
		require.Error(t, ValidateCodexAPIGroupMembership(group, []Account{codex, other}))
	}
	group.RequireOAuthOnly = true
	require.Error(t, ValidateCodexAPIGroupMembership(group, []Account{codex}))
	group.RequireOAuthOnly = false
	group.Platform = PlatformAnthropic
	require.Error(t, ValidateCodexAPIGroupMembership(group, []Account{codex}))
}

func TestCodexAPIExcludedFromGenericScheduling(t *testing.T) {
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeCodexAPI}
	scheduler := &defaultOpenAIAccountScheduler{}
	allowed, _ := scheduler.isAccountRequestCompatibleReason(context.Background(), account, OpenAIAccountScheduleRequest{})
	require.False(t, allowed)
	require.NotEmpty(t, openAICompatibleAccountEligibilityFailureReasonBeforeProfit(context.Background(), account, PlatformOpenAI, "", false, ""))
	require.False(t, account.IsOpenAIApiKey())
}

//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestCodexAPIConcurrentGroupAssignmentsStayIsolated(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	groups := newGroupRepositoryWithSQL(integrationEntClient, integrationDB)
	accounts := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, nil)
	group := &service.Group{Name: "codex-concurrent-assignment", Platform: service.PlatformOpenAI, Status: service.StatusActive, SubscriptionType: service.SubscriptionTypeStandard, RateMultiplier: 1}
	require.NoError(t, groups.Create(ctx, group))
	var ids []int64
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM account_groups WHERE group_id = $1", group.ID)
		for _, id := range ids {
			_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM accounts WHERE id = $1", id)
		}
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM groups WHERE id = $1", group.ID)
	})
	for _, kind := range []string{service.AccountTypeCodexAPI, service.AccountTypeAPIKey} {
		account := &service.Account{Name: kind, Platform: service.PlatformOpenAI, Type: kind, Status: service.StatusActive, Credentials: map[string]any{"base_url": "https://gateway.example", "api_key": "test-key"}}
		require.NoError(t, accounts.Create(ctx, account))
		ids = append(ids, account.ID)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	go func() { <-start; results <- accounts.BindGroups(ctx, ids[0], []int64{group.ID}) }()
	go func() { <-start; results <- groups.BindAccountsToGroup(ctx, group.ID, []int64{ids[1]}) }()
	close(start)
	first, second := <-results, <-results
	require.True(t, (first == nil) != (second == nil), "exactly one incompatible assignment should succeed")
	members, err := accounts.ListByGroup(ctx, group.ID)
	require.NoError(t, err)
	require.Len(t, members, 1)
}

func TestCodexAPIRejectedGroupedCreateRollsBackAccount(t *testing.T) {
	ctx := context.Background()
	groups := newGroupRepositoryWithSQL(integrationEntClient, integrationDB)
	accounts := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, nil)
	group := &service.Group{Name: "codex-create-rollback", Platform: service.PlatformOpenAI, Status: service.StatusActive, SubscriptionType: service.SubscriptionTypeStandard, RateMultiplier: 1}
	require.NoError(t, groups.Create(ctx, group))
	name := fmt.Sprintf("rejected-for-group-%d", group.ID)
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM account_groups WHERE group_id = $1", group.ID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM accounts WHERE name = $1", name)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM groups WHERE id = $1", group.ID)
	})
	member := &service.Account{Name: name, Platform: service.PlatformOpenAI, Type: service.AccountTypeCodexAPI, Status: service.StatusActive, Credentials: map[string]any{"base_url": "https://gateway.example", "api_key": "test-key"}}
	require.NoError(t, accounts.CreateWithAccountGroups(ctx, member, []service.AccountGroup{{GroupID: group.ID, Priority: 1}}))
	rejected := &service.Account{Name: name, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Status: service.StatusActive}
	require.Error(t, accounts.CreateWithAccountGroups(ctx, rejected, []service.AccountGroup{{GroupID: group.ID, Priority: 1}}))
	var count int
	require.NoError(t, scanSingleRow(ctx, integrationDB, "SELECT COUNT(*) FROM accounts WHERE name = $1", []any{name}, &count))
	require.Equal(t, 1, count)
}

func TestCodexAPIGroupMutationIsolation(t *testing.T) {
	for _, firstType := range []string{service.AccountTypeCodexAPI, service.AccountTypeAPIKey} {
		for _, operation := range []string{"bind-account", "bind-group", "add", "duplicate"} {
			t.Run(firstType+"/"+operation, func(t *testing.T) {
				ctx := context.Background()
				tx := testEntTx(t)
				groups := newGroupRepositoryWithSQL(tx.Client(), tx)
				accounts := newAccountRepositoryWithSQL(tx.Client(), tx, nil)
				group := &service.Group{Name: "dedicated-test", Platform: service.PlatformOpenAI, Status: service.StatusActive, SubscriptionType: service.SubscriptionTypeStandard, RateMultiplier: 1}
				require.NoError(t, groups.Create(ctx, group))
				create := func(kind string) *service.Account {
					account := &service.Account{Name: kind, Platform: service.PlatformOpenAI, Type: kind, Status: "inactive", Credentials: map[string]any{"base_url": "https://gateway.example", "api_key": "test-key"}}
					require.NoError(t, accounts.Create(ctx, account))
					return account
				}
				first := create(firstType)
				otherType := service.AccountTypeCodexAPI
				if firstType == otherType {
					otherType = service.AccountTypeAPIKey
				}
				other := create(otherType)
				require.NoError(t, accounts.BindGroups(ctx, first.ID, []int64{group.ID}))
				var err error
				switch operation {
				case "bind-account":
					err = accounts.BindGroups(ctx, other.ID, []int64{group.ID})
				case "bind-group":
					err = groups.BindAccountsToGroup(ctx, group.ID, []int64{other.ID})
				case "add":
					err = accounts.AddToGroup(ctx, other.ID, group.ID, 1)
				case "duplicate":
					clone := *other
					clone.ID = 0
					err = accounts.CreateWithAccountGroups(ctx, &clone, []service.AccountGroup{{GroupID: group.ID, Priority: 1}})
				}
				require.Error(t, err)
				members, err := accounts.ListAllWithFilters(ctx, "", "", "", "", group.ID, "")
				require.NoError(t, err)
				require.Len(t, members, 1)
				require.Equal(t, first.ID, members[0].ID)
			})
		}
	}
}

func TestCodexAPIGroupDirectoryIncludesDisabledAndHydratesBindings(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	repo := newAccountRepositoryWithSQL(tx.Client(), tx, nil)
	proxy := mustCreateProxy(t, tx.Client(), &service.Proxy{Name: "dedicated-proxy"})
	group := mustCreateGroup(t, tx.Client(), &service.Group{Name: "dedicated-directory", Platform: service.PlatformOpenAI})
	account := &service.Account{Name: "disabled-codex", Platform: service.PlatformOpenAI, Type: service.AccountTypeCodexAPI, Status: service.StatusDisabled, ProxyID: &proxy.ID, Credentials: map[string]any{"base_url": "https://gateway.example", "api_key": "test-key"}}
	priority := 17
	require.NoError(t, repo.CreateWithAccountGroups(ctx, account, []service.AccountGroup{{GroupID: group.ID, Priority: priority}}))
	members, err := repo.ListAllWithFilters(ctx, "", "", "", "", group.ID, "")
	require.NoError(t, err)
	require.Len(t, members, 1)
	member := members[0]
	require.Equal(t, account.ID, member.ID)
	require.Equal(t, service.StatusDisabled, member.Status)
	require.NotNil(t, member.Proxy)
	require.Equal(t, proxy.ID, member.Proxy.ID)
	require.Len(t, member.AccountGroups, 1)
	require.Equal(t, group.ID, member.AccountGroups[0].GroupID)
	require.Equal(t, priority, member.AccountGroups[0].Priority)
}

func TestCodexAPIGroupAndAccountEdits(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	groups := newGroupRepositoryWithSQL(tx.Client(), tx)
	accounts := newAccountRepositoryWithSQL(tx.Client(), tx, nil)
	group := &service.Group{Name: "dedicated-edit", Platform: service.PlatformOpenAI, Status: service.StatusActive, SubscriptionType: service.SubscriptionTypeStandard, RateMultiplier: 1}
	require.NoError(t, groups.Create(ctx, group))
	var members []*service.Account
	for range 2 {
		account := &service.Account{Name: "codex", Platform: service.PlatformOpenAI, Type: service.AccountTypeCodexAPI, Status: service.StatusActive, Credentials: map[string]any{"base_url": "https://gateway.example", "api_key": "test-key"}}
		require.NoError(t, accounts.Create(ctx, account))
		require.NoError(t, accounts.BindGroups(ctx, account.ID, []int64{group.ID}))
		members = append(members, account)
	}
	members[0].Type = service.AccountTypeAPIKey
	require.Error(t, accounts.Update(ctx, members[0]))
	members[0].Type = service.AccountTypeCodexAPI
	members[0].Platform = service.PlatformAnthropic
	require.Error(t, accounts.Update(ctx, members[0]))
	group.RequireOAuthOnly = true
	require.Error(t, groups.Update(ctx, group))
	group.RequireOAuthOnly = false
	group.Platform = service.PlatformAnthropic
	require.Error(t, groups.Update(ctx, group))
	copy := &service.Group{Name: "invalid-copy", Platform: service.PlatformOpenAI, RequireOAuthOnly: true, Status: service.StatusActive, SubscriptionType: service.SubscriptionTypeStandard, RateMultiplier: 1}
	require.Error(t, groups.CreateFromSource(ctx, copy, group.ID))
}

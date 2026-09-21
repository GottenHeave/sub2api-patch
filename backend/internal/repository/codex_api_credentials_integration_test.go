//go:build integration

package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestCodexAPICredentialWritesPreserveValidAccounts(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	repo := newAccountRepositoryWithSQL(tx.Client(), tx, nil)
	valid := map[string]any{"base_url": "https://gateway.example/prefix", "api_key": "test-key"}
	var ids []int64
	for _, kind := range []string{service.AccountTypeAPIKey, service.AccountTypeCodexAPI} {
		account := &service.Account{Name: kind, Platform: service.PlatformOpenAI, Type: kind, Credentials: valid, Status: service.StatusActive}
		require.NoError(t, repo.Create(ctx, account))
		ids = append(ids, account.ID)
	}
	for _, patch := range []map[string]any{{"api_key": nil}, {"base_url": "invalid"}} {
		_, err := repo.BulkUpdate(ctx, ids, service.AccountBulkUpdate{Credentials: patch})
		require.Error(t, err)
		for _, id := range ids {
			account, err := repo.GetByID(ctx, id)
			require.NoError(t, err)
			require.Equal(t, valid, account.Credentials)
		}
		require.Error(t, repo.UpdateCredentials(ctx, ids[1], patch))
		account, err := repo.GetByID(ctx, ids[1])
		require.NoError(t, err)
		require.Equal(t, valid, account.Credentials)
	}
	_, err := repo.BulkUpdate(ctx, ids, service.AccountBulkUpdate{Credentials: map[string]any{"custom": "retained"}})
	require.NoError(t, err)
	for _, id := range ids {
		account, err := repo.GetByID(ctx, id)
		require.NoError(t, err)
		require.Equal(t, "retained", account.Credentials["custom"])
		require.Equal(t, valid["api_key"], account.Credentials["api_key"])
	}
	_, err = repo.BulkUpdate(ctx, ids[:1], service.AccountBulkUpdate{Credentials: map[string]any{"api_key": nil}})
	require.NoError(t, err)
	ordinary, err := repo.GetByID(ctx, ids[0])
	require.NoError(t, err)
	require.Nil(t, ordinary.Credentials["api_key"])
}

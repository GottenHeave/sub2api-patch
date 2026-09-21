package repository

import (
	"context"
	"database/sql"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func expectOrdinaryCredentialAccount(mock sqlmock.Sqlmock) {
	mock.ExpectQuery("SELECT platform, type, credentials").WillReturnRows(
		sqlmock.NewRows([]string{"platform", "type", "credentials"}).AddRow(service.PlatformOpenAI, service.AccountTypeAPIKey, `{}`))
}

type credentialRecordingSQL struct {
	*recordingSQLExecutor
	db *sql.DB
}

func (e credentialRecordingSQL) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return e.db.QueryContext(ctx, query, args...)
}

func TestCodexAPICredentialGuardValidatesEffectiveDocument(t *testing.T) {
	for _, merge := range []bool{true, false} {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })
		for _, patch := range []map[string]any{{"api_key": nil}, {"base_url": "invalid"}, {"extra_field": true}} {
			mock.ExpectQuery("SELECT platform, type, credentials").WillReturnRows(sqlmock.NewRows([]string{"platform", "type", "credentials"}).AddRow(service.PlatformOpenAI, service.AccountTypeCodexAPI, `{"api_key":"key","base_url":"https://gateway.example"}`))
			err := validateCodexAPICredentialWrite(context.Background(), db, []int64{1}, patch, merge)
			if _, extraOnly := patch["extra_field"]; extraOnly && merge {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		}
		require.NoError(t, mock.ExpectationsWereMet())
	}
}

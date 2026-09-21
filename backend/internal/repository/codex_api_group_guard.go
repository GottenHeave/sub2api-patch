package repository

import (
	"context"
	"encoding/json"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

// Account locks precede group locks so changing an account type cannot race
// membership assignment. Group locks serialize prospective membership checks.
func lockCodexAPIAccounts(ctx context.Context, exec sqlExecutor, ids []int64) error {
	rows, err := exec.QueryContext(ctx, `SELECT id FROM accounts WHERE id = ANY($1) AND deleted_at IS NULL ORDER BY id FOR NO KEY UPDATE`, pq.Array(ids))
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
	}
	return rows.Err()
}

func validateCodexAPIGroup(ctx context.Context, exec sqlExecutor, groupID int64, replacement *service.Account, additions []int64, proposed *service.Group) error {
	group := proposed
	if group == nil {
		group = &service.Group{ID: groupID}
		if err := scanSingleRow(ctx, exec, `SELECT platform, require_oauth_only FROM groups WHERE id = $1 AND deleted_at IS NULL`, []any{groupID}, &group.Platform, &group.RequireOAuthOnly); err != nil {
			return err
		}
	}
	rows, err := exec.QueryContext(ctx, `SELECT a.id, a.platform, a.type FROM accounts a WHERE a.deleted_at IS NULL AND (a.id = ANY($2) OR EXISTS (SELECT 1 FROM account_groups ag WHERE ag.account_id = a.id AND ag.group_id = $1))`, groupID, pq.Array(additions))
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	var members []service.Account
	for rows.Next() {
		var account service.Account
		if err := rows.Scan(&account.ID, &account.Platform, &account.Type); err != nil {
			return err
		}
		if replacement != nil && account.ID == replacement.ID {
			account = *replacement
		}
		members = append(members, account)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return service.ValidateCodexAPIGroupMembership(group, members)
}

func validateCodexAPIAccountUpdate(ctx context.Context, exec sqlExecutor, account *service.Account) error {
	if err := service.ValidateCodexAPIAccount(account.Platform, account.Type, account.Credentials); err != nil {
		return err
	}
	rows, err := exec.QueryContext(ctx, `SELECT ag.group_id FROM account_groups ag JOIN accounts a ON a.id = ag.account_id WHERE a.id = $1 AND (a.type = $2 OR $3 = $2) ORDER BY ag.group_id`, account.ID, service.AccountTypeCodexAPI, account.Type)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	if err := lockLiveGroups(ctx, exec, ids); err != nil {
		return err
	}
	for _, id := range ids {
		if err := validateCodexAPIGroup(ctx, exec, id, account, nil, nil); err != nil {
			return err
		}
	}
	return nil
}

func validateCodexAPICredentialWrite(ctx context.Context, exec sqlExecutor, ids []int64, credentials map[string]any, merge bool) error {
	rows, err := exec.QueryContext(ctx, `SELECT platform, type, credentials FROM accounts WHERE id = ANY($1) AND deleted_at IS NULL ORDER BY id FOR NO KEY UPDATE`, pq.Array(ids))
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var platform, kind string
		var stored []byte
		if err := rows.Scan(&platform, &kind, &stored); err != nil {
			return err
		}
		if kind != service.AccountTypeCodexAPI {
			continue
		}
		effective := credentials
		if merge {
			effective = make(map[string]any)
			if len(stored) > 0 {
				if err := json.Unmarshal(stored, &effective); err != nil {
					return err
				}
			}
			if effective == nil {
				effective = make(map[string]any)
			}
			for key, value := range credentials {
				effective[key] = value
			}
		}
		if err := service.ValidateCodexAPIAccount(platform, kind, effective); err != nil {
			return err
		}
	}
	return rows.Err()
}

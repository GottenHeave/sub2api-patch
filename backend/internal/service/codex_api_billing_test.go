package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCodexAPIObservedUsageChargesConfiguredAccountQuota(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	billingRepo := &openAIRecordUsageBillingRepoStub{}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo,
		&openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
	account := &Account{ID: 3, Platform: PlatformOpenAI, Type: AccountTypeCodexAPI,
		Extra: map[string]any{"quota_limit": 100.0}}
	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &OpenAIForwardResult{RequestID: "codex-api-billing", Model: "gpt-5.1", Usage: OpenAIUsage{InputTokens: 1200, OutputTokens: 300}},
		APIKey: &APIKey{ID: 1, Group: &Group{RateMultiplier: 1}},
		User:   &User{ID: 2}, Account: account,
	})
	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.NotNil(t, billingRepo.lastCmd)
	require.Greater(t, billingRepo.lastCmd.AccountQuotaCost, 0.0)
	require.InDelta(t, usageRepo.lastLog.TotalCost, billingRepo.lastCmd.AccountQuotaCost, 1e-10)
	require.Equal(t, 1200, usageRepo.lastLog.InputTokens)
}

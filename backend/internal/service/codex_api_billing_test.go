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

func TestCodexAPIBillingMatchesOAuthServiceTierContract(t *testing.T) {
	tests := []struct {
		name      string
		requested string
		observed  string
	}{
		{name: "default response for default request", requested: "default", observed: "default"},
		{name: "default response for priority request", requested: "priority", observed: "default"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oauthLog, oauthBilling := recordCodexTierUsage(t, AccountTypeOAuth, tt.requested, tt.observed)
			codexLog, codexBilling := recordCodexTierUsage(t, AccountTypeCodexAPI, tt.requested, tt.observed)

			require.Equal(t, *oauthLog.ServiceTier, *codexLog.ServiceTier)
			require.Equal(t, 2000, oauthLog.CacheReadTokens)
			require.Equal(t, 1.5, oauthLog.RateMultiplier)
			require.InDelta(t, oauthLog.TotalCost, codexLog.TotalCost, 1e-12)
			require.InDelta(t, oauthLog.ActualCost, codexLog.ActualCost, 1e-12)
			require.InDelta(t, codexLog.TotalCost, codexBilling.AccountQuotaCost, 1e-12)
			require.Equal(t, *oauthLog.ServiceTier, oauthBilling.ServiceTier)
			require.Equal(t, *codexLog.ServiceTier, codexBilling.ServiceTier)
		})
	}

	apiKeyLog, _ := recordCodexTierUsage(t, AccountTypeAPIKey, "priority", "default")
	require.Equal(t, "default", *apiKeyLog.ServiceTier)
	priorityOAuthLog, _ := recordCodexTierUsage(t, AccountTypeOAuth, "priority", "default")
	require.Less(t, apiKeyLog.TotalCost, priorityOAuthLog.TotalCost)
}

func recordCodexTierUsage(t *testing.T, accountType, requested, observed string) (*UsageLog, *UsageBillingCommand) {
	t.Helper()
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	billingRepo := &openAIRecordUsageBillingRepoStub{}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo,
		&openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
	groupID := int64(1)
	requestedTier := requested
	account := &Account{Platform: PlatformOpenAI, Type: accountType}
	if accountType == AccountTypeCodexAPI {
		account.Extra = map[string]any{"quota_limit": 100.0}
	}

	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &OpenAIForwardResult{
			Model:                       "gpt-5.1",
			ServiceTier:                 &requestedTier,
			UpstreamResponseServiceTier: observed,
			Usage: OpenAIUsage{
				InputTokens:          5000,
				OutputTokens:         1000,
				CacheReadInputTokens: 2000,
			},
		},
		APIKey: &APIKey{
			GroupID: &groupID,
			Group:   &Group{ID: groupID, RateMultiplier: 1.5},
		},
		User:    &User{},
		Account: account,
	})
	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.NotNil(t, billingRepo.lastCmd)
	require.NotNil(t, usageRepo.lastLog.ServiceTier)
	return usageRepo.lastLog, billingRepo.lastCmd
}

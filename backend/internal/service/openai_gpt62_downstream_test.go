//go:build unit

package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestGPT62DownstreamCacheCreation12hPricing(t *testing.T) {
	for _, model := range []string{"gpt-6.2-sol", "gpt-6.2-astra"} {
		t.Run(model, func(t *testing.T) {
			unit := 2e-6
			if model == "gpt-6.2-astra" {
				unit = 8e-6
			}
			svc := newTestBillingService()
			for _, resolver := range []*ModelPricingResolver{nil, NewModelPricingResolver(nil, svc)} {
				cost, err := svc.CalculateCostUnified(CostInput{
					Model: model, RateMultiplier: 1, Resolver: resolver,
					PricingAt: time.Date(2027, 2, 1, 0, 0, 0, 0, time.UTC),
					Tokens: UsageTokens{CacheCreationTokens: 100, CacheCreation12hTokens: 100},
				})
				require.NoError(t, err)
				require.InDelta(t, 100*unit*2.5, cost.CacheCreationCost, 1e-12)
			}
		})
	}
}

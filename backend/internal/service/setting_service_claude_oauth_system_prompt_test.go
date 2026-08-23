package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func resetGatewayForwardingSettingsCacheForTest(t *testing.T) {
	t.Helper()
	gatewayForwardingSF.Forget("gateway_forwarding")
	gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{})
	t.Cleanup(func() {
		gatewayForwardingSF.Forget("gateway_forwarding")
		gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{})
	})
}

func TestSettingService_GetClaudeOAuthSystemPromptInjectionSettings(t *testing.T) {
	t.Run("defaults to enabled with empty prompt", func(t *testing.T) {
		resetGatewayForwardingSettingsCacheForTest(t)
		svc := NewSettingService(&gatewayTTLSettingRepo{data: map[string]string{}}, &config.Config{})

		enabled, prompt, blocks := svc.GetClaudeOAuthSystemPromptInjectionSettings(context.Background())

		require.True(t, enabled)
		require.Empty(t, prompt)
		require.Empty(t, blocks)
	})

	t.Run("uses configured switch prompt and blocks", func(t *testing.T) {
		resetGatewayForwardingSettingsCacheForTest(t)
		const customPrompt = "custom prompt\n\nkeep spacing"
		const customBlocks = `[{"type":"text","text":"custom block","cache_control":true}]`
		svc := NewSettingService(&gatewayTTLSettingRepo{data: map[string]string{
			SettingKeyEnableClaudeOAuthSystemPromptInjection: "false",
			SettingKeyClaudeOAuthSystemPrompt:                customPrompt,
			SettingKeyClaudeOAuthSystemPromptBlocks:          customBlocks,
		}}, &config.Config{})

		enabled, prompt, blocks := svc.GetClaudeOAuthSystemPromptInjectionSettings(context.Background())

		require.False(t, enabled)
		require.Equal(t, customPrompt, prompt)
		require.Equal(t, customBlocks, blocks)
	})
}

func TestSettingService_IsOpenAICodexPromptInjectionEnabled(t *testing.T) {
	t.Run("defaults to disabled", func(t *testing.T) {
		resetGatewayForwardingSettingsCacheForTest(t)
		svc := NewSettingService(&gatewayTTLSettingRepo{data: map[string]string{}}, &config.Config{})

		require.False(t, svc.IsOpenAICodexPromptInjectionEnabled(context.Background()))
	})

	t.Run("uses configured switch", func(t *testing.T) {
		resetGatewayForwardingSettingsCacheForTest(t)
		svc := NewSettingService(&gatewayTTLSettingRepo{data: map[string]string{
			SettingKeyEnableOpenAICodexPromptInjection: "true",
		}}, &config.Config{})

		require.True(t, svc.IsOpenAICodexPromptInjectionEnabled(context.Background()))
	})

	t.Run("fails closed when settings cannot be read", func(t *testing.T) {
		resetGatewayForwardingSettingsCacheForTest(t)
		svc := NewSettingService(&gatewayForwardingErrorSettingRepo{}, &config.Config{})

		require.False(t, svc.IsOpenAICodexPromptInjectionEnabled(context.Background()))
	})
}

type gatewayForwardingErrorSettingRepo struct{}

func (r *gatewayForwardingErrorSettingRepo) Get(context.Context, string) (*Setting, error) {
	return nil, ErrSettingNotFound
}

func (r *gatewayForwardingErrorSettingRepo) GetValue(context.Context, string) (string, error) {
	return "", ErrSettingNotFound
}

func (r *gatewayForwardingErrorSettingRepo) Set(context.Context, string, string) error {
	return errors.New("unexpected Set call")
}

func (r *gatewayForwardingErrorSettingRepo) GetMultiple(context.Context, []string) (map[string]string, error) {
	return nil, errors.New("settings unavailable")
}

func (r *gatewayForwardingErrorSettingRepo) SetMultiple(context.Context, map[string]string) error {
	return errors.New("unexpected SetMultiple call")
}

func (r *gatewayForwardingErrorSettingRepo) GetAll(context.Context) (map[string]string, error) {
	return nil, errors.New("unexpected GetAll call")
}

func (r *gatewayForwardingErrorSettingRepo) Delete(context.Context, string) error {
	return errors.New("unexpected Delete call")
}

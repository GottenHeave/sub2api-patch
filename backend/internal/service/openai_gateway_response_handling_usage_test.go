package service

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIUsageFromGJSON_ParsesSingularInputTokenDetails(t *testing.T) {
	usage, ok := openAIUsageFromGJSON(gjson.Parse(`{
		"input_tokens": 100,
		"output_tokens": 5,
		"input_token_details": {"cached_tokens": 42}
	}`))

	require.True(t, ok)
	require.Equal(t, 42, usage.CacheReadInputTokens)
}

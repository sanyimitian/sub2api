package service

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestOpenAISessionPoolRequiresExplicitOpenAIAPIKeyAccount(t *testing.T) {
	for _, tc := range []struct {
		platform, accountType string
		enabled, want         bool
	}{
		{PlatformOpenAI, AccountTypeAPIKey, true, true},
		{PlatformOpenAI, AccountTypeAPIKey, false, false},
		{PlatformOpenAI, AccountTypeOAuth, true, false},
		{PlatformOpenAI, AccountTypeSetupToken, true, false},
		{PlatformAnthropic, AccountTypeAPIKey, true, false},
	} {
		a := &Account{Platform: tc.platform, Type: tc.accountType, Extra: map[string]any{OpenAISessionPoolEnabledExtraKey: tc.enabled}}
		require.Equal(t, tc.want, a.OpenAISessionPoolEnabled())
		require.Equal(t, tc.enabled && !tc.want, ValidateOpenAISessionPoolExtra(a.Platform, a.Type, a.Extra) != nil)
	}
	var absent *Account
	require.False(t, absent.OpenAISessionPoolEnabled())
	require.NoError(t, ValidateOpenAISessionPoolExtra(PlatformOpenAI, AccountTypeAPIKey, nil))
	require.Error(t, ValidateOpenAISessionPoolExtra(PlatformOpenAI, AccountTypeAPIKey, map[string]any{OpenAISessionPoolEnabledExtraKey: "true"}))
}

func TestOpenAISessionPoolIdentityInvalidatesOnlyWhenRouteCredentialsChange(t *testing.T) {
	a := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "test-secret", "base_url": "https://example.com"}}
	before := openAISessionPoolAccountFingerprint(a)
	require.NotEmpty(t, before)
	require.NotContains(t, before, "test-secret")
	a.Name = "修改展示名称"
	a.GroupIDs = []int64{1, 2}
	require.Equal(t, before, openAISessionPoolAccountFingerprint(a))
	a.Credentials["api_key"] = "different-test-secret"
	require.NotEqual(t, before, openAISessionPoolAccountFingerprint(a))
}

func TestOpenAISessionPoolModelConfiguration(t *testing.T) {
	a := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	require.Equal(t, []string{"gpt-6-sol"}, a.OpenAISessionPoolModels())
	a.Extra = map[string]any{OpenAISessionPoolModelsExtraKey: []any{" gpt-6-sol ", "custom-model", "gpt-6-sol"}}
	require.NoError(t, ValidateOpenAISessionPoolExtra(a.Platform, a.Type, a.Extra))
	require.Equal(t, []string{"gpt-6-sol", "custom-model"}, a.OpenAISessionPoolModels())
	for _, invalid := range []any{[]any{}, []any{""}, "gpt-6-sol", []any{1}, []any{false}} {
		require.Error(t, ValidateOpenAISessionPoolExtra(a.Platform, a.Type, map[string]any{OpenAISessionPoolModelsExtraKey: invalid}))
	}
}

func TestOpenAISessionPoolDefaultModelRespectsAccountSupport(t *testing.T) {
	for _, tc := range []struct {
		mapping map[string]any
		want    string
	}{
		{nil, "gpt-6-sol"},
		{map[string]any{"gpt-6-sol": "upstream-sol", "gpt-6.1-sol": "upstream-61"}, "gpt-6-sol"},
		{map[string]any{"gpt-6.1-sol": "upstream-61"}, "gpt-6.1-sol"},
		{map[string]any{"gpt-6-*": "upstream"}, "gpt-6-sol"},
	} {
		a := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"model_mapping": tc.mapping}}
		require.Equal(t, []string{tc.want}, a.OpenAISessionPoolModels())
		a.Extra = map[string]any{OpenAISessionPoolModelsExtraKey: []string{"custom"}}
		require.Equal(t, []string{"custom"}, a.OpenAISessionPoolModels())
	}
}

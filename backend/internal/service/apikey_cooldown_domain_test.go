package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDefaultAPIKeyFailureCooldownSettings(t *testing.T) {
	settings := DefaultAPIKeyFailureCooldownSettings()

	require.Equal(t, APIKeyFailureCooldownSettingsVersion, settings.Version)
	require.Equal(t, map[APIKeyFailureFamily]APIKeyCooldownPolicy{
		APIKeyFailureRateLimit:          {Enabled: true, Cooldowns: []int{60, 300, 900}, Mode: APIKeyCooldownModeHoldLast},
		APIKeyFailureOverload:           {Enabled: true, Cooldowns: []int{60, 300}, Mode: APIKeyCooldownModeHoldLast},
		APIKeyFailureTransientUpstream:  {Enabled: true, Cooldowns: []int{30, 120, 600}, Mode: APIKeyCooldownModeHoldLast},
		APIKeyFailureTemporaryForbidden: {Enabled: true, Cooldowns: []int{300}, Mode: APIKeyCooldownModeHoldLast},
		APIKeyFailureAccountBlocked:     {Enabled: true, Cooldowns: []int{300}, Mode: APIKeyCooldownModeHoldLast},
		APIKeyFailureUnauthorized:       {Enabled: true, Cooldowns: []int{1800}, Mode: APIKeyCooldownModeHoldLast},
		APIKeyFailureQuotaExhausted:     {Enabled: true, Cooldowns: []int{3600}, Mode: APIKeyCooldownModeHoldLast},
		APIKeyFailureModelUnsupported:   {Enabled: true, Cooldowns: []int{1800}, Mode: APIKeyCooldownModeHoldLast},
		APIKeyFailureGlobalUpstream:     {Enabled: true, Cooldowns: []int{1800}, Mode: APIKeyCooldownModeHoldLast},
		APIKeyFailureUnknown:            {Enabled: true, Cooldowns: []int{60, 600, 1800}, Mode: APIKeyCooldownModeCycle},
	}, settings.Policies)

	settings.Policies[APIKeyFailureRateLimit] = APIKeyCooldownPolicy{}
	fresh := DefaultAPIKeyFailureCooldownSettings()
	require.Equal(t, []int{60, 300, 900}, fresh.Policies[APIKeyFailureRateLimit].Cooldowns,
		"default settings must not share mutable state")
}

func TestIsAPIKeyFailureCooldownApplicable(t *testing.T) {
	tests := []struct {
		name    string
		account *Account
		want    bool
	}{
		{name: "nil", account: nil, want: false},
		{name: "direct api key", account: &Account{Type: AccountTypeAPIKey}, want: true},
		{name: "api key pool", account: &Account{Type: AccountTypeAPIKey, Credentials: map[string]any{"pool_mode": true}}, want: false},
		{name: "oauth", account: &Account{Type: AccountTypeOAuth}, want: false},
		{name: "setup token", account: &Account{Type: AccountTypeSetupToken}, want: false},
		{name: "bedrock", account: &Account{Type: AccountTypeBedrock}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, IsAPIKeyFailureCooldownApplicable(tt.account))
		})
	}
}

func TestAPIKeyFailureFamilyDefaultScope(t *testing.T) {
	require.Equal(t, APIKeyCooldownScopeAccount, APIKeyFailureRateLimit.DefaultScope())
	require.Equal(t, APIKeyCooldownScopeAccount, APIKeyFailureTransientUpstream.DefaultScope())
	require.Equal(t, APIKeyCooldownScopeModel, APIKeyFailureModelUnsupported.DefaultScope())
	require.Equal(t, APIKeyCooldownScopeModel, APIKeyFailureOverload.DefaultScope())
}

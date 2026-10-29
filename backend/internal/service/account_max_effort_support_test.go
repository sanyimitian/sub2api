package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAccountSupportsMaxReasoningEffort(t *testing.T) {
	tests := []struct {
		name        string
		platform    string
		credentials map[string]any
		model       string
		want        bool
	}{
		{name: "missing configuration defaults to supported", platform: PlatformOpenAI, model: "gpt-6-astra", want: true},
		{name: "explicit false disables", platform: PlatformOpenAI, credentials: map[string]any{openAIMaxEffortSupportCredentialKey: map[string]any{"gpt-6-astra": false}}, model: "gpt-6-astra", want: false},
		{name: "explicit true enables", platform: PlatformOpenAI, credentials: map[string]any{openAIMaxEffortSupportCredentialKey: map[string]any{"gpt-6-astra": true}}, model: "gpt-6-astra", want: true},
		{name: "exact overrides wildcard", platform: PlatformOpenAI, credentials: map[string]any{openAIMaxEffortSupportCredentialKey: map[string]any{"gpt-*": false, "gpt-6-astra": true}}, model: "gpt-6-astra", want: true},
		{name: "longest wildcard wins", platform: PlatformOpenAI, credentials: map[string]any{openAIMaxEffortSupportCredentialKey: map[string]any{"gpt-*": true, "gpt-6-*": false}}, model: "gpt-6-astra", want: false},
		{name: "request-side alias is checked", platform: PlatformOpenAI, credentials: map[string]any{
			"model_mapping": map[string]any{"public-astra": "gpt-6-astra"},
			openAIMaxEffortSupportCredentialKey: map[string]any{"public-astra": false},
		}, model: "public-astra", want: false},
		{name: "mapped model is a fallback key", platform: PlatformOpenAI, credentials: map[string]any{
			"model_mapping": map[string]any{"public-astra": "gpt-6-astra"},
			openAIMaxEffortSupportCredentialKey: map[string]any{"gpt-6-astra": false},
		}, model: "public-astra", want: false},
		{name: "max model suffix uses base entry", platform: PlatformOpenAI, credentials: map[string]any{openAIMaxEffortSupportCredentialKey: map[string]any{"gpt-6-astra": false}}, model: "gpt-6-astra-max", want: false},
		{name: "non-OpenAI account is not gated", platform: PlatformAnthropic, credentials: map[string]any{openAIMaxEffortSupportCredentialKey: map[string]any{"claude-*": false}}, model: "claude-sonnet-4", want: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			account := &Account{Platform: test.platform, Credentials: test.credentials}
			require.Equal(t, test.want, account.SupportsMaxReasoningEffort(test.model))
		})
	}
}

func TestAccountSupportsMaxReasoningEffortUsesRequestedEffortContext(t *testing.T) {
	account := &Account{
		Platform:    PlatformOpenAI,
		Credentials: map[string]any{openAIMaxEffortSupportCredentialKey: map[string]any{"gpt-6-astra": false}},
	}

	require.True(t, accountSupportsRequestedMaxEffort(context.Background(), account, "gpt-6-astra"))
	require.True(t, accountSupportsRequestedMaxEffort(WithRequestedReasoningEffort(context.Background(), "high"), account, "gpt-6-astra"))
	require.False(t, accountSupportsRequestedMaxEffort(WithRequestedReasoningEffort(context.Background(), "max"), account, "gpt-6-astra"))
	require.False(t, accountSupportsRequestedMaxEffort(WithRequestedReasoningEffort(context.Background(), "MAX"), account, "gpt-6-astra"))
}

func TestOpenAIAccountSchedulerRejectsMaxUnsupportedAccount(t *testing.T) {
	account := &Account{
		Platform:    PlatformOpenAI,
		Credentials: map[string]any{openAIMaxEffortSupportCredentialKey: map[string]any{"gpt-6-astra": false}},
	}
	scheduler := &defaultOpenAIAccountScheduler{}
	ctx := WithRequestedReasoningEffort(context.Background(), "max")

	compatible, reason := scheduler.isAccountRequestCompatibleReason(ctx, account, OpenAIAccountScheduleRequest{
		Platform:       PlatformOpenAI,
		RequestedModel: "gpt-6-astra",
	})
	require.False(t, compatible)
	require.Equal(t, "max_effort_unsupported", reason)
}

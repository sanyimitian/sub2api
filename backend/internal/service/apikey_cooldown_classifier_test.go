package service

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestClassifyAPIKeyFailurePriority(t *testing.T) {
	base := APIKeyFailureObservation{
		AccountID:   42,
		AccountType: AccountTypeAPIKey,
		HTTPStatus:  http.StatusServiceUnavailable,
		ReplaySafe:  true,
	}
	tests := []struct {
		name        string
		mutate      func(*APIKeyFailureObservation)
		disposition APIKeyCooldownDisposition
		family      APIKeyFailureFamily
	}{
		{name: "oauth ignored", mutate: func(o *APIKeyFailureObservation) { o.AccountType = AccountTypeOAuth }, disposition: APIKeyCooldownDispositionIgnored},
		{name: "pool ignored", mutate: func(o *APIKeyFailureObservation) { o.PoolMode = true }, disposition: APIKeyCooldownDispositionIgnored},
		{name: "matched custom rule delegated", mutate: func(o *APIKeyFailureObservation) { o.CustomRuleMatched = true }, disposition: APIKeyCooldownDispositionDelegated},
		{name: "request error ignored", mutate: func(o *APIKeyFailureObservation) { o.RequestError = true }, disposition: APIKeyCooldownDispositionIgnored},
		{name: "content safety ignored", mutate: func(o *APIKeyFailureObservation) { o.ContentSafety = true }, disposition: APIKeyCooldownDispositionIgnored},
		{name: "client canceled ignored", mutate: func(o *APIKeyFailureObservation) { o.ClientCanceled = true }, disposition: APIKeyCooldownDispositionIgnored},
		{name: "client timeout ignored", mutate: func(o *APIKeyFailureObservation) { o.ClientTimedOut = true }, disposition: APIKeyCooldownDispositionIgnored},
		{name: "response started ignored", mutate: func(o *APIKeyFailureObservation) { o.ResponseStarted = true }, disposition: APIKeyCooldownDispositionIgnored},
		{name: "permanent credential delegated", mutate: func(o *APIKeyFailureObservation) { o.PermanentCredential = true }, disposition: APIKeyCooldownDispositionDelegated},
		{name: "invalid key signal delegated", mutate: func(o *APIKeyFailureObservation) {
			o.HTTPStatus = http.StatusUnauthorized
			o.ErrorCode = "invalid_api_key"
		}, disposition: APIKeyCooldownDispositionDelegated},
		{name: "known failure", mutate: func(*APIKeyFailureObservation) {}, disposition: APIKeyCooldownDispositionCooldown, family: APIKeyFailureTransientUpstream},
		{name: "unmatched custom configuration still classifies", mutate: func(o *APIKeyFailureObservation) { o.CustomRuleMatched = false }, disposition: APIKeyCooldownDispositionCooldown, family: APIKeyFailureTransientUpstream},
		{name: "unknown upstream fallback", mutate: func(o *APIKeyFailureObservation) {
			o.HTTPStatus = http.StatusTeapot
			o.ErrorSummary = "provider-specific failure"
		}, disposition: APIKeyCooldownDispositionCooldown, family: APIKeyFailureUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			observation := base
			tt.mutate(&observation)
			decision := ClassifyAPIKeyFailure(observation)
			require.Equal(t, tt.disposition, decision.Disposition)
			require.Equal(t, tt.family, decision.Family)
			if decision.ShouldCooldown() {
				require.True(t, decision.Exclude)
				require.True(t, decision.SafeToReplay)
			}
		})
	}
}

func TestClassifyAPIKeyFailureKnownFamiliesAndScopes(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		code      string
		summary   string
		transport APIKeyTransportError
		model     string
		global    bool
		wide      bool
		family    APIKeyFailureFamily
		scope     APIKeyCooldownScope
	}{
		{name: "429", status: 429, family: APIKeyFailureRateLimit, scope: APIKeyCooldownScopeAccount},
		{name: "529 model", status: 529, model: "claude-5", family: APIKeyFailureOverload, scope: APIKeyCooldownScopeModel},
		{name: "529 account without model", status: 529, family: APIKeyFailureOverload, scope: APIKeyCooldownScopeAccount},
		{name: "529 explicit account wide", status: 529, model: "claude-5", wide: true, family: APIKeyFailureOverload, scope: APIKeyCooldownScopeAccount},
		{name: "500", status: 500, family: APIKeyFailureTransientUpstream, scope: APIKeyCooldownScopeAccount},
		{name: "connection timeout", transport: APIKeyTransportErrorConnectTimeout, family: APIKeyFailureTransientUpstream, scope: APIKeyCooldownScopeAccount},
		{name: "connection reset", transport: APIKeyTransportErrorReset, family: APIKeyFailureTransientUpstream, scope: APIKeyCooldownScopeAccount},
		{name: "empty response", transport: APIKeyTransportErrorEmptyResponse, family: APIKeyFailureTransientUpstream, scope: APIKeyCooldownScopeAccount},
		{name: "temporary 403", status: 403, code: "concurrency_limit", family: APIKeyFailureTemporaryForbidden, scope: APIKeyCooldownScopeAccount},
		{name: "blocked account", status: 403, code: "account_suspended", family: APIKeyFailureAccountBlocked, scope: APIKeyCooldownScopeAccount},
		{name: "other 401", status: 401, summary: "authentication failed", family: APIKeyFailureUnauthorized, scope: APIKeyCooldownScopeAccount},
		{name: "quota", status: 429, code: "insufficient_quota", family: APIKeyFailureQuotaExhausted, scope: APIKeyCooldownScopeAccount},
		{name: "model unsupported", status: 404, code: "model_not_found", model: "gpt-x", family: APIKeyFailureModelUnsupported, scope: APIKeyCooldownScopeModel},
		{name: "global only current account", status: 503, global: true, family: APIKeyFailureGlobalUpstream, scope: APIKeyCooldownScopeAccount},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision := ClassifyAPIKeyFailure(APIKeyFailureObservation{
				AccountType: AccountTypeAPIKey,
				HTTPStatus:  tt.status, ErrorCode: tt.code, ErrorSummary: tt.summary,
				TransportError: tt.transport, Model: tt.model, ExplicitGlobal: tt.global,
				AccountWideOverload: tt.wide,
			})
			require.Equal(t, APIKeyCooldownDispositionCooldown, decision.Disposition)
			require.Equal(t, tt.family, decision.Family)
			require.Equal(t, tt.scope, decision.Scope)
		})
	}
}

func TestParseAPIKeyUpstreamReset(t *testing.T) {
	now := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		headers http.Header
		want    time.Time
		ok      bool
	}{
		{name: "retry after seconds", headers: http.Header{"Retry-After": []string{"75"}}, want: now.Add(75 * time.Second), ok: true},
		{name: "retry after http date", headers: http.Header{"Retry-After": []string{now.Add(2 * time.Minute).Format(http.TimeFormat)}}, want: now.Add(2 * time.Minute), ok: true},
		{name: "unix reset", headers: http.Header{"X-RateLimit-Reset": []string{"1787832300"}}, want: time.Unix(1787832300, 0), ok: true},
		{name: "unix milliseconds reset", headers: http.Header{"X-RateLimit-Reset": []string{"1787832300000"}}, want: time.UnixMilli(1787832300000), ok: true},
		{name: "rfc3339 vendor reset", headers: http.Header{"Anthropic-Ratelimit-Requests-Reset": []string{now.Add(3 * time.Minute).Format(time.RFC3339)}}, want: now.Add(3 * time.Minute), ok: true},
		{name: "expired reset", headers: http.Header{"Retry-After": []string{"Thu, 27 Aug 2026 11:00:00 GMT"}}, ok: false},
		{name: "invalid reset", headers: http.Header{"Retry-After": []string{"later"}}, ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseAPIKeyUpstreamReset(tt.headers, now)
			require.Equal(t, tt.ok, ok)
			if tt.ok {
				require.Equal(t, tt.want, got)
			}
		})
	}
}

func TestClassifyAPIKeyFailureNoFailureSignalIgnored(t *testing.T) {
	decision := ClassifyAPIKeyFailure(APIKeyFailureObservation{AccountType: AccountTypeAPIKey})
	require.Equal(t, APIKeyCooldownDispositionIgnored, decision.Disposition)
}

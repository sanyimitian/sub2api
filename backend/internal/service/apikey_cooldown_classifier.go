package service

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

func ClassifyAPIKeyFailure(observation APIKeyFailureObservation) APIKeyCooldownDecision {
	observation = SanitizeAPIKeyFailureObservation(observation)
	ignored := APIKeyCooldownDecision{Disposition: APIKeyCooldownDispositionIgnored}
	delegated := APIKeyCooldownDecision{Disposition: APIKeyCooldownDispositionDelegated}
	if !observation.Applicable() {
		return ignored
	}
	if observation.CustomRuleMatched {
		return delegated
	}
	if isExcludedAPIKeyRequestFailure(observation) {
		return ignored
	}
	if observation.PermanentCredential || isPermanentCredentialSignal(observation) {
		return delegated
	}
	if !hasAPIKeyFailureSignal(observation) {
		return ignored
	}

	family, scope := classifyKnownAPIKeyFailure(observation)
	if family == "" {
		family = APIKeyFailureUnknown
		scope = APIKeyCooldownScopeAccount
	}
	return APIKeyCooldownDecision{
		Disposition:  APIKeyCooldownDispositionCooldown,
		Family:       family,
		Scope:        scope,
		Exclude:      true,
		SafeToReplay: observation.ReplaySafe && !observation.ClientCanceled && !observation.ClientTimedOut && !observation.ResponseStarted,
		Reason:       string(family),
	}
}

func isExcludedAPIKeyRequestFailure(observation APIKeyFailureObservation) bool {
	if observation.RequestError || observation.ContentSafety || observation.ClientCanceled || observation.ClientTimedOut {
		return true
	}
	if observation.ResponseStarted && !observation.FirstValidContentTimedOut {
		return true
	}
	code := strings.ToLower(strings.TrimSpace(observation.ErrorCode))
	errorType := strings.ToLower(strings.TrimSpace(observation.ErrorType))
	return containsExact(code, "invalid_request", "invalid_request_error", "bad_request", "content_policy_violation", "content_filter") ||
		containsExact(errorType, "invalid_request", "invalid_request_error", "content_policy", "content_filter")
}

func isPermanentCredentialSignal(observation APIKeyFailureObservation) bool {
	code := strings.ToLower(strings.TrimSpace(observation.ErrorCode))
	errorType := strings.ToLower(strings.TrimSpace(observation.ErrorType))
	summary := strings.ToLower(observation.ErrorSummary)
	if containsExact(code, "invalid_api_key", "invalid_key", "token_revoked", "invalid_token", "authentication_token_invalid") ||
		containsExact(errorType, "invalid_api_key", "invalid_token", "token_revoked") {
		return true
	}
	return observation.HTTPStatus == http.StatusUnauthorized && apiKeyContainsAny(summary,
		"invalid api key", "api key is invalid", "token has been revoked", "revoked token")
}

func hasAPIKeyFailureSignal(observation APIKeyFailureObservation) bool {
	return observation.HTTPStatus >= http.StatusBadRequest || observation.TransportError != APIKeyTransportErrorNone ||
		strings.TrimSpace(observation.ErrorCode) != "" || strings.TrimSpace(observation.ErrorType) != "" || strings.TrimSpace(observation.ErrorSummary) != ""
}

func classifyKnownAPIKeyFailure(observation APIKeyFailureObservation) (APIKeyFailureFamily, APIKeyCooldownScope) {
	code := strings.ToLower(strings.TrimSpace(observation.ErrorCode))
	errorType := strings.ToLower(strings.TrimSpace(observation.ErrorType))
	summary := strings.ToLower(observation.ErrorSummary)

	if apiKeyContainsAny(code, "insufficient_quota", "quota_exhausted", "billing_hard_limit") ||
		apiKeyContainsAny(errorType, "insufficient_quota", "quota_exhausted") ||
		apiKeyContainsAny(summary, "quota exhausted", "quota has been exhausted", "insufficient balance", "balance exhausted", "billing hard limit") {
		return APIKeyFailureQuotaExhausted, APIKeyCooldownScopeAccount
	}
	if apiKeyContainsAny(code, "model_not_found", "model_unsupported", "unsupported_model") ||
		apiKeyContainsAny(errorType, "model_not_found", "model_unsupported") ||
		apiKeyContainsAny(summary, "model not found", "model is not supported", "does not support model") {
		return APIKeyFailureModelUnsupported, APIKeyCooldownScopeModel
	}
	if observation.ExplicitGlobal || apiKeyContainsAny(code, "global_outage", "service_outage") || apiKeyContainsAny(errorType, "global_outage", "service_outage") {
		return APIKeyFailureGlobalUpstream, APIKeyCooldownScopeAccount
	}
	if observation.HTTPStatus == 529 || apiKeyContainsAny(code, "overloaded", "server_is_overloaded", "model_overloaded") ||
		apiKeyContainsAny(errorType, "overloaded", "server_is_overloaded") {
		if strings.TrimSpace(observation.Model) != "" && !observation.AccountWideOverload {
			return APIKeyFailureOverload, APIKeyCooldownScopeModel
		}
		return APIKeyFailureOverload, APIKeyCooldownScopeAccount
	}
	if observation.HTTPStatus == http.StatusTooManyRequests {
		return APIKeyFailureRateLimit, APIKeyCooldownScopeAccount
	}
	if observation.HTTPStatus == http.StatusUnauthorized {
		return APIKeyFailureUnauthorized, APIKeyCooldownScopeAccount
	}
	if observation.HTTPStatus == http.StatusForbidden {
		if apiKeyContainsAny(code, "temporarily_restricted", "concurrency_limit") ||
			apiKeyContainsAny(errorType, "temporarily_restricted", "concurrency_limit") ||
			apiKeyContainsAny(summary, "temporarily restricted", "temporary access restriction", "concurrency limit") {
			return APIKeyFailureTemporaryForbidden, APIKeyCooldownScopeAccount
		}
		if apiKeyContainsAny(code, "account_suspended", "account_blocked", "permission_revoked") ||
			apiKeyContainsAny(errorType, "account_suspended", "account_blocked", "permission_revoked") ||
			apiKeyContainsAny(summary, "account suspended", "account blocked", "permission permanently revoked") {
			return APIKeyFailureAccountBlocked, APIKeyCooldownScopeAccount
		}
	}
	if observation.HTTPStatus == http.StatusInternalServerError || observation.HTTPStatus == http.StatusBadGateway ||
		observation.HTTPStatus == http.StatusServiceUnavailable || observation.HTTPStatus == http.StatusGatewayTimeout ||
		observation.TransportError == APIKeyTransportErrorConnectTimeout || observation.TransportError == APIKeyTransportErrorReadTimeout ||
		observation.TransportError == APIKeyTransportErrorReset || observation.TransportError == APIKeyTransportErrorEmptyResponse {
		return APIKeyFailureTransientUpstream, APIKeyCooldownScopeAccount
	}
	return "", ""
}

func containsExact(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if value == candidate {
			return true
		}
	}
	return false
}

func apiKeyContainsAny(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}

// ParseAPIKeyUpstreamReset accepts Retry-After and a small allowlist of stable reset headers.
func ParseAPIKeyUpstreamReset(headers http.Header, now time.Time) (time.Time, bool) {
	if headers == nil {
		return time.Time{}, false
	}
	if value := strings.TrimSpace(apiKeyHeaderValue(headers, "Retry-After")); value != "" {
		if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds > 0 {
			return now.Add(time.Duration(seconds) * time.Second), true
		}
		if resetAt, err := http.ParseTime(value); err == nil && resetAt.After(now) {
			return resetAt, true
		}
	}

	for _, name := range []string{
		"X-RateLimit-Reset",
		"X-RateLimit-Reset-Requests",
		"Anthropic-Ratelimit-Requests-Reset",
		"X-Codex-Primary-Reset-At",
	} {
		value := strings.TrimSpace(apiKeyHeaderValue(headers, name))
		if value == "" {
			continue
		}
		if resetAt, ok := parseAPIKeyResetValue(value, now); ok {
			return resetAt, true
		}
	}
	return time.Time{}, false
}

func apiKeyHeaderValue(headers http.Header, name string) string {
	if value := headers.Get(name); value != "" {
		return value
	}
	for key, values := range headers {
		if strings.EqualFold(key, name) && len(values) > 0 {
			return values[0]
		}
	}
	return ""
}

func parseAPIKeyResetValue(value string, now time.Time) (time.Time, bool) {
	if numeric, err := strconv.ParseInt(value, 10, 64); err == nil {
		var resetAt time.Time
		if numeric >= 1_000_000_000_000 {
			resetAt = time.UnixMilli(numeric)
		} else {
			resetAt = time.Unix(numeric, 0)
		}
		if resetAt.After(now) {
			return resetAt, true
		}
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, http.TimeFormat} {
		if resetAt, err := time.Parse(layout, value); err == nil && resetAt.After(now) {
			return resetAt, true
		}
	}
	return time.Time{}, false
}

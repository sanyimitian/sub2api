package service

import (
	"net/http"
	"time"
)

const APIKeyFailureCooldownSettingsVersion = 1

// APIKeyFailureFamily groups failures that share one consecutive-failure streak.
type APIKeyFailureFamily string

const (
	APIKeyFailureRateLimit          APIKeyFailureFamily = "rate_limit"
	APIKeyFailureOverload           APIKeyFailureFamily = "overload"
	APIKeyFailureTransientUpstream  APIKeyFailureFamily = "transient_upstream"
	APIKeyFailureTemporaryForbidden APIKeyFailureFamily = "temporary_forbidden"
	APIKeyFailureAccountBlocked     APIKeyFailureFamily = "account_blocked"
	APIKeyFailureUnauthorized       APIKeyFailureFamily = "unauthorized"
	APIKeyFailureQuotaExhausted     APIKeyFailureFamily = "quota_exhausted"
	APIKeyFailureModelUnsupported   APIKeyFailureFamily = "model_unsupported"
	APIKeyFailureGlobalUpstream     APIKeyFailureFamily = "global_upstream"
	APIKeyFailureUnknown            APIKeyFailureFamily = "unknown"
)

var allAPIKeyFailureFamilies = []APIKeyFailureFamily{
	APIKeyFailureRateLimit,
	APIKeyFailureOverload,
	APIKeyFailureTransientUpstream,
	APIKeyFailureTemporaryForbidden,
	APIKeyFailureAccountBlocked,
	APIKeyFailureUnauthorized,
	APIKeyFailureQuotaExhausted,
	APIKeyFailureModelUnsupported,
	APIKeyFailureGlobalUpstream,
	APIKeyFailureUnknown,
}

type APIKeyCooldownScope string

const (
	APIKeyCooldownScopeAccount APIKeyCooldownScope = "account"
	APIKeyCooldownScopeModel   APIKeyCooldownScope = "account_model"
)

func (f APIKeyFailureFamily) DefaultScope() APIKeyCooldownScope {
	switch f {
	case APIKeyFailureOverload, APIKeyFailureModelUnsupported:
		return APIKeyCooldownScopeModel
	default:
		return APIKeyCooldownScopeAccount
	}
}

type APIKeyCooldownMode string

const (
	APIKeyCooldownModeHoldLast APIKeyCooldownMode = "hold_last"
	APIKeyCooldownModeCycle    APIKeyCooldownMode = "cycle"
)

type APIKeyCooldownPolicy struct {
	Enabled   bool               `json:"enabled"`
	Cooldowns []int              `json:"cooldowns"`
	Mode      APIKeyCooldownMode `json:"mode"`
}

type APIKeyFailureCooldownSettings struct {
	Version  int                                          `json:"version"`
	Policies map[APIKeyFailureFamily]APIKeyCooldownPolicy `json:"policies"`
}

func DefaultAPIKeyFailureCooldownSettings() *APIKeyFailureCooldownSettings {
	return &APIKeyFailureCooldownSettings{
		Version: APIKeyFailureCooldownSettingsVersion,
		Policies: map[APIKeyFailureFamily]APIKeyCooldownPolicy{
			APIKeyFailureRateLimit:          defaultAPIKeyCooldownPolicy(APIKeyCooldownModeHoldLast, 60, 300, 900),
			APIKeyFailureOverload:           defaultAPIKeyCooldownPolicy(APIKeyCooldownModeHoldLast, 60, 300),
			APIKeyFailureTransientUpstream:  defaultAPIKeyCooldownPolicy(APIKeyCooldownModeHoldLast, 30, 120, 600),
			APIKeyFailureTemporaryForbidden: defaultAPIKeyCooldownPolicy(APIKeyCooldownModeHoldLast, 300),
			APIKeyFailureAccountBlocked:     defaultAPIKeyCooldownPolicy(APIKeyCooldownModeHoldLast, 300),
			APIKeyFailureUnauthorized:       defaultAPIKeyCooldownPolicy(APIKeyCooldownModeHoldLast, 1800),
			APIKeyFailureQuotaExhausted:     defaultAPIKeyCooldownPolicy(APIKeyCooldownModeHoldLast, 3600),
			APIKeyFailureModelUnsupported:   defaultAPIKeyCooldownPolicy(APIKeyCooldownModeHoldLast, 1800),
			APIKeyFailureGlobalUpstream:     defaultAPIKeyCooldownPolicy(APIKeyCooldownModeHoldLast, 1800),
			APIKeyFailureUnknown:            defaultAPIKeyCooldownPolicy(APIKeyCooldownModeCycle, 60, 600, 1800),
		},
	}
}

func defaultAPIKeyCooldownPolicy(mode APIKeyCooldownMode, cooldowns ...int) APIKeyCooldownPolicy {
	return APIKeyCooldownPolicy{Enabled: true, Cooldowns: append([]int(nil), cooldowns...), Mode: mode}
}

func IsAPIKeyFailureCooldownApplicable(account *Account) bool {
	return account != nil && account.Type == AccountTypeAPIKey && !account.IsPoolMode()
}

type APIKeyTransportError string

const (
	APIKeyTransportErrorNone           APIKeyTransportError = ""
	APIKeyTransportErrorConnectTimeout APIKeyTransportError = "connect_timeout"
	APIKeyTransportErrorReadTimeout    APIKeyTransportError = "read_timeout"
	APIKeyTransportErrorReset          APIKeyTransportError = "connection_reset"
	APIKeyTransportErrorEmptyResponse  APIKeyTransportError = "empty_response"
	APIKeyTransportErrorOther          APIKeyTransportError = "other"
)

// APIKeyCooldownAttemptToken captures the shared generations observed immediately before a send.
type APIKeyCooldownAttemptToken struct {
	StartedAt   time.Time        `json:"started_at"`
	Generations map[string]int64 `json:"generations,omitempty"`
}

// APIKeyFailureObservation is the bounded terminal result of one upstream attempt.
type APIKeyFailureObservation struct {
	AttemptID      string
	AttemptStarted time.Time
	AttemptToken   APIKeyCooldownAttemptToken

	AccountID   int64
	AccountType string
	PoolMode    bool
	Platform    string
	Model       string

	HTTPStatus     int
	Headers        http.Header
	UpstreamReset  *time.Time
	ErrorCode      string
	ErrorType      string
	ErrorSummary   string
	TransportError APIKeyTransportError

	RequestSent         bool
	ReplaySafe          bool
	ClientCanceled      bool
	ClientTimedOut      bool
	ResponseStarted     bool
	RequestError        bool
	ContentSafety       bool
	CustomRuleMatched   bool
	PermanentCredential bool
	ExplicitGlobal      bool
	AccountWideOverload bool
}

func (o APIKeyFailureObservation) Applicable() bool {
	return o.AccountType == AccountTypeAPIKey && !o.PoolMode
}

type APIKeyCooldownDisposition string

const APIKeyCooldownActiveReason GatewayFailureReason = "api_key_cooldown_active"

const (
	APIKeyCooldownDispositionIgnored   APIKeyCooldownDisposition = "ignored"
	APIKeyCooldownDispositionDelegated APIKeyCooldownDisposition = "delegated"
	APIKeyCooldownDispositionCooldown  APIKeyCooldownDisposition = "cooldown"
)

type APIKeyCooldownDecision struct {
	Disposition  APIKeyCooldownDisposition
	Family       APIKeyFailureFamily
	Scope        APIKeyCooldownScope
	Streak       int64
	Generation   int64
	Until        time.Time
	Exclude      bool
	SafeToReplay bool
	Reason       string
}

func (d APIKeyCooldownDecision) ShouldCooldown() bool {
	return d.Disposition == APIKeyCooldownDispositionCooldown
}

type APIKeyCooldownSuccess struct {
	AttemptID      string
	AccountID      int64
	Model          string
	AttemptStarted time.Time
	AttemptToken   APIKeyCooldownAttemptToken
}

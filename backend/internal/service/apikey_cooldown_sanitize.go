package service

import (
	"net/http"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/util/logredact"
)

const APIKeyErrorSummaryMaxRunes = 512

var (
	apiKeyAuthorizationPattern = regexp.MustCompile(`(?i)\bauthorization\s*[:=]\s*bearer\s+[^\s,;]+`)
	apiKeyBearerPattern        = regexp.MustCompile(`(?i)\bbearer\s+[a-z0-9._~+/=-]+`)
	apiKeySecretPattern        = regexp.MustCompile(`(?i)\b(?:sk|xai)-[a-z0-9_-]{6,}\b`)
)

func SanitizeAPIKeyErrorSummary(raw string) string {
	if raw == "" {
		return ""
	}
	// Bound work before applying redaction expressions to an untrusted upstream body.
	runes := []rune(raw)
	if len(runes) > APIKeyErrorSummaryMaxRunes*8 {
		runes = runes[:APIKeyErrorSummaryMaxRunes*8]
	}
	preRedacted := apiKeyAuthorizationPattern.ReplaceAllString(string(runes), "authorization=***")
	redacted := logredact.RedactText(preRedacted, "api_key", "apikey", "token", "authorization", "secret", "key")
	redacted = apiKeyBearerPattern.ReplaceAllString(redacted, "Bearer ***")
	redacted = apiKeySecretPattern.ReplaceAllString(redacted, "***")
	redacted = strings.Join(strings.Fields(redacted), " ")

	runes = []rune(redacted)
	if len(runes) > APIKeyErrorSummaryMaxRunes {
		runes = append(runes[:APIKeyErrorSummaryMaxRunes-1], '…')
	}
	return string(runes)
}

func SanitizeAPIKeyFailureObservation(observation APIKeyFailureObservation) APIKeyFailureObservation {
	observation.ErrorCode = sanitizeAPIKeyErrorIdentifier(observation.ErrorCode)
	observation.ErrorType = sanitizeAPIKeyErrorIdentifier(observation.ErrorType)
	observation.ErrorSummary = SanitizeAPIKeyErrorSummary(observation.ErrorSummary)
	observation.Model = truncateAPIKeyRunes(strings.TrimSpace(observation.Model), 256)

	allowedHeaders := make(http.Header)
	for _, name := range []string{
		"Retry-After",
		"X-RateLimit-Reset",
		"X-RateLimit-Reset-Requests",
		"Anthropic-Ratelimit-Requests-Reset",
		"X-Codex-Primary-Reset-At",
	} {
		if value := strings.TrimSpace(apiKeyHeaderValue(observation.Headers, name)); value != "" {
			allowedHeaders.Set(name, truncateAPIKeyRunes(value, 128))
		}
	}
	observation.Headers = allowedHeaders
	return observation
}

func sanitizeAPIKeyErrorIdentifier(value string) string {
	value = strings.TrimSpace(value)
	var builder strings.Builder
	lastUnderscore := false
	for _, r := range value {
		allowed := unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == ':' || r == '-'
		if allowed {
			_, _ = builder.WriteRune(r)
			lastUnderscore = false
		} else if !lastUnderscore && builder.Len() > 0 {
			_ = builder.WriteByte('_')
			lastUnderscore = true
		}
	}
	return strings.Trim(truncateAPIKeyRunes(builder.String(), 96), "_")
}

func truncateAPIKeyRunes(value string, limit int) string {
	if limit <= 0 || utf8.RuneCountInString(value) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[:limit])
}

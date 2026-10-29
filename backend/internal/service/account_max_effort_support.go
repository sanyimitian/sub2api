package service

import (
	"context"
	"strings"
)

const openAIMaxEffortSupportCredentialKey = "model_max_effort_support"

// SupportsMaxReasoningEffort reports whether an OpenAI account allows max for
// the requested model. Unconfigured models remain supported for compatibility.
func (a *Account) SupportsMaxReasoningEffort(requestedModel string) bool {
	if a == nil || !a.IsOpenAI() || a.Credentials == nil {
		return true
	}
	rawSupport := a.Credentials[openAIMaxEffortSupportCredentialKey]
	if !hasBoolModelMapping(rawSupport) {
		return true
	}

	if supported, found := resolveBoolModelMapping(rawSupport, requestedModel); found {
		return supported
	}
	baseModel := stripMaxEffortModelSuffix(requestedModel)
	if baseModel != requestedModel {
		if supported, found := resolveBoolModelMapping(rawSupport, baseModel); found {
			return supported
		}
	}
	mappedModel, matched := a.ResolveMappedModel(requestedModel)
	if matched {
		if supported, found := resolveBoolModelMapping(rawSupport, mappedModel); found {
			return supported
		}
		mappedBaseModel := stripMaxEffortModelSuffix(mappedModel)
		if mappedBaseModel != mappedModel {
			if supported, found := resolveBoolModelMapping(rawSupport, mappedBaseModel); found {
				return supported
			}
		}
	}
	if normalized := normalizeRequestedModelForLookup(a.Platform, requestedModel); normalized != requestedModel {
		if supported, found := resolveBoolModelMapping(rawSupport, normalized); found {
			return supported
		}
		if normalizedBaseModel := stripMaxEffortModelSuffix(normalized); normalizedBaseModel != normalized {
			if supported, found := resolveBoolModelMapping(rawSupport, normalizedBaseModel); found {
				return supported
			}
		}
	}
	return true
}

func hasBoolModelMapping(raw any) bool {
	switch mapping := raw.(type) {
	case map[string]bool:
		return len(mapping) > 0
	case map[string]any:
		for _, value := range mapping {
			if _, ok := value.(bool); ok {
				return true
			}
		}
	}
	return false
}

func resolveBoolModelMapping(raw any, requestedModel string) (bool, bool) {
	if requestedModel == "" {
		return false, false
	}
	get := func(pattern string) (bool, bool) {
		switch mapping := raw.(type) {
		case map[string]bool:
			supported, exists := mapping[pattern]
			return supported, exists
		case map[string]any:
			supported, exists := mapping[pattern].(bool)
			return supported, exists
		default:
			return false, false
		}
	}
	if supported, exists := get(requestedModel); exists {
		return supported, true
	}
	var (
		bestPattern string
		bestSupport bool
		found       bool
	)
	rangePatterns := func(pattern string, supported bool) {
		if matchWildcard(pattern, requestedModel) && (!found || len(pattern) > len(bestPattern) || (len(pattern) == len(bestPattern) && pattern < bestPattern)) {
			bestPattern = pattern
			bestSupport = supported
			found = true
		}
	}
	switch mapping := raw.(type) {
	case map[string]bool:
		for pattern, supported := range mapping {
			rangePatterns(pattern, supported)
		}
	case map[string]any:
		for pattern, value := range mapping {
			if supported, ok := value.(bool); ok {
				rangePatterns(pattern, supported)
			}
		}
	}
	return bestSupport, found
}

func stripMaxEffortModelSuffix(model string) string {
	trimmed := strings.TrimSpace(model)
	if trimmed == "" {
		return trimmed
	}
	separator := strings.LastIndexAny(trimmed, "-_")
	if separator < 0 || !strings.EqualFold(trimmed[separator+1:], "max") {
		return trimmed
	}
	return trimmed[:separator]
}

func accountSupportsRequestedMaxEffort(ctx context.Context, account *Account, requestedModel string) bool {
	effort := RequestedReasoningEffortFromContext(ctx)
	if effort == nil || NormalizeMaxReasoningEffort(*effort) != "max" {
		return true
	}
	if publicModel, ok := RequestedPublicModelFromContext(ctx); ok {
		return account.SupportsMaxReasoningEffort(publicModel)
	}
	return account.SupportsMaxReasoningEffort(requestedModel)
}

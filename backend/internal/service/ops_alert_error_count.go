package service

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

const OpsMetricUpstreamErrorCount = "upstream_error_count"

type OpsErrorCountFilters struct {
	StatusCodes    []int    `json:"status_codes"`
	ExcludedModels []string `json:"excluded_models"`
}

// Decode through JSON to accept both API values and strongly typed callers.
func ParseOpsErrorCountFilters(filters map[string]any) (*OpsErrorCountFilters, error) {
	out := &OpsErrorCountFilters{StatusCodes: []int{502, 503}, ExcludedModels: []string{}}
	raw, err := json.Marshal(filters)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return nil, fmt.Errorf("invalid error count filters: %w", err)
	}
	if len(out.StatusCodes) == 0 || len(out.StatusCodes) > 100 {
		return nil, fmt.Errorf("status_codes must contain 1 to 100 HTTP error codes")
	}
	seenCodes := map[int]bool{}
	codes := []int{}
	for _, code := range out.StatusCodes {
		if code < 400 || code > 599 {
			return nil, fmt.Errorf("status_codes must be between 400 and 599")
		}
		if !seenCodes[code] {
			seenCodes[code] = true
			codes = append(codes, code)
		}
	}
	sort.Ints(codes)
	out.StatusCodes = codes
	if len(out.ExcludedModels) > 200 {
		return nil, fmt.Errorf("at most 200 excluded models are allowed")
	}
	models := []string{}
	seenModels := map[string]bool{}
	for _, model := range out.ExcludedModels {
		model = strings.TrimSpace(model)
		if model == "" || len(model) > 256 {
			return nil, fmt.Errorf("excluded model names must contain 1 to 256 bytes")
		}
		if !seenModels[model] {
			seenModels[model] = true
			models = append(models, model)
		}
	}
	out.ExcludedModels = models
	return out, nil
}

func normalizeOpsErrorCountRule(rule *OpsAlertRule) error {
	if rule.MetricType != OpsMetricUpstreamErrorCount {
		return nil
	}
	if rule.Threshold < 0 || rule.Threshold > 1e9 || math.Trunc(rule.Threshold) != rule.Threshold {
		return fmt.Errorf("error count threshold must be an integer between 0 and 1000000000")
	}
	filters, err := ParseOpsErrorCountFilters(rule.Filters)
	if err != nil {
		return err
	}
	if rule.Filters == nil {
		rule.Filters = map[string]any{}
	}
	rule.Filters["status_codes"] = filters.StatusCodes
	rule.Filters["excluded_models"] = filters.ExcludedModels
	return nil
}

func opsErrorCountFilter(rule *OpsAlertRule, start, end time.Time, platform string, groupID *int64) (*OpsErrorLogFilter, error) {
	filters, err := ParseOpsErrorCountFilters(rule.Filters)
	if err != nil {
		return nil, err
	}
	return &OpsErrorLogFilter{
		StartTime: &start, EndTime: &end, Platform: platform, GroupID: groupID,
		Phase: "upstream", IncludeRecoveredUpstream: true, View: "errors",
		StatusCodes: filters.StatusCodes, ExcludedModels: filters.ExcludedModels,
	}, nil
}

func opsErrorCountDescription(rule *OpsAlertRule, counts map[int]int64) string {
	filters, err := ParseOpsErrorCountFilters(rule.Filters)
	if err != nil {
		return ""
	}
	parts := make([]string, 0, len(filters.StatusCodes))
	for _, code := range filters.StatusCodes {
		parts = append(parts, fmt.Sprintf("%d: %d", code, counts[code]))
	}
	description := "\nHTTP errors: " + strings.Join(parts, "; ")
	if len(filters.ExcludedModels) > 0 {
		description += "\nExcluded requested models: " + strings.Join(filters.ExcludedModels, ", ")
	}
	return description
}

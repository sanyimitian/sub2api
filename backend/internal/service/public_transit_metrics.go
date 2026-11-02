package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"reflect"
	"slices"
	"strings"
)

type PublicTransitMetricRange struct {
	Enabled bool     `json:"enabled"`
	Min     *float64 `json:"min,omitempty"`
	Max     *float64 `json:"max,omitempty"`
}

type PublicTransitMetricField struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
	Unit string `json:"unit"`
}

var publicTransitMetricNames = map[string]bool{
	"availability_7d": true, "availability_15d": true, "availability_30d": true,
	"avg_latency_7d_ms": true, "latest_latency_ms": true, "latest_ping_latency_ms": true,
	"latency_ms": true, "ping_latency_ms": true, "request_count": true,
	"success_count": true, "error_count": true, "success_rate": true,
	"avg_latency_ms": true, "avg_ttft_ms": true, "cache_hit_rate": true,
	"input_tokens": true, "output_tokens": true, "cache_creation_tokens": true,
	"cache_read_tokens": true, "success_requests": true, "error_requests": true,
	"token_count": true, "rpm": true, "tpm": true, "error_rate": true,
	"cache_rate": true, "cache_rate_numerator": true, "cache_rate_denominator": true,
	"sample_count": true, "p50_ms": true, "p90_ms": true, "p95_ms": true,
	"avg_ms": true, "upstream_affected_requests": true, "upstream_attempt_count": true,
	"score": true, "error_rate_score": true, "ttft_score": true, "cache_score": true,
}

func publicTransitMetricAllowed(path string) bool {
	parts := strings.Split(path, ".")
	if !publicTransitMetricNames[parts[len(parts)-1]] {
		return false
	}
	switch parts[0] {
	case "monitoring", "passive_monitoring":
		if len(parts) < 3 {
			return false
		}
		for _, part := range parts[1 : len(parts)-1] {
			if part == "thresholds" || part == "coverage" {
				return false
			}
		}
		return true
	case "groups":
		return len(parts) == 5 && parts[1] == "*" && parts[2] == "cache_usage"
	case "cache":
		return path == "cache.hit_rate"
	default:
		return false
	}
}

func PublicTransitMetricCatalog() []PublicTransitMetricField {
	fields := make(map[string]PublicTransitMetricField)
	collectPublicTransitMetricFields(reflect.TypeOf(PublicTransitSnapshot{}), "", fields)
	collectPublicTransitMetricFields(reflect.TypeOf(PublicTransitSnapshotV2{}), "", fields)
	result := make([]PublicTransitMetricField, 0, len(fields))
	for _, field := range fields {
		result = append(result, field)
	}
	slices.SortFunc(result, func(a, b PublicTransitMetricField) int { return strings.Compare(a.Path, b.Path) })
	return result
}

func collectPublicTransitMetricFields(typ reflect.Type, path string, fields map[string]PublicTransitMetricField) {
	for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice {
		if typ.Kind() == reflect.Slice {
			path += ".*"
		}
		typ = typ.Elem()
	}
	if typ.Kind() != reflect.Struct {
		isInteger := typ.Kind() >= reflect.Int && typ.Kind() <= reflect.Int64
		isDecimal := typ.Kind() == reflect.Float32 || typ.Kind() == reflect.Float64
		if (isInteger || isDecimal) && publicTransitMetricAllowed(path) {
			kind := "decimal"
			if isInteger {
				kind = "integer"
			}
			unit := "count"
			leaf := path[strings.LastIndex(path, ".")+1:]
			switch {
			case strings.HasSuffix(leaf, "_ms"):
				unit = "ms"
			case strings.HasPrefix(leaf, "availability_") || strings.HasPrefix(path, "groups.*.cache_usage.") && leaf == "cache_hit_rate" || path == "cache.hit_rate":
				unit = "%"
			case strings.HasSuffix(leaf, "_rate"):
				unit = "0-1"
			case leaf == "rpm":
				unit = "req/min"
			case leaf == "tpm":
				unit = "tokens/min"
			case strings.HasSuffix(leaf, "score"):
				unit = "0-100"
			}
			fields[path] = PublicTransitMetricField{Path: path, Kind: kind, Unit: unit}
		}
		return
	}
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if !field.IsExported() {
			continue
		}
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name == "" || name == "-" {
			continue
		}
		childPath := name
		if path != "" {
			childPath = path + "." + name
		}
		if strings.Contains(childPath, ".thresholds.") || strings.Contains(childPath, ".coverage.") {
			continue
		}
		collectPublicTransitMetricFields(field.Type, childPath, fields)
	}
}

func ValidatePublicTransitMetricRanges(ranges map[string]PublicTransitMetricRange) error {
	catalog := make(map[string]PublicTransitMetricField)
	for _, field := range PublicTransitMetricCatalog() {
		catalog[field.Path] = field
	}
	for path, value := range ranges {
		field, ok := catalog[path]
		if !ok {
			return fmt.Errorf("unknown public transit metric: %s", path)
		}
		if !value.Enabled || value.Min == nil || value.Max == nil {
			continue
		}
		min, max := *value.Min, *value.Max
		if math.IsNaN(min) || math.IsNaN(max) || math.IsInf(min, 0) || math.IsInf(max, 0) || min < 0 || max > 1e15 || min >= max {
			return fmt.Errorf("invalid range for %s: expected 0 <= min < max <= 1e15", path)
		}
		if field.Kind == "integer" && (math.Trunc(min) != min || math.Trunc(max) != max) {
			return fmt.Errorf("integer bounds required for %s", path)
		}
		if field.Unit == "%" && max > 100 || field.Unit == "0-1" && max > 1 || field.Unit == "0-100" && max > 100 {
			return fmt.Errorf("range for %s exceeds its unit maximum", path)
		}
		if field.Kind == "decimal" && math.Nextafter(min, math.Inf(1)) > max {
			return fmt.Errorf("range for %s has fewer than two representable values", path)
		}
	}
	return nil
}

func (s *PublicTransitService) ApplyPublicTransitMetricRanges(_ context.Context, snapshot map[string]any, ranges map[string]PublicTransitMetricRange) error {
	if len(ranges) == 0 {
		return nil
	}
	if err := ValidatePublicTransitMetricRanges(ranges); err != nil {
		return err
	}
	active := false
	for _, value := range ranges {
		if value.Enabled && value.Min != nil && value.Max != nil {
			active = true
			break
		}
	}
	if !active {
		return nil
	}
	kinds := make(map[string]string)
	for _, field := range PublicTransitMetricCatalog() {
		kinds[field.Path] = field.Kind
	}
	applyPublicTransitMetricRanges(snapshot, "", ranges, kinds)
	return nil
}

func applyPublicTransitMetricRanges(value any, path string, ranges map[string]PublicTransitMetricRange, kinds map[string]string) {
	switch item := value.(type) {
	case map[string]any:
		for key, child := range item {
			childPath := key
			if path != "" {
				childPath = path + "." + key
			}
			if rule, ok := ranges[childPath]; ok && rule.Enabled && rule.Min != nil && rule.Max != nil {
				if _, ok := child.(json.Number); ok {
					if kinds[childPath] == "integer" {
						item[key] = publicTransitRandomInteger(int64(*rule.Min), int64(*rule.Max))
					} else {
						item[key] = publicTransitRandomDecimal(*rule.Min, *rule.Max)
					}
				}
				continue
			}
			applyPublicTransitMetricRanges(child, childPath, ranges, kinds)
		}
	case []any:
		for _, child := range item {
			applyPublicTransitMetricRanges(child, path+".*", ranges, kinds)
		}
	}
}

func publicTransitRandomInteger(min, max int64) int64 {
	return min + rand.Int63n(max-min+1)
}

func publicTransitRandomDecimal(min, max float64) float64 {
	return min + (max-min)*rand.Float64()
}

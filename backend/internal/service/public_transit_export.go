package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// PublicTransitExportSections controls optional top-level snapshot sections.
// Identity and endpoint fields always remain available for discovery.
type PublicTransitExportSections struct {
	Billing           bool                               `json:"billing"`
	Groups            bool                               `json:"groups"`
	Monitoring        bool                               `json:"monitoring"`
	PassiveMonitoring bool                               `json:"passive_monitoring"`
	Cache             bool                               `json:"cache"`
	Disclosure        bool                               `json:"disclosure"`
	Limits            bool                               `json:"limits"`
	Completeness      bool                               `json:"completeness"`
	Metrics           map[string]PublicTransitMetricRange `json:"metrics,omitempty"`
}

func DefaultPublicTransitExportSections() PublicTransitExportSections {
	return PublicTransitExportSections{
		Billing: true, Groups: true, Monitoring: true, PassiveMonitoring: true,
		Cache: true, Disclosure: true, Limits: true, Completeness: true,
	}
}

func (s *SettingService) GetPublicTransitExportSections(ctx context.Context) (PublicTransitExportSections, error) {
	values, err := s.settingRepo.GetMultiple(ctx, []string{SettingKeyPublicTransitExportSections})
	if err != nil {
		return PublicTransitExportSections{}, err
	}
	raw := values[SettingKeyPublicTransitExportSections]
	if raw == "" {
		return DefaultPublicTransitExportSections(), nil
	}
	var sections PublicTransitExportSections
	if err := json.Unmarshal([]byte(raw), &sections); err != nil {
		return sections, fmt.Errorf("decode public transit export sections: %w", err)
	}
	return sections, nil
}

func (s *SettingService) SetPublicTransitExportSections(ctx context.Context, sections PublicTransitExportSections) error {
	if err := ValidatePublicTransitMetricRanges(sections.Metrics); err != nil {
		return infraerrors.BadRequest("INVALID_PUBLIC_TRANSIT_METRIC_RANGE", err.Error())
	}
	raw, err := json.Marshal(sections)
	if err != nil {
		return err
	}
	return s.settingRepo.SetMultiple(ctx, map[string]string{SettingKeyPublicTransitExportSections: string(raw)})
}

func (s *PublicTransitService) GetExportSections(ctx context.Context) (PublicTransitExportSections, error) {
	return s.settingService.GetPublicTransitExportSections(ctx)
}

func (s *PublicTransitService) SetExportSections(ctx context.Context, sections PublicTransitExportSections) error {
	return s.settingService.SetPublicTransitExportSections(ctx, sections)
}

func (s *PublicTransitService) PublicSnapshot(ctx context.Context, snapshot any) (map[string]any, error) {
	sections, err := s.GetExportSections(ctx)
	if err != nil {
		return nil, err
	}
	result, err := FilterPublicTransitSnapshot(snapshot, sections)
	if err != nil {
		return nil, err
	}
	if err := s.ApplyPublicTransitMetricRanges(ctx, result, sections.Metrics); err != nil {
		return nil, err
	}
	return result, nil
}

func FilterPublicTransitSnapshot(snapshot any, sections PublicTransitExportSections) (map[string]any, error) {
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&result); err != nil {
		return nil, err
	}
	for key, enabled := range map[string]bool{
		"billing": sections.Billing,
		"groups": sections.Groups,
		"monitoring": sections.Monitoring,
		"passive_monitoring": sections.PassiveMonitoring,
		"cache": sections.Cache,
		"disclosure": sections.Disclosure,
		"limits": sections.Limits,
		"completeness": sections.Completeness,
	} {
		if !enabled {
			delete(result, key)
		}
	}
	if !sections.Cache {
		stripPublicTransitCacheFields(result)
	}
	return result, nil
}

func stripPublicTransitCacheFields(value any) {
	switch item := value.(type) {
	case map[string]any:
		for key, child := range item {
			if key == "cache" || key == "cache_usage" || strings.HasPrefix(key, "cache_") {
				delete(item, key)
				continue
			}
			stripPublicTransitCacheFields(child)
		}
	case []any:
		for _, child := range item {
			stripPublicTransitCacheFields(child)
		}
	}
}

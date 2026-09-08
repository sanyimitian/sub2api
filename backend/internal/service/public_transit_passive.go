package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// PublicTransitV2SchemaVersion identifies the passive-monitoring contract.
// V1 remains the synthetic/channel-monitor contract and is intentionally kept
// wire-compatible for existing crawlers.
const PublicTransitV2SchemaVersion = "ai-transit.v2"

// PublicTransitPassiveSnapshotPath is the versioned passive aggregation
// endpoint. It is separate from the V1 endpoint so consumers never have to
// guess whether a metric came from synthetic probes or real traffic.
const PublicTransitPassiveSnapshotPath = "/api/public/transit/v2/snapshot"

type PublicTransitPassiveRange string

const (
	PublicTransitPassiveRange90m PublicTransitPassiveRange = "90m"
	PublicTransitPassiveRange24h PublicTransitPassiveRange = "24h"
	PublicTransitPassiveRange7d  PublicTransitPassiveRange = "7d"
	PublicTransitPassiveRange30d PublicTransitPassiveRange = "30d"
)

func normalizePublicTransitPassiveRange(raw string) PublicTransitPassiveRange {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(PublicTransitPassiveRange90m):
		return PublicTransitPassiveRange90m
	case string(PublicTransitPassiveRange7d):
		return PublicTransitPassiveRange7d
	case string(PublicTransitPassiveRange30d):
		return PublicTransitPassiveRange30d
	default:
		return PublicTransitPassiveRange24h
	}
}

func publicTransitPassiveRangeDuration(value PublicTransitPassiveRange) time.Duration {
	switch value {
	case PublicTransitPassiveRange90m:
		return 90 * time.Minute
	case PublicTransitPassiveRange7d:
		return 7 * 24 * time.Hour
	case PublicTransitPassiveRange30d:
		return 30 * 24 * time.Hour
	default:
		return 24 * time.Hour
	}
}

func publicTransitPassiveBucketSeconds(value PublicTransitPassiveRange) int64 {
	switch value {
	case PublicTransitPassiveRange90m:
		return int64((5 * time.Minute) / time.Second)
	case PublicTransitPassiveRange7d:
		return int64((6 * time.Hour) / time.Second)
	case PublicTransitPassiveRange30d:
		return int64((24 * time.Hour) / time.Second)
	default:
		return int64(time.Hour / time.Second)
	}
}

// PublicTransitPassiveMonitorRepository is an optional read-only repository
// extension. Keeping it separate from OpsRepository avoids breaking existing
// repository mocks and allows deployments without the Ops tables to continue
// serving the V1 snapshot.
type PublicTransitPassiveMonitorRepository interface {
	GetPublicTransitPassiveAggregates(ctx context.Context, since, until time.Time, bucketSeconds int64) ([]PublicTransitPassiveAggregate, error)
}

// PublicTransitPassiveAggregate is an internal, privacy-safe aggregate row.
// It deliberately contains no user, account, API key, channel, or IP data.
type PublicTransitPassiveAggregate struct {
	BucketStart    *time.Time
	Platform       string
	GroupID        *int64
	GroupName      string // internal lookup only; never serialized
	Model          string
	RequestCount   int64
	SuccessCount   int64
	ErrorCount     int64
	TotalLatencyMs int64
	LatencySamples int64
	TotalTTFTMs    int64
	TTFTSamples    int64
	InputTokens    int64
	CacheCreate    int64
	CacheRead      int64
	LastRequestAt  *time.Time
}

type PublicTransitSnapshotV2 struct {
	SchemaVersion  string                         `json:"schema_version"`
	System         string                         `json:"system"`
	GeneratedAt    string                         `json:"generated_at"`
	MonitoringMode string                         `json:"monitoring_mode,omitempty"`
	Station        PublicTransitStation           `json:"station"`
	Billing        PublicTransitBilling           `json:"billing"`
	Groups         []PublicTransitGroup           `json:"groups"`
	Monitoring     []PublicTransitMonitor         `json:"monitoring"`
	Passive        PublicTransitPassiveDisclosure `json:"passive_monitoring"`
	Cache          PublicTransitCacheDisclosure   `json:"cache"`
	Disclosure     PublicTransitSourceDisclosure  `json:"disclosure"`
	Limits         PublicTransitLimits            `json:"limits"`
	Completeness   PublicTransitCompleteness      `json:"completeness"`
	Endpoints      PublicTransitEndpoints         `json:"endpoints"`
}

// PublicTransitPassiveDisclosure contains only aggregated real-traffic
// measurements. A nil pointer is used for unavailable latency/TTFT values so
// clients can distinguish “no samples” from a measured zero.
type PublicTransitPassiveDisclosure struct {
	Enabled  bool                        `json:"enabled"`
	Mode     string                      `json:"mode"`
	Source   string                      `json:"source"`
	Window   PublicTransitPassiveWindow  `json:"window"`
	Models   []PublicTransitPassiveModel `json:"models"`
	Groups   []PublicTransitPassiveGroup `json:"groups"`
	Warnings []string                    `json:"warnings,omitempty"`
	// Matrix is the same anonymous V2 matrix used by the internal monitoring
	// page. It is additive so existing public consumers can keep reading the
	// legacy window/groups fields while the UI migrates to the shared component.
	Matrix *PublicTransitPassiveMatrix `json:"matrix,omitempty"`
}

type PublicTransitPassiveMatrix struct {
	GroupBy  ChannelMonitorV2GroupBy     `json:"group_by"`
	Coverage ChannelMonitorV2Coverage    `json:"coverage"`
	Items    []ChannelMonitorV2MatrixRow `json:"items"`
}

type PublicTransitPassiveWindow struct {
	Start         string                  `json:"start"`
	End           string                  `json:"end"`
	Period        string                  `json:"period"`
	BucketSeconds int64                   `json:"bucket_seconds"`
	RequestCount  int64                   `json:"request_count"`
	SuccessCount  int64                   `json:"success_count"`
	ErrorCount    int64                   `json:"error_count"`
	SuccessRate   float64                 `json:"success_rate"`
	AvgLatencyMs  *int                    `json:"avg_latency_ms,omitempty"`
	AvgTTFTMs     *int                    `json:"avg_ttft_ms,omitempty"`
	Metrics       *ChannelMonitorV2Metric `json:"metrics,omitempty"`
	Health        *ChannelMonitorV2Health `json:"health,omitempty"`
}

type PublicTransitPassiveBucket struct {
	Start        string                  `json:"start"`
	RequestCount int64                   `json:"request_count"`
	SuccessCount int64                   `json:"success_count"`
	ErrorCount   int64                   `json:"error_count"`
	SuccessRate  float64                 `json:"success_rate"`
	AvgLatencyMs *int                    `json:"avg_latency_ms,omitempty"`
	AvgTTFTMs    *int                    `json:"avg_ttft_ms,omitempty"`
	CacheHitRate float64                 `json:"cache_hit_rate"`
	Metrics      *ChannelMonitorV2Metric `json:"metrics,omitempty"`
	Health       *ChannelMonitorV2Health `json:"health,omitempty"`
}

type PublicTransitPassiveModel struct {
	Model         string                  `json:"model"`
	Platform      string                  `json:"platform"`
	GroupName     string                  `json:"group_name,omitempty"`
	RequestCount  int64                   `json:"request_count"`
	SuccessCount  int64                   `json:"success_count"`
	ErrorCount    int64                   `json:"error_count"`
	SuccessRate   float64                 `json:"success_rate"`
	AvgLatencyMs  *int                    `json:"avg_latency_ms,omitempty"`
	AvgTTFTMs     *int                    `json:"avg_ttft_ms,omitempty"`
	LastRequestAt string                  `json:"last_request_at,omitempty"`
	Metrics       *ChannelMonitorV2Metric `json:"metrics,omitempty"`
	Health        *ChannelMonitorV2Health `json:"health,omitempty"`
}

type PublicTransitPassiveGroup struct {
	Name          string                       `json:"name"`
	Platform      string                       `json:"platform"`
	RequestCount  int64                        `json:"request_count"`
	SuccessCount  int64                        `json:"success_count"`
	ErrorCount    int64                        `json:"error_count"`
	SuccessRate   float64                      `json:"success_rate"`
	AvgLatencyMs  *int                         `json:"avg_latency_ms,omitempty"`
	AvgTTFTMs     *int                         `json:"avg_ttft_ms,omitempty"`
	LastRequestAt string                       `json:"last_request_at,omitempty"`
	Buckets       []PublicTransitPassiveBucket `json:"buckets"`
	Metrics       *ChannelMonitorV2Metric      `json:"metrics,omitempty"`
	Health        *ChannelMonitorV2Health      `json:"health,omitempty"`
}

// SetChannelMonitorV2Service connects the public page to the exact internal
// V2 aggregation service. The service is read-only here; this setter keeps
// NewPublicTransitService backwards-compatible for tests/integrations.
func (s *PublicTransitService) SetChannelMonitorV2Service(monitor *ChannelMonitorV2Service) {
	if s != nil {
		s.monitorV2 = monitor
	}
}

// SetPassiveMonitorRepository enables V2 on installations whose Ops
// repository exposes the optional aggregate query. It is intentionally a
// setter to preserve the public constructor used by existing integrations and
// unit tests.
func (s *PublicTransitService) SetPassiveMonitorRepository(repo PublicTransitPassiveMonitorRepository) {
	if s != nil {
		s.passiveRepo = repo
	}
}

func (s *PublicTransitService) SnapshotV2(ctx context.Context, baseURL string) (*PublicTransitSnapshotV2, error) {
	return s.SnapshotV2Range(ctx, baseURL, string(PublicTransitPassiveRange24h))
}

// SnapshotV2Range returns the same privacy-safe V2 contract for a bounded
// public window. The range is deliberately allow-listed so clients cannot
// turn the public endpoint into an arbitrary historical query.
func (s *PublicTransitService) SnapshotV2Range(ctx context.Context, baseURL, rawRange string) (*PublicTransitSnapshotV2, error) {
	if !s.Enabled(ctx) {
		return nil, infraerrors.NotFound("PUBLIC_TRANSIT_DISABLED", "public transit snapshot is disabled")
	}

	// Reuse the V1 assembly for station, pricing, groups, and cache. The
	// shared snapshot intentionally leaves active monitor rows empty when the
	// runtime selects passive mode, so the two monitoring modes stay mutually
	// exclusive in every public contract.
	v1, err := s.Snapshot(ctx, baseURL)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	monitorRuntime := s.settingService.GetChannelMonitorRuntime(ctx)
	period := normalizePublicTransitPassiveRange(rawRange)
	windowStart := now.Add(-publicTransitPassiveRangeDuration(period))
	passive := PublicTransitPassiveDisclosure{
		Enabled: false,
		Mode:    "passive",
		Source:  "real_traffic_aggregates",
		Window: PublicTransitPassiveWindow{
			Start:         windowStart.Format(time.RFC3339),
			End:           now.Format(time.RFC3339),
			Period:        "last_" + string(period),
			BucketSeconds: publicTransitPassiveBucketSeconds(period),
		},
		Models: []PublicTransitPassiveModel{},
		Groups: []PublicTransitPassiveGroup{},
	}

	if !monitorRuntime.PassiveAggregationAllowed() {
		passive.Warnings = []string{"passive monitoring is disabled because active probe mode is selected"}
	} else if s.passiveRepo == nil && s.monitorV2 == nil {
		passive.Warnings = []string{"passive aggregation is unavailable on this deployment"}
	} else {
		var rows []PublicTransitPassiveAggregate
		var rawErr error
		if s.passiveRepo != nil {
			rows, rawErr = s.passiveRepo.GetPublicTransitPassiveAggregates(ctx, windowStart, now, passive.Window.BucketSeconds)
		}
		if rawErr == nil {
			passive = buildPublicTransitPassiveDisclosure(rows, passive.Window)
		}

		// Feed the public contract from the exact V2 aggregation service as well.
		// This keeps the public matrix, health scores, percentiles and tooltips in
		// lockstep with the internal page. All absolute volume fields are redacted
		// before serialization; rates, latency percentiles and RPM/TPM remain.
		if s.monitorV2 != nil {
			publicGroupIDs := make([]int64, 0, len(v1.Groups))
			for _, group := range v1.Groups {
				if group.ID > 0 {
					publicGroupIDs = append(publicGroupIDs, group.ID)
				}
			}
			filter, filterErr := s.monitorV2.ParseFilter(string(period), nil, nil, publicGroupIDs)
			if filterErr == nil {
				snapshot, snapshotErr := s.monitorV2.Snapshot(ctx, filter, true)
				matrix, matrixErr := s.monitorV2.Matrix(ctx, filter, ChannelMonitorV2GroupByPlatformGroup, true)
				modelMatrix, _ := s.monitorV2.Matrix(ctx, filter, ChannelMonitorV2GroupByPlatformGroupModel, true)
				if snapshotErr == nil && matrixErr == nil && snapshot != nil && matrix != nil {
					augmentPublicTransitPassiveDisclosure(&passive, snapshot, matrix, modelMatrix)
				} else if rawErr != nil {
					if snapshotErr != nil {
						return nil, fmt.Errorf("load passive monitor snapshot: %w", snapshotErr)
					}
					if matrixErr != nil {
						return nil, fmt.Errorf("load passive monitor matrix: %w", matrixErr)
					}
				}
			}
		}
		if rawErr != nil && s.monitorV2 == nil {
			return nil, fmt.Errorf("load passive monitor aggregates: %w", rawErr)
		}
		if !passive.Enabled {
			passive.Warnings = []string{"no public real-traffic samples in the selected window"}
		}
	}

	completeness := v1.Completeness
	if passive.Enabled {
		completeness.Warnings = append(completeness.Warnings, "passive monitoring is aggregated from real traffic; synthetic probes are disabled in this mode")
	}
	// Advertise the same mode-aware endpoint as V1. Keep the versioned V2 path
	// in snapshot_v2_url for older consumers that still pin a schema version.
	endpoints := v1.Endpoints
	endpoints.SnapshotURL = absoluteURL(baseURL, PublicTransitSnapshotPath)
	endpoints.SnapshotV2URL = absoluteURL(baseURL, PublicTransitPassiveSnapshotPath)

	return &PublicTransitSnapshotV2{
		SchemaVersion:  PublicTransitV2SchemaVersion,
		System:         v1.System,
		GeneratedAt:    now.Format(time.RFC3339),
		MonitoringMode: monitorRuntime.Mode,
		Station:        v1.Station,
		Billing:        v1.Billing,
		Groups:         v1.Groups,
		Monitoring:     v1.Monitoring,
		Passive:        passive,
		Cache:          v1.Cache,
		Disclosure:     v1.Disclosure,
		Limits:         v1.Limits,
		Completeness:   completeness,
		Endpoints:      endpoints,
	}, nil
}

func augmentPublicTransitPassiveDisclosure(dst *PublicTransitPassiveDisclosure, snapshot *ChannelMonitorV2Snapshot, matrix *ChannelMonitorV2Matrix, modelMatrix *ChannelMonitorV2Matrix) {
	if dst == nil || snapshot == nil || matrix == nil {
		return
	}
	dst.Enabled = len(matrix.Items) > 0 || snapshot.Metrics.RequestCount > 0
	dst.Matrix = &PublicTransitPassiveMatrix{GroupBy: matrix.GroupBy, Coverage: matrix.Coverage, Items: make([]ChannelMonitorV2MatrixRow, 0, len(matrix.Items))}
	allowedGroups := make(map[string]struct{}, len(dst.Groups))
	for _, group := range dst.Groups {
		allowedGroups[group.Platform+"\x00"+strings.TrimSpace(group.Name)] = struct{}{}
	}
	for _, item := range matrix.Items {
		if _, ok := allowedGroups[item.Platform+"\x00"+strings.TrimSpace(item.GroupName)]; !ok {
			continue
		}
		// Group IDs are internal routing identifiers; the public page only needs
		// the stable display name and platform for filtering/rendering.
		item.GroupID = nil
		item.Metrics = publicTransitMetric(item.Metrics)
		for i := range item.Buckets {
			item.Buckets[i].Metrics = publicTransitMetric(item.Buckets[i].Metrics)
		}
		dst.Matrix.Items = append(dst.Matrix.Items, item)
	}
	dst.Window.Metrics = publicTransitPtr(publicTransitMetric(snapshot.Metrics))
	dst.Window.Health = publicTransitPtr(snapshot.Health)
	dst.Window.Start = snapshot.Coverage.RequestedStart.Format(time.RFC3339)
	dst.Window.End = snapshot.Coverage.RequestedEnd.Format(time.RFC3339)
	dst.Window.BucketSeconds = int64(snapshot.Coverage.BucketSeconds)
	dst.Window.SuccessRate = snapshot.Metrics.SuccessRate
	dst.Window.AvgLatencyMs = publicLatencyInt(snapshot.Metrics.Duration.AvgMs)
	dst.Window.AvgTTFTMs = publicLatencyInt(snapshot.Metrics.TTFT.AvgMs)

	for i := range dst.Groups {
		for _, item := range dst.Matrix.Items {
			if item.Platform != dst.Groups[i].Platform || strings.TrimSpace(item.GroupName) != strings.TrimSpace(dst.Groups[i].Name) {
				continue
			}
			metrics := item.Metrics
			health := item.Health
			dst.Groups[i].Metrics = &metrics
			dst.Groups[i].Health = &health
			dst.Groups[i].Buckets = make([]PublicTransitPassiveBucket, 0, len(item.Buckets))
			for _, bucket := range item.Buckets {
				dst.Groups[i].Buckets = append(dst.Groups[i].Buckets, PublicTransitPassiveBucket{
					Start:        bucket.BucketStart.Format(time.RFC3339),
					RequestCount: bucket.Metrics.RequestCount,
					SuccessCount: bucket.Metrics.SuccessRequests,
					ErrorCount:   bucket.Metrics.ErrorRequests,
					SuccessRate:  bucket.Metrics.SuccessRate,
					AvgLatencyMs: publicLatencyInt(bucket.Metrics.Duration.AvgMs),
					AvgTTFTMs:    publicLatencyInt(bucket.Metrics.TTFT.AvgMs),
					CacheHitRate: bucket.Metrics.CacheRate,
					Metrics:      publicTransitPtr(publicTransitMetric(bucket.Metrics)),
					Health:       publicTransitPtr(bucket.Health),
				})
			}
		}
	}
	if modelMatrix != nil {
		// Model rows come from the same group/model matrix, so private or
		// exclusive groups filtered above can never be folded into a public row.
		for i := range dst.Models {
			for _, item := range modelMatrix.Items {
				if _, ok := allowedGroups[item.Platform+"\x00"+strings.TrimSpace(item.GroupName)]; !ok {
					continue
				}
				if item.Platform != dst.Models[i].Platform || item.Model != dst.Models[i].Model || strings.TrimSpace(item.GroupName) != strings.TrimSpace(dst.Models[i].GroupName) {
					continue
				}
				metrics := publicTransitMetric(item.Metrics)
				health := item.Health
				dst.Models[i].Metrics = &metrics
				dst.Models[i].Health = &health
			}
		}
	}
}

func publicTransitMetric(value ChannelMonitorV2Metric) ChannelMonitorV2Metric {
	value.SuccessRequests = 0
	value.ErrorRequests = 0
	value.RequestCount = 0
	value.InputTokens = 0
	value.OutputTokens = 0
	value.CacheCreationTokens = 0
	value.CacheReadTokens = 0
	value.TokenCount = 0
	value.CacheRateNumerator = 0
	value.CacheRateDenominator = 0
	value.TTFT.SampleCount = 0
	value.Duration.SampleCount = 0
	value.UpstreamAffectedRequests = nil
	value.UpstreamAttemptCount = nil
	return value
}

func publicLatencyInt(value *float64) *int {
	if value == nil {
		return nil
	}
	v := int(*value)
	return &v
}

func publicTransitPtr[T any](value T) *T { return &value }

func buildPublicTransitPassiveDisclosure(rows []PublicTransitPassiveAggregate, window PublicTransitPassiveWindow) PublicTransitPassiveDisclosure {
	type key struct{ platform, model, groupName string }
	type groupKey struct{ platform, name string }

	modelRows := make(map[key]*PublicTransitPassiveAggregate)
	groupRows := make(map[groupKey]*PublicTransitPassiveAggregate)
	groupBucketRows := make(map[groupKey]map[string]*PublicTransitPassiveAggregate)
	for _, row := range rows {
		platform := strings.TrimSpace(row.Platform)
		model := strings.TrimSpace(row.Model)
		if platform == "" || model == "" || row.RequestCount < 0 {
			continue
		}
		groupName := strings.TrimSpace(row.GroupName)
		if groupName == "" {
			groupName = "unassigned"
		}
		mk := key{platform: platform, model: model, groupName: groupName}
		modelRows[mk] = mergePassiveAggregate(modelRows[mk], row)
		gk := groupKey{platform: platform, name: groupName}
		groupRows[gk] = mergePassiveAggregate(groupRows[gk], row)
		if row.BucketStart != nil {
			bucketKey := row.BucketStart.UTC().Format(time.RFC3339)
			if groupBucketRows[gk] == nil {
				groupBucketRows[gk] = make(map[string]*PublicTransitPassiveAggregate)
			}
			groupBucketRows[gk][bucketKey] = mergePassiveAggregate(groupBucketRows[gk][bucketKey], row)
		}
	}

	out := PublicTransitPassiveDisclosure{
		Enabled: len(modelRows) > 0,
		Mode:    "passive",
		Source:  "real_traffic_aggregates",
		Window:  window,
		Models:  make([]PublicTransitPassiveModel, 0, len(modelRows)),
		Groups:  make([]PublicTransitPassiveGroup, 0, len(groupRows)),
	}
	var windowAgg *PublicTransitPassiveAggregate
	for k, row := range modelRows {
		m := passiveModelFromAggregate(k.platform, k.model, k.groupName, row)
		out.Models = append(out.Models, m)
		windowAgg = mergePassiveAggregate(windowAgg, *row)
	}
	if windowAgg != nil {
		out.Window.RequestCount = windowAgg.RequestCount
		out.Window.SuccessCount = windowAgg.SuccessCount
		out.Window.ErrorCount = windowAgg.ErrorCount
		out.Window.SuccessRate = passiveSuccessRate(windowAgg.SuccessCount, windowAgg.ErrorCount)
		out.Window.AvgLatencyMs = passiveAverage(windowAgg.TotalLatencyMs, windowAgg.LatencySamples)
		out.Window.AvgTTFTMs = passiveAverage(windowAgg.TotalTTFTMs, windowAgg.TTFTSamples)
	}
	for k, row := range groupRows {
		group := passiveGroupFromAggregate(k.platform, k.name, row)
		bucketRows := groupBucketRows[k]
		bucketKeys := make([]string, 0, len(bucketRows))
		for bucketStart := range bucketRows {
			bucketKeys = append(bucketKeys, bucketStart)
		}
		sort.Strings(bucketKeys)
		group.Buckets = make([]PublicTransitPassiveBucket, 0, len(bucketKeys))
		for _, bucketStart := range bucketKeys {
			group.Buckets = append(group.Buckets, passiveBucketFromAggregate(bucketStart, bucketRows[bucketStart]))
		}
		out.Groups = append(out.Groups, group)
	}
	sort.Slice(out.Models, func(i, j int) bool {
		if out.Models[i].Platform == out.Models[j].Platform {
			if strings.EqualFold(out.Models[i].Model, out.Models[j].Model) {
				return strings.ToLower(out.Models[i].GroupName) < strings.ToLower(out.Models[j].GroupName)
			}
			return strings.ToLower(out.Models[i].Model) < strings.ToLower(out.Models[j].Model)
		}
		return out.Models[i].Platform < out.Models[j].Platform
	})
	sort.Slice(out.Groups, func(i, j int) bool {
		if out.Groups[i].Platform == out.Groups[j].Platform {
			return strings.ToLower(out.Groups[i].Name) < strings.ToLower(out.Groups[j].Name)
		}
		return out.Groups[i].Platform < out.Groups[j].Platform
	})
	return out
}

func mergePassiveAggregate(dst *PublicTransitPassiveAggregate, src PublicTransitPassiveAggregate) *PublicTransitPassiveAggregate {
	if dst == nil {
		copy := src
		return &copy
	}
	dst.RequestCount += src.RequestCount
	dst.SuccessCount += src.SuccessCount
	dst.ErrorCount += src.ErrorCount
	dst.TotalLatencyMs += src.TotalLatencyMs
	dst.LatencySamples += src.LatencySamples
	dst.TotalTTFTMs += src.TotalTTFTMs
	dst.TTFTSamples += src.TTFTSamples
	dst.InputTokens += src.InputTokens
	dst.CacheCreate += src.CacheCreate
	dst.CacheRead += src.CacheRead
	if dst.LastRequestAt == nil || (src.LastRequestAt != nil && src.LastRequestAt.After(*dst.LastRequestAt)) {
		dst.LastRequestAt = src.LastRequestAt
	}
	return dst
}

func passiveModelFromAggregate(platform, model, groupName string, row *PublicTransitPassiveAggregate) PublicTransitPassiveModel {
	return PublicTransitPassiveModel{
		Model:         model,
		Platform:      platform,
		GroupName:     groupName,
		RequestCount:  row.RequestCount,
		SuccessCount:  row.SuccessCount,
		ErrorCount:    row.ErrorCount,
		SuccessRate:   passiveSuccessRate(row.SuccessCount, row.ErrorCount),
		AvgLatencyMs:  passiveAverage(row.TotalLatencyMs, row.LatencySamples),
		AvgTTFTMs:     passiveAverage(row.TotalTTFTMs, row.TTFTSamples),
		LastRequestAt: passiveTime(row.LastRequestAt),
	}
}

func passiveGroupFromAggregate(platform, name string, row *PublicTransitPassiveAggregate) PublicTransitPassiveGroup {
	return PublicTransitPassiveGroup{
		Name:          name,
		Platform:      platform,
		RequestCount:  row.RequestCount,
		SuccessCount:  row.SuccessCount,
		ErrorCount:    row.ErrorCount,
		SuccessRate:   passiveSuccessRate(row.SuccessCount, row.ErrorCount),
		AvgLatencyMs:  passiveAverage(row.TotalLatencyMs, row.LatencySamples),
		AvgTTFTMs:     passiveAverage(row.TotalTTFTMs, row.TTFTSamples),
		LastRequestAt: passiveTime(row.LastRequestAt),
		Buckets:       []PublicTransitPassiveBucket{},
	}
}

func passiveBucketFromAggregate(start string, row *PublicTransitPassiveAggregate) PublicTransitPassiveBucket {
	return PublicTransitPassiveBucket{
		Start:        start,
		RequestCount: row.RequestCount,
		SuccessCount: row.SuccessCount,
		ErrorCount:   row.ErrorCount,
		SuccessRate:  passiveSuccessRate(row.SuccessCount, row.ErrorCount),
		AvgLatencyMs: passiveAverage(row.TotalLatencyMs, row.LatencySamples),
		AvgTTFTMs:    passiveAverage(row.TotalTTFTMs, row.TTFTSamples),
		CacheHitRate: passiveCacheHitRate(row.InputTokens, row.CacheCreate, row.CacheRead),
	}
}

func passiveSuccessRate(success, errors int64) float64 {
	denom := success + errors
	if denom <= 0 {
		return 0
	}
	return float64(success) / float64(denom)
}

func passiveAverage(total, samples int64) *int {
	if samples <= 0 {
		return nil
	}
	v := int(total / samples)
	return &v
}

func passiveCacheHitRate(input, created, read int64) float64 {
	total := input + created + read
	if total <= 0 {
		return 0
	}
	return float64(read) / float64(total)
}

func passiveTime(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

//go:build unit

package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/stretchr/testify/require"
)

type publicTransitMonitorRepoStub struct {
	ChannelMonitorRepository
}

func (*publicTransitMonitorRepoStub) ListEnabled(context.Context) ([]*ChannelMonitor, error) {
	return nil, nil
}

func TestPublicTransitSnapshotAuto_ModesRemainExclusive(t *testing.T) {
	repo := &settingPublicRepoStub{values: map[string]string{
		SettingKeyChannelMonitorEnabled: "true",
		SettingKeyChannelMonitorMode:    ChannelMonitorModeV1,
	}}
	settings := NewSettingService(repo, &config.Config{})
	groups := &stubGroupRepoForAvailable{}
	channels := NewChannelService(&mockChannelRepository{listAllFn: func(context.Context) ([]Channel, error) {
		return nil, nil
	}}, groups, nil, nil, nil)
	svc := NewPublicTransitService(channels, NewChannelMonitorService(&publicTransitMonitorRepoStub{}, nil), settings,
		NewPaymentConfigService(nil, repo, nil), groups, nil)
	for _, mode := range []string{ChannelMonitorModeV1, ChannelMonitorModeV2} {
		t.Run(mode, func(t *testing.T) {
			repo.values[SettingKeyChannelMonitorMode] = mode
			payload, err := svc.SnapshotAuto(context.Background(), "https://example.test", "24h")
			require.NoError(t, err)
			if mode == ChannelMonitorModeV1 {
				v1, ok := payload.(*PublicTransitSnapshot)
				require.True(t, ok)
				require.Equal(t, PublicTransitSchemaVersion, v1.SchemaVersion)
			} else {
				v2, ok := payload.(*PublicTransitSnapshotV2)
				require.True(t, ok)
				require.Equal(t, PublicTransitV2SchemaVersion, v2.SchemaVersion)
				require.Empty(t, v2.Monitoring)
			}
			discovery, err := svc.Discovery(context.Background(), "https://example.test")
			require.NoError(t, err)
			require.Len(t, discovery.Capabilities, 1)
		})
	}
}

func TestPublicTransitGroups_RespectsAllowlistAndGroupPricing(t *testing.T) {
	group := Group{ID: 42, Name: "public", Platform: PlatformOpenAI, Status: StatusActive,
		ModelAllowlist: GroupModelAllowlist{Enabled: true, Models: []string{"gpt-5.5"}},
		ModelPricing:   []ChannelModelPricing{{Models: []string{"gpt-5.5"}, InputPrice: testPtrFloat64(9e-6)}}}
	channels := []AvailableChannel{{ID: 123, Status: StatusActive,
		Groups:          []AvailableGroupRef{{ID: group.ID, Name: group.Name, Platform: group.Platform}},
		SupportedModels: []SupportedModel{{Name: "gpt-5.5", Platform: PlatformOpenAI}, {Name: "private-model", Platform: PlatformOpenAI}}}}
	groups := buildPublicTransitGroups([]Group{group}, channels, nil, nil)
	require.Len(t, groups, 1)
	require.Len(t, groups[0].Models, 1)
	require.Equal(t, "gpt-5.5", groups[0].Models[0].StandardModel)
	require.Equal(t, 9e-6, *groups[0].Models[0].Price.InputUSDPerToken)
	encoded, err := json.Marshal(groups)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), `"id"`)
	require.NotContains(t, string(encoded), "private-model")
}

func TestPublicTransitPassiveMatrix_DoesNotSerializePrivateGroupsOrIDs(t *testing.T) {
	groupID := int64(123)
	disclosure := PublicTransitPassiveDisclosure{Groups: []PublicTransitPassiveGroup{{Name: "public", Platform: PlatformOpenAI}}}
	matrix := &ChannelMonitorV2Matrix{Items: []ChannelMonitorV2MatrixRow{
		{Platform: PlatformOpenAI, GroupID: &groupID, GroupName: "public"},
		{Platform: PlatformOpenAI, GroupID: &groupID, GroupName: "private"},
	}}
	augmentPublicTransitPassiveDisclosure(&disclosure, &ChannelMonitorV2Snapshot{}, matrix, nil)
	require.Len(t, disclosure.Matrix.Items, 1)
	encoded, err := json.Marshal(disclosure)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "group_id")
	require.NotContains(t, string(encoded), "private")
}

func TestPublicTransitGroups_CompositeKeepsConcreteModelPlatforms(t *testing.T) {
	group := Group{ID: 42, Name: "composite", Platform: PlatformComposite, Status: StatusActive,
		ModelAllowlist: GroupModelAllowlist{Enabled: true, Models: []string{"shared"}}}
	channels := []AvailableChannel{{Status: StatusActive,
		Groups: []AvailableGroupRef{{ID: group.ID, Name: group.Name, Platform: group.Platform}},
		SupportedModels: []SupportedModel{
			{Name: "shared", Platform: PlatformOpenAI}, {Name: "shared", Platform: PlatformAnthropic},
			{Name: "shared", Platform: PlatformComposite}, {Name: "private-model", Platform: PlatformOpenAI},
		}}}
	groups := buildPublicTransitGroups([]Group{group}, channels, nil, nil)
	require.Len(t, groups, 1)
	require.Len(t, groups[0].Models, 2)
	require.Equal(t, PlatformOpenAI, groups[0].Models[0].Platform)
	require.Equal(t, PlatformAnthropic, groups[0].Models[1].Platform)
}

func TestAdjustPublicCacheCounts_UsesPublicRangeAndKeepsRatioConsistent(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   int64
		new  int64
		read int64
		want float64
	}{
		{name: "boost within range", in: 100, new: 0, read: 400, want: 88},
		{name: "cap high rate", in: 0, new: 100, read: 900, want: 92},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input, created, read, rate := adjustPublicCacheCounts(tc.in, tc.new, tc.read, "stable-key")
			require.InDelta(t, tc.want, rate, 0.1)
			require.InDelta(t, rate, float64(read)/float64(input+created+read)*100, 1e-12)
		})
	}
}

func TestAdjustPublicCacheCounts_LowRateUsesStableExceptionRange(t *testing.T) {
	input, created, read, rate := adjustPublicCacheCounts(100, 20, 30, "group/24h")
	_, _, _, repeatedRate := adjustPublicCacheCounts(100, 20, 30, "group/24h")
	require.GreaterOrEqual(t, rate, 75.0)
	require.Less(t, rate, 80.0)
	require.Equal(t, rate, repeatedRate)
	require.InDelta(t, rate, float64(read)/float64(input+created+read)*100, 1e-12)
	require.NotZero(t, input)
	require.NotZero(t, created)
	require.NotZero(t, read)
}

func TestAdjustPublicCacheCounts_EmptyUsageStillProducesConsistentPublicValues(t *testing.T) {
	input, created, read, rate := adjustPublicCacheCounts(0, 0, 0, "empty/24h")
	require.Equal(t, publicCacheMinimumTotal, input+created+read)
	require.InDelta(t, rate, float64(read)/float64(input+created+read)*100, 1e-12)
	require.GreaterOrEqual(t, rate, 75.0)
	require.Less(t, rate, 80.0)
}

func TestBuildPublicTransitGroups_FiltersExclusiveGroupsAndExportsPricing(t *testing.T) {
	configuredGroups := []Group{
		{
			ID:               10,
			Name:             "public-pro",
			Platform:         "anthropic",
			SubscriptionType: "standard",
			RateMultiplier:   1.25,
			Status:           StatusActive,
		},
		{
			ID:             11,
			Name:           "private-vip",
			Platform:       "anthropic",
			RateMultiplier: 0.8,
			Status:         StatusActive,
			IsExclusive:    true,
		},
	}
	channels := []AvailableChannel{{
		ID:     1,
		Name:   "primary",
		Status: StatusActive,
		Groups: []AvailableGroupRef{
			{
				ID:               10,
				Name:             "public-pro",
				Platform:         "anthropic",
				SubscriptionType: "standard",
				RateMultiplier:   1.25,
			},
			{
				ID:             11,
				Name:           "private-vip",
				Platform:       "anthropic",
				RateMultiplier: 0.8,
				IsExclusive:    true,
			},
		},
		SupportedModels: []SupportedModel{{
			Name:          "claude-sonnet-4",
			Platform:      "anthropic",
			PricingSource: ModelPriceSourceCustom,
			CatalogSource: ModelCatalogSourceChannel,
			Pricing: &ChannelModelPricing{
				Platform:        "anthropic",
				Models:          []string{"claude-sonnet-4"},
				BillingMode:     BillingModeToken,
				InputPrice:      testPtrFloat64(3e-6),
				OutputPrice:     testPtrFloat64(1.5e-5),
				CacheWritePrice: testPtrFloat64(3.75e-6),
				CacheReadPrice:  testPtrFloat64(3e-7),
			},
		}},
	}}

	cacheUsage := map[int64]PublicTransitCacheUsage{
		10: publicCacheUsageFromSummary(usagestats.GroupCacheUsageSummary{
			GroupID: 10,
			Last24h: usagestats.GroupCacheUsageWindow{
				InputTokens:         100,
				CacheCreationTokens: 20,
				CacheReadTokens:     30,
				CacheHitRate:        20,
			},
			Last7d: usagestats.GroupCacheUsageWindow{
				InputTokens:         400,
				CacheCreationTokens: 80,
				CacheReadTokens:     120,
				CacheHitRate:        20,
			},
			Total: usagestats.GroupCacheUsageWindow{
				InputTokens:         900,
				CacheCreationTokens: 100,
				CacheReadTokens:     1000,
				CacheHitRate:        50,
			},
		}),
	}
	groups := buildPublicTransitGroups(configuredGroups, channels, cacheUsage, nil)

	require.Len(t, groups, 1)
	require.Equal(t, "public-pro", groups[0].Name)
	require.False(t, groups[0].IsExclusive)
	require.InDelta(t, 1.25, groups[0].RateMultiplier, 1e-12)
	cache24h := groups[0].CacheUsage.Last24h
	require.InDelta(t, cache24h.CacheHitRate,
		float64(cache24h.CacheReadTokens)/float64(cache24h.InputTokens+cache24h.CacheCreationTokens+cache24h.CacheReadTokens)*100, 1e-12)
	require.GreaterOrEqual(t, cache24h.CacheHitRate, 75.0)
	require.Less(t, cache24h.CacheHitRate, 80.0)
	require.GreaterOrEqual(t, groups[0].CacheUsage.Last7d.CacheHitRate, 75.0)
	require.Less(t, groups[0].CacheUsage.Last7d.CacheHitRate, 80.0)
	require.GreaterOrEqual(t, groups[0].CacheUsage.Total.CacheHitRate, 75.0)
	require.Less(t, groups[0].CacheUsage.Total.CacheHitRate, 80.0)
	require.Len(t, groups[0].Models, 1)

	model := groups[0].Models[0]
	require.Equal(t, "claude-sonnet-4", model.StandardModel)
	require.Equal(t, "anthropic", model.Platform)
	require.Equal(t, string(BillingModeToken), model.BillingMode)
	require.Equal(t, ModelPriceSourceCustom, model.PriceSource)
	require.Equal(t, ModelCatalogSourceChannel, model.CatalogSource)
	require.NotNil(t, model.Price)
	require.InDelta(t, 3e-6, *model.Price.InputUSDPerToken, 1e-12)
	require.InDelta(t, 1.5e-5, *model.Price.OutputUSDPerToken, 1e-12)
	require.True(t, hasCachePricing(groups))
}

func TestBuildPublicTransitGroups_ExportsConfiguredGroupsWithoutAvailableChannels(t *testing.T) {
	configuredGroups := []Group{
		{
			ID:               1,
			Name:             "gpt pro号池",
			Platform:         "openai",
			SubscriptionType: "standard",
			RateMultiplier:   0.2,
			Status:           StatusActive,
		},
		{
			ID:             2,
			Name:           "disabled",
			Platform:       "openai",
			RateMultiplier: 9,
			Status:         StatusDisabled,
		},
		{
			ID:             3,
			Name:           "exclusive",
			Platform:       "openai",
			RateMultiplier: 0.1,
			Status:         StatusActive,
			IsExclusive:    true,
		},
	}

	groups := buildPublicTransitGroups(configuredGroups, nil, nil, nil)

	require.Len(t, groups, 1)
	require.Equal(t, "gpt pro号池", groups[0].Name)
	require.Equal(t, "openai", groups[0].Platform)
	require.InDelta(t, 0.2, groups[0].RateMultiplier, 1e-12)
	require.Equal(t, "last_24h", groups[0].CacheUsage.Last24h.Period)
	require.GreaterOrEqual(t, groups[0].CacheUsage.Last24h.CacheHitRate, 75.0)
	require.Less(t, groups[0].CacheUsage.Last24h.CacheHitRate, 80.0)
	require.Equal(t, int64(1000), groups[0].CacheUsage.Last24h.InputTokens+
		groups[0].CacheUsage.Last24h.CacheCreationTokens+groups[0].CacheUsage.Last24h.CacheReadTokens)
	require.Empty(t, groups[0].Models)
}

func TestBuildPublicTransitGroups_ExportsEnabledGroupModelsList(t *testing.T) {
	groups := buildPublicTransitGroups([]Group{{
		ID:             1,
		Name:           "gpt free号池",
		Platform:       "openai",
		RateMultiplier: 0.1,
		Status:         StatusActive,
		ModelAllowlist: GroupModelAllowlist{
			Enabled: true,
			Models:  []string{"gpt-5.5", "gpt-image-2"},
		},
	}}, nil, nil, nil)

	require.Len(t, groups, 1)
	require.Len(t, groups[0].Models, 2)
	require.Equal(t, "gpt-5.5", groups[0].Models[0].StandardModel)
	require.Equal(t, ModelCatalogSourceGroupModelsList, groups[0].Models[0].CatalogSource)
	require.Equal(t, ModelPriceSourceUnknown, groups[0].Models[0].PriceSource)
}

func TestToPublicTransitModel_NormalizesImageModeToPerRequest(t *testing.T) {
	price := 0.134
	group := Group{
		Platform:     "openai",
		ImagePrice1K: &price,
		ImagePrice2K: testPtrFloat64(0.201),
		ImagePrice4K: testPtrFloat64(0.268),
	}
	model := toPublicTransitModel(SupportedModel{
		Name:          "gpt-image-2",
		Platform:      "openai",
		PricingSource: ModelPriceSourceCustom,
		CatalogSource: ModelCatalogSourceChannel,
		Pricing: &ChannelModelPricing{
			BillingMode:     BillingModeImage,
			PerRequestPrice: &price,
		},
	}, group)

	require.Equal(t, string(BillingModePerRequest), model.BillingMode)
	require.NotNil(t, model.Price)
	require.Equal(t, &price, model.Price.PerRequestUSD)
	require.InDelta(t, 0.134, *model.Price.ImageSizePrices["1k"], 1e-12)
	require.InDelta(t, 0.201, *model.Price.ImageSizePrices["2k"], 1e-12)
	require.InDelta(t, 0.268, *model.Price.ImageSizePrices["4k"], 1e-12)
}

func TestBuildPublicTransitPassiveDisclosureAggregatesModelsAndGroups(t *testing.T) {
	now := time.Now().UTC()
	bucketA := now.Add(-time.Hour).Truncate(time.Hour)
	bucketB := now.Truncate(time.Hour)
	rows := []PublicTransitPassiveAggregate{
		{BucketStart: &bucketA, Platform: "openai", GroupName: "gpt", Model: "gpt-5", RequestCount: 3, SuccessCount: 2, ErrorCount: 1, TotalLatencyMs: 300, LatencySamples: 2, TotalTTFTMs: 80, TTFTSamples: 2, InputTokens: 100, CacheRead: 50, LastRequestAt: &now},
		{BucketStart: &bucketB, Platform: "openai", GroupName: "gpt", Model: "gpt-5", RequestCount: 2, SuccessCount: 2, TotalLatencyMs: 100, LatencySamples: 2, TotalTTFTMs: 20, TTFTSamples: 1, InputTokens: 50, CacheCreate: 25, CacheRead: 25, LastRequestAt: &now},
	}
	out := buildPublicTransitPassiveDisclosure(rows, PublicTransitPassiveWindow{Period: "last_24h", BucketSeconds: 3600})
	require.Len(t, out.Models, 1)
	require.Equal(t, "gpt", out.Models[0].GroupName)
	require.Equal(t, int64(5), out.Models[0].RequestCount)
	require.InDelta(t, 0.8, out.Models[0].SuccessRate, 1e-12)
	require.Equal(t, 100, *out.Models[0].AvgLatencyMs)
	require.Equal(t, int64(5), out.Window.RequestCount)
	require.Equal(t, 100, *out.Window.AvgLatencyMs)
	require.Len(t, out.Groups, 1)
	require.Equal(t, "gpt", out.Groups[0].Name)
	require.Len(t, out.Groups[0].Buckets, 2)
	require.Equal(t, bucketA.Format(time.RFC3339), out.Groups[0].Buckets[0].Start)
	require.InDelta(t, 50.0/150.0, out.Groups[0].Buckets[0].CacheHitRate, 1e-12)
	require.InDelta(t, out.Groups[0].Buckets[0].CacheHitRate,
		out.Groups[0].Buckets[0].Metrics.CacheRate, 1e-12)
}

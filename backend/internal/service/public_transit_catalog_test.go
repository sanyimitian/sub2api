//go:build unit

package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestPublicTransitModelCatalog_OnlySupplementsExplicitEligibleGroups(t *testing.T) {
	pricing := &PricingService{pricingData: map[string]*LiteLLMModelPricing{
		"glm-5.3": {InputCostPerToken: 1.4e-6, OutputCostPerToken: 4.4e-6, CacheReadInputTokenCost: 2.6e-7},
	}}
	configured := []Group{
		{ID: 1, Name: "opt-in", Platform: PlatformOpenAI, Status: StatusActive, RateMultiplier: 0.1},
		{ID: 2, Name: "not-opted-in", Platform: PlatformOpenAI, Status: StatusActive},
		{ID: 3, Name: "private", Platform: PlatformOpenAI, Status: StatusActive, IsExclusive: true},
		{ID: 4, Name: "disabled", Platform: PlatformOpenAI, Status: StatusDisabled},
		{ID: 5, Name: "restricted", Platform: PlatformOpenAI, Status: StatusActive,
			ModelAllowlist: GroupModelAllowlist{Enabled: true, Models: []string{"other-model"}}},
		{ID: 6, Name: "channel-billing", Platform: PlatformOpenAI, Status: StatusActive},
		{ID: 7, Name: "existing-catalog", Platform: PlatformOpenAI, Status: StatusActive},
		{ID: 8, Name: "composite", Platform: PlatformComposite, Status: StatusActive},
	}
	// Include even private/inactive groups in the input to prove the supplement
	// independently enforces disclosure restrictions rather than trusting callers.
	groups := make([]PublicTransitGroup, 0, len(configured))
	for _, group := range configured {
		groups = append(groups, PublicTransitGroup{ID: group.ID, RateMultiplier: group.RateMultiplier})
	}
	groups[6].Models = []PublicTransitModel{{StandardModel: "existing", PriceSource: "existing"}}
	channels := []AvailableChannel{{Status: StatusActive, Groups: []AvailableGroupRef{{ID: 6}}}}
	catalog := []PublicTransitModelCatalogEntry{
		{GroupID: 1, Models: []string{"glm-5.3", "GLM-5.3"}},
		{GroupID: 1, Models: []string{"glm-5.3"}},
	}
	for _, id := range []int64{3, 4, 5, 6, 7, 8, 999} {
		catalog = append(catalog, PublicTransitModelCatalogEntry{GroupID: id, Models: []string{"glm-5.3"}})
	}
	before, err := json.Marshal(configured)
	require.NoError(t, err)
	supplementPublicTransitModelCatalog(groups, configured, channels, catalog, pricing)
	require.Len(t, groups[0].Models, 1)
	require.Equal(t, "glm-5.3", groups[0].Models[0].StandardModel)
	// Chinese model family does not change the group's actual request platform.
	require.Equal(t, PlatformOpenAI, groups[0].Models[0].Platform)
	require.Equal(t, []string{"openai_chat_completions", "openai_responses"}, groups[0].Models[0].SupportedProtocol)
	require.Equal(t, 0.1, groups[0].RateMultiplier)
	require.Equal(t, 1.4e-6, *groups[0].Models[0].Price.InputUSDPerToken)
	for _, i := range []int{1, 2, 3, 4, 5, 7} {
		require.Empty(t, groups[i].Models)
	}
	require.Equal(t, []PublicTransitModel{{StandardModel: "existing", PriceSource: "existing"}}, groups[6].Models)
	after, err := json.Marshal(configured)
	require.NoError(t, err)
	require.JSONEq(t, string(before), string(after), "public catalog must not mutate group configuration")
}

func TestPublicTransitModelCatalog_PricesAreIdentifiedAndGroupOverridesAreMerged(t *testing.T) {
	pricing := &PricingService{pricingData: map[string]*LiteLLMModelPricing{
		"claude-opus-5-5": {InputCostPerToken: 4e-6, OutputCostPerToken: 20e-6, CacheReadInputTokenCost: 0.2e-6},
		"image-only":      {TokenPricingAbsent: true, Mode: "image_generation", OutputCostPerImage: 0.04},
	}}
	group := Group{Platform: PlatformAnthropic, ModelPricing: []ChannelModelPricing{
		{Models: []string{"claude-opus-5-5"}, InputPrice: testPtrFloat64(3e-6), CacheReadPrice: testPtrFloat64(0)},
	}}
	model, ok := publicCatalogModel(group, "claude-opus-5-5", pricing)
	require.True(t, ok)
	require.Equal(t, 3e-6, *model.Price.InputUSDPerToken)
	require.Equal(t, 20e-6, *model.Price.OutputUSDPerToken)
	require.Zero(t, *model.Price.CacheReadUSDPerToken, "explicit custom zero is not missing pricing")
	require.Equal(t, ModelPriceSourceCustom, model.PriceSource)
	require.Equal(t, "public_catalog", model.CatalogSource)
	require.Nil(t, group.ModelPricing[0].OutputPrice)
	require.Equal(t, 4e-6, pricing.pricingData["claude-opus-5-5"].InputCostPerToken)

	alias, ok := publicCatalogModel(Group{Platform: PlatformAnthropic}, "anthropic/claude-opus-5-5", pricing)
	require.True(t, ok)
	require.Equal(t, 4e-6, *alias.Price.InputUSDPerToken)
	for _, unknown := range []string{"arbitrary-opus-5", "gpt-invented", "deepseek-unverified", "image-only"} {
		_, ok := publicCatalogModel(group, unknown, pricing)
		require.False(t, ok, "missing catalog prices must not become guessed or zero prices: %s", unknown)
	}
	_, ok = publicCatalogModel(group, "claude-opus-5-5", nil)
	require.False(t, ok, "partial group card alone does not establish complete token pricing")
}

func TestPublicTransitModelCatalog_DeepseekPublishedRatesMatchActualBilling(t *testing.T) {
	billing := NewBillingService(&config.Config{}, nil)
	resolver := NewModelPricingResolver(nil, billing)
	tokens := UsageTokens{InputTokens: 1000, OutputTokens: 500, CacheReadTokens: 1000}
	for _, custom := range []bool{false, true} {
		group := Group{Platform: PlatformOpenAI, RateMultiplier: 0.1}
		if custom {
			group.ModelPricing = []ChannelModelPricing{{
				Models: []string{"deepseek-v4.1-flash"}, InputPrice: testPtrFloat64(1e-6),
			}}
		}
		for _, now := range []time.Time{
			time.Date(2026, 11, 17, 2, 0, 0, 0, time.UTC),  // Tuesday peak
			time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC), // Tuesday off-peak
			time.Date(2026, 11, 21, 2, 0, 0, 0, time.UTC),  // Beijing weekend
		} {
			model, ok := publicCatalogModelAt(group, "deepseek-v4.1-flash", nil, now)
			require.True(t, ok)
			cost, err := billing.CalculateCostUnified(CostInput{
				Ctx: context.Background(), Model: "deepseek-v4.1-flash", Group: &group,
				Tokens: tokens, RateMultiplier: group.RateMultiplier, Resolver: resolver, PricingAt: now,
			})
			require.NoError(t, err)
			publishedCost := float64(tokens.InputTokens)*(*model.Price.InputUSDPerToken) +
				float64(tokens.OutputTokens)*(*model.Price.OutputUSDPerToken) +
				float64(tokens.CacheReadTokens)*(*model.Price.CacheReadUSDPerToken)
			require.InDelta(t, cost.TotalCost, publishedCost, 1e-12, "custom=%t at=%s", custom, now)
			require.InDelta(t, cost.ActualCost, publishedCost*group.RateMultiplier, 1e-12)
			if custom {
				require.NotContains(t, model.Source.Disclosure, "2x off-peak")
			} else {
				require.Contains(t, model.Source.Disclosure, "2x off-peak")
			}
		}
	}
}

func TestPublicTransitModelCatalog_DeepseekOverrideFileRatesMatchActualBilling(t *testing.T) {
	const catalog = `{"deepseek-v4.1-flash":{"input_cost_per_token":9e-6,"output_cost_per_token":8e-6,"cache_read_input_token_cost":7e-6}}`
	for _, tc := range []struct {
		name  string
		patch string
	}{
		{"input only", `{"input_cost_per_token":1e-6}`},
		{"output only", `{"output_cost_per_token":2e-6}`},
		{"cache read only", `{"cache_read_input_token_cost":3e-7}`},
		{"explicit zeros", `{"input_cost_per_token":0,"output_cost_per_token":0,"cache_read_input_token_cost":0}`},
		{"null is not an override", `{"input_cost_per_token":null,"output_cost_per_token":null,"cache_read_input_token_cost":null}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pricing := newPricingServiceWithOverride(t, `{"deepseek-v4.1-flash":`+tc.patch+`}`)
			var err error
			pricing.pricingData, err = pricing.parsePricingData([]byte(catalog))
			require.NoError(t, err)
			billing := NewBillingService(&config.Config{}, pricing)
			resolver := NewModelPricingResolver(nil, billing)
			before, err := json.Marshal(pricing.pricingData)
			require.NoError(t, err)
			for _, custom := range []bool{false, true} {
				group := Group{Platform: PlatformOpenAI, RateMultiplier: 0.1}
				if custom {
					group.ModelPricing = []ChannelModelPricing{{
						Models: []string{"deepseek-v4.1-flash"}, InputPrice: testPtrFloat64(4e-6),
					}}
				}
				for _, now := range []time.Time{
					time.Date(2026, 11, 17, 2, 0, 0, 0, time.UTC),
					time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC),
					time.Date(2026, 11, 21, 2, 0, 0, 0, time.UTC),
				} {
					model, ok := publicCatalogModelAt(group, "deepseek-v4.1-flash", pricing, now)
					require.True(t, ok)
					// Compare each component independently so opposite rate errors
					// cannot cancel out in the total price assertion.
					tokens := UsageTokens{InputTokens: 1000, OutputTokens: 500, CacheReadTokens: 1000}
					cost, err := billing.CalculateCostUnified(CostInput{
						Ctx: context.Background(), Model: "deepseek-v4.1-flash", Group: &group,
						Tokens: tokens, RateMultiplier: group.RateMultiplier, Resolver: resolver, PricingAt: now,
					})
					require.NoError(t, err)
					require.NotNil(t, model.Price.InputUSDPerToken)
					require.NotNil(t, model.Price.OutputUSDPerToken)
					require.NotNil(t, model.Price.CacheReadUSDPerToken)
					require.InDelta(t, cost.InputCost, float64(tokens.InputTokens)*(*model.Price.InputUSDPerToken), 1e-12, "custom=%t at=%s", custom, now)
					require.InDelta(t, cost.OutputCost, float64(tokens.OutputTokens)*(*model.Price.OutputUSDPerToken), 1e-12, "custom=%t at=%s", custom, now)
					require.InDelta(t, cost.CacheReadCost, float64(tokens.CacheReadTokens)*(*model.Price.CacheReadUSDPerToken), 1e-12, "custom=%t at=%s", custom, now)
				}
			}
			after, err := json.Marshal(pricing.pricingData)
			require.NoError(t, err)
			require.JSONEq(t, string(before), string(after), "public snapshots must not mutate shared billing prices")
		})
	}
}

func TestPublicTransitModelCatalog_SnapshotModesUseSameOptInCatalog(t *testing.T) {
	repo := &settingPublicRepoStub{values: map[string]string{
		SettingKeyPublicTransitModelCatalog: `[{"group_id":42,"models":["claude-opus-5-5"]}]`,
		SettingKeyChannelMonitorEnabled:     "true",
	}}
	settings := NewSettingService(repo, &config.Config{})
	groups := &stubGroupRepoForAvailable{activeGroups: []Group{{
		ID: 42, Name: "verified-public-group", Platform: PlatformAnthropic, Status: StatusActive, RateMultiplier: 0.5,
	}}}
	pricing := &PricingService{pricingData: map[string]*LiteLLMModelPricing{
		"claude-opus-5-5": {InputCostPerToken: 4e-6, OutputCostPerToken: 20e-6},
	}}
	channels := NewChannelService(&mockChannelRepository{listAllFn: func(context.Context) ([]Channel, error) {
		return nil, nil
	}}, groups, nil, pricing, nil)
	svc := NewPublicTransitService(channels, NewChannelMonitorService(&publicTransitMonitorRepoStub{}, nil), settings,
		NewPaymentConfigService(nil, repo, nil), groups, nil)
	for _, mode := range []string{ChannelMonitorModeV1, ChannelMonitorModeV2} {
		t.Run(mode, func(t *testing.T) {
			repo.values[SettingKeyChannelMonitorMode] = mode
			payload, err := svc.SnapshotAuto(context.Background(), "https://example.test", "24h")
			require.NoError(t, err)
			encoded, err := json.Marshal(payload)
			require.NoError(t, err)
			var snapshot struct {
				Groups []PublicTransitGroup `json:"groups"`
			}
			require.NoError(t, json.Unmarshal(encoded, &snapshot))
			require.Len(t, snapshot.Groups, 1)
			require.Len(t, snapshot.Groups[0].Models, 1)
			require.Equal(t, "claude-opus-5-5", snapshot.Groups[0].Models[0].StandardModel)
			require.Equal(t, 0.5, snapshot.Groups[0].RateMultiplier)
			require.NotContains(t, string(encoded), "group_id")
		})
	}
}

func TestPublicTransitModelCatalog_ConfigurationDefaultsOffAndRejectsAmbiguousInput(t *testing.T) {
	for _, raw := range []string{"", "[]", "null"} {
		catalog, err := parsePublicTransitModelCatalog(raw)
		require.NoError(t, err)
		require.Empty(t, catalog)
	}
	for _, raw := range []string{
		`{"42":["glm-5.3"]}`,
		`[{"group_id":0,"models":["glm-5.3"]}]`,
		`[{"group_id":42,"models":["claude-*"]}]`,
		`[{"group_id":42,"models":["private\nmodel"]}]`,
		`[{"group_id":42,"models":["glm-5.3"],"rate_multiplier":0.01}]`,
		`[] []`,
	} {
		_, err := parsePublicTransitModelCatalog(raw)
		require.Error(t, err)
	}
	catalog, err := parsePublicTransitModelCatalog(`[{"group_id":42,"models":[" claude-opus-5-5 "]}]`)
	require.NoError(t, err)
	require.Equal(t, "claude-opus-5-5", catalog[0].Models[0])
}

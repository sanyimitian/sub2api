package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"time"
	"unicode"
)

const SettingKeyPublicTransitModelCatalog = "public_transit_model_catalog"

// PublicTransitModelCatalogEntry is an explicit publication opt-in, independent
// of the group's request allowlist. Operators must verify model support before
// adding an entry. This setting is read only by public snapshot assembly; it
// neither advertises models on authenticated /models nor changes routing or
// billing. No accounts, credentials or monitor results are used to populate it.
type PublicTransitModelCatalogEntry struct {
	GroupID int64    `json:"group_id"`
	Models  []string `json:"models"`
}

func (s *SettingService) GetPublicTransitModelCatalog(ctx context.Context) ([]PublicTransitModelCatalogEntry, error) {
	values, err := s.settingRepo.GetMultiple(ctx, []string{SettingKeyPublicTransitModelCatalog})
	if err != nil {
		return nil, err
	}
	return parsePublicTransitModelCatalog(values[SettingKeyPublicTransitModelCatalog])
}

func parsePublicTransitModelCatalog(raw string) ([]PublicTransitModelCatalogEntry, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var entries []PublicTransitModelCatalogEntry
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&entries); err != nil {
		return nil, fmt.Errorf("invalid public model catalog: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("public model catalog must contain one JSON array")
	}
	for i := range entries {
		if entries[i].GroupID <= 0 {
			return nil, fmt.Errorf("public model catalog entry %d requires a positive group_id", i)
		}
		for j, name := range entries[i].Models {
			name = strings.TrimSpace(name)
			if name == "" || len(name) > 200 || strings.ContainsAny(name, "*?") || strings.IndexFunc(name, unicode.IsSpace) >= 0 || strings.IndexFunc(name, unicode.IsControl) >= 0 {
				return nil, fmt.Errorf("public model catalog entry %d requires explicit model names", i)
			}
			entries[i].Models[j] = name
		}
	}
	return entries, nil
}

// supplementPublicTransitModelCatalog only fills groups with no existing
// published models and no active channel association. In particular, it must
// not bypass a channel's model mapping, restriction or custom billing price.
// Entries are stable across individual monitor successes/failures, while the
// separate monitoring payload continues to report real availability.
func supplementPublicTransitModelCatalog(groups []PublicTransitGroup, configuredGroups []Group, channels []AvailableChannel, catalog []PublicTransitModelCatalogEntry, pricingService *PricingService) {
	configured := make(map[int64]Group, len(configuredGroups))
	for _, group := range configuredGroups {
		if group.Status == StatusActive && !group.IsExclusive && isConcreteRequestPlatform(group.Platform) {
			configured[group.ID] = group
		}
	}
	channelGroups := make(map[int64]bool)
	for _, channel := range channels {
		if channel.Status == StatusActive {
			for _, group := range channel.Groups {
				channelGroups[group.ID] = true
			}
		}
	}
	modelsByGroup := make(map[int64][]string)
	for _, entry := range catalog {
		modelsByGroup[entry.GroupID] = append(modelsByGroup[entry.GroupID], entry.Models...)
	}
	for i := range groups {
		out := &groups[i]
		group, ok := configured[out.ID]
		if !ok || channelGroups[out.ID] || len(out.Models) != 0 {
			continue
		}
		seen := make(map[string]bool)
		for _, name := range modelsByGroup[out.ID] {
			key := strings.ToLower(name)
			if seen[key] || (group.ModelAllowlistEnabled() && !group.ModelAllowlist.Allows(name)) {
				continue
			}
			seen[key] = true
			model, ok := publicCatalogModel(group, name, pricingService)
			if ok {
				out.Models = append(out.Models, model)
			}
		}
		sort.SliceStable(out.Models, func(i, j int) bool {
			return strings.ToLower(out.Models[i].StandardModel) < strings.ToLower(out.Models[j].StandardModel)
		})
	}
}

func publicCatalogModel(group Group, name string, pricingService *PricingService) (PublicTransitModel, bool) {
	return publicCatalogModelAt(group, name, pricingService, time.Now())
}

func publicCatalogModelAt(group Group, name string, pricingService *PricingService, now time.Time) (PublicTransitModel, bool) {
	// Identified lookup accepts known aliases but never guesses a price from
	// substrings such as "gpt" or "opus". Missing token prices stay unpublished.
	var price *ChannelModelPricing
	var overrides tokenPriceOverrides
	if base := pricingService.GetIdentifiedModelPricing(name); base != nil && !base.TokenPricingAbsent && base.Mode != "image_generation" {
		price = synthesizePricingFromLiteLLM(base, nil)
		overrides = base.priceOverrides
		// Unlike a missing catalog rate, an explicit override of zero is a
		// configured price. Preserve it before applying model-specific policy.
		for _, field := range []struct {
			explicit bool
			value    float64
			dst      **float64
		}{
			{overrides.input, base.InputCostPerToken, &price.InputPrice},
			{overrides.output, base.OutputCostPerToken, &price.OutputPrice},
			{overrides.cacheRead, base.CacheReadInputTokenCost, &price.CacheReadPrice},
		} {
			if field.explicit {
				value := field.value
				*field.dst = &value
			}
		}
	}
	// This verified model has a built-in billing policy even when absent from
	// the downloaded catalog. Reuse its billing constants, not a family guess.
	// Billing applies these base rates before any partial group overrides.
	deepseekFlash := strings.EqualFold(name, "deepseek-v4.1-flash")
	if deepseekFlash {
		if price == nil {
			price = &ChannelModelPricing{BillingMode: BillingModeToken}
		}
		if !overrides.input {
			price.InputPrice = nonZeroPtr(deepseekFlashOffPeakInputPrice)
		}
		if !overrides.output {
			price.OutputPrice = nonZeroPtr(deepseekFlashOffPeakOutputPrice)
		}
		if !overrides.cacheRead {
			price.CacheReadPrice = nonZeroPtr(deepseekFlashOffPeakCacheRead)
		}
	}
	priceSource := ModelPriceSourceStandard
	if custom := matchGroupModelPricing(&group, name); custom != nil {
		if (custom.BillingMode != "" && custom.BillingMode != BillingModeToken) || custom.TimePricing != nil {
			return PublicTransitModel{}, false
		}
		if price == nil {
			price = &ChannelModelPricing{BillingMode: BillingModeToken}
		}
		// Group token cards override only configured flat fields, as in billing.
		// Their legacy interval fields are ignored by ModelPricingResolver.
		for _, field := range []struct{ src, dst **float64 }{
			{&custom.InputPrice, &price.InputPrice},
			{&custom.OutputPrice, &price.OutputPrice},
			{&custom.CacheWritePrice, &price.CacheWritePrice},
			{&custom.CacheReadPrice, &price.CacheReadPrice},
		} {
			if *field.src != nil {
				*field.dst = *field.src
			}
		}
		priceSource = ModelPriceSourceCustom
	}
	if deepseekFlash && priceSource == ModelPriceSourceStandard {
		// Match actual default-card billing at snapshot time. Group custom
		// prices do not receive this multiplier, including partial price cards.
		// Explicit override-file fields also stay fixed, matching billing.
		multiplier := deepseekPeakMultiplierAt(now)
		for _, field := range []struct {
			explicit bool
			dst      **float64
		}{
			{overrides.input, &price.InputPrice},
			{overrides.output, &price.OutputPrice},
			{overrides.cacheRead, &price.CacheReadPrice},
		} {
			if !field.explicit {
				value := **field.dst * multiplier
				*field.dst = &value
			}
		}
	}
	if price == nil || price.InputPrice == nil || price.OutputPrice == nil {
		return PublicTransitModel{}, false
	}
	for _, value := range []*float64{price.InputPrice, price.OutputPrice, price.CacheWritePrice, price.CacheReadPrice} {
		if value != nil && (math.IsNaN(*value) || math.IsInf(*value, 0) || *value < 0) {
			return PublicTransitModel{}, false
		}
	}
	// The already merged price must not be replaced again by a partial group
	// card in toPublicTransitModel. Clearing this copy cannot affect billing.
	group.ModelPricing = nil
	model := toPublicTransitModel(SupportedModel{
		Name: name, Platform: group.Platform, Pricing: price,
		PricingSource: priceSource, CatalogSource: "public_catalog",
	}, group)
	if deepseekFlash && priceSource == ModelPriceSourceStandard {
		model.Source.Disclosure += "; token prices reflect the current billing period: weekday 01:00-04:00 and 06:00-10:00 UTC cost 2x off-peak for default rates; Beijing weekends are off-peak; explicitly overridden rates stay fixed"
	}
	return model, true
}

package service

import (
 "context"
 "encoding/json"
 "os"
 "sort"
 "testing"

 "github.com/Wei-Shaw/sub2api/internal/config"
 "github.com/stretchr/testify/require"
)

func TestPricingAudit20261112(t *testing.T) {
 const dir = "/tmp/sub2api-pricing-audit-20261112/"
 cfg := &config.Config{}
 cfg.Pricing.FallbackFile = dir + "fallback.json"
 ps := &PricingService{cfg: cfg}
 data, err := os.ReadFile(dir + "catalog.json")
 require.NoError(t, err)
 ps.pricingData, err = ps.parsePricingData(data)
 require.NoError(t, err)
 ps.pricingData = ps.mergeFallbackPricingData(ps.pricingData)
 bs := NewBillingService(cfg, ps)
 resolver := NewModelPricingResolver(nil, bs)
 var snapshot struct { Models map[string]string `json:"models"` }
 data, err = os.ReadFile(dir + "group-before.json")
 require.NoError(t, err)
 require.NoError(t, json.Unmarshal(data, &snapshot))
 models := make([]string, 0, len(snapshot.Models))
 for model := range snapshot.Models { models = append(models, model) }
 sort.Strings(models)
 for _, model := range models {
  pricing, e := bs.GetModelPricing(model)
  if e != nil { t.Logf("MISSING %s", model); continue }
  source := "catalog"
  if ps.GetModelPricing(model) == nil { source = "fallback" }
  t.Logf("PRICED %s source=%s input=%g output=%g cache=%g", model, source, pricing.InputPricePerToken*1e6, pricing.OutputPricePerToken*1e6, pricing.CacheReadPricePerToken*1e6)
 }
 var cards []ChannelModelPricing
 data, err = os.ReadFile(dir + "pricing-addition.json")
 require.NoError(t, err)
 require.NoError(t, json.Unmarshal(data, &cards))
 cards, err = normalizeGroupModelPricing(PlatformOpenAI, cards)
 require.NoError(t, err)
 group := &Group{ID: 100, Platform: PlatformOpenAI, ModelPricing: cards, LongContextPricingEnabled: true}
 for _, test := range []struct{name string; tokens UsageTokens; cny float64}{
  {"input", UsageTokens{InputTokens:1000000},13},
  {"output", UsageTokens{OutputTokens:1000000},68},
  {"cache_read", UsageTokens{CacheReadTokens:1000000},1.3},
  {"cache_write_5m", UsageTokens{CacheCreationTokens:1000000,CacheCreation5mTokens:1000000},13},
  {"cache_write_1h", UsageTokens{CacheCreationTokens:1000000,CacheCreation1hTokens:1000000},26},
  {"historical_example",UsageTokens{InputTokens:485,OutputTokens:337,CacheReadTokens:427264},(485*13+337*68+427264*1.3)/1e6},
 } {
  cost, e := bs.CalculateCostUnified(CostInput{Ctx:context.Background(),Model:"kimi-k3.1-pro",GroupID:&group.ID,Group:group,Tokens:test.tokens,RateMultiplier:0.16,Resolver:resolver})
  require.NoError(t,e)
  require.InDelta(t,test.cny/6.6707*0.16,cost.ActualCost,1e-10,test.name)
  t.Logf("VERIFIED %s total_usd=%.10f actual_usd=%.10f",test.name,cost.TotalCost,cost.ActualCost)
 }
}

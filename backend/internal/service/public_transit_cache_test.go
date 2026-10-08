package service

import (
	"context"
	"fmt"
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

type publicTransitPresentationCacheStub struct {
	state  PublicTransitPresentationState
	found  bool
	reads  int
	writes int
}

func (c *publicTransitPresentationCacheStub) Get(context.Context, string) (PublicTransitPresentationState, bool, error) {
	c.reads++
	return c.state, c.found, nil
}

func (c *publicTransitPresentationCacheStub) Set(_ context.Context, _ string, state PublicTransitPresentationState) error {
	c.writes++
	c.state = state
	c.found = true
	return nil
}

func TestPublicTransitCachePolicyValidation(t *testing.T) {
	require.NoError(t, DefaultPublicTransitCachePolicy().Validate())
	invalid := DefaultPublicTransitCachePolicy()
	invalid.LowRateMax = 81
	require.Error(t, invalid.Validate())
}

func TestPublicTransitCachePolicyDisabledReturnsOriginalAndSkipsPool(t *testing.T) {
	pool := &publicTransitPresentationCacheStub{}
	svc := &PublicTransitService{presentationCache: pool}
	policy := DefaultPublicTransitCachePolicy()
	policy.Enabled = false

	input, created, read, rate, err := svc.publicCachePresentationWithPolicy(context.Background(), "key", 100, 20, 30, policy)
	require.NoError(t, err)
	require.Equal(t, int64(100), input)
	require.Equal(t, int64(20), created)
	require.Equal(t, int64(30), read)
	require.InDelta(t, 20, rate, 1e-12)
	require.Zero(t, pool.reads)
	require.Zero(t, pool.writes)
}

func TestAdjustPublicCacheRateRandomizesBoostedRatesAtOrAbove92(t *testing.T) {
	policy := DefaultPublicTransitCachePolicy()
	belowThreshold := adjustPublicCacheRateWithPolicy(83.63, "group/below", policy)
	require.InDelta(t, 83.63*1.1, belowThreshold, 1e-12)

	first := adjustPublicCacheRateWithPolicy(83.64, "group/stable", policy)
	require.GreaterOrEqual(t, first, 90.0)
	require.LessOrEqual(t, first, 93.0)
	require.InDelta(t, first, math.Round(first*100)/100, 1e-12)
	require.Equal(t, first, adjustPublicCacheRateWithPolicy(83.64, "group/stable", policy))

	values := make(map[float64]struct{})
	for i := range 100 {
		value := adjustPublicCacheRateWithPolicy(83.64, fmt.Sprintf("group/%d", i), policy)
		require.GreaterOrEqual(t, value, 90.0)
		require.LessOrEqual(t, value, 93.0)
		require.InDelta(t, value, math.Round(value*100)/100, 1e-12)
		values[value] = struct{}{}
	}
	require.Greater(t, len(values), 1)
}

func TestAdjustPublicCacheCountsMatchRandomizedRate(t *testing.T) {
	input, created, read, rate := adjustPublicCacheCountsWithPolicy(1636, 0, 8364, "group/high-cache", DefaultPublicTransitCachePolicy())
	total := input + created + read

	require.Equal(t, int64(10000), total)
	require.GreaterOrEqual(t, rate, 90.0)
	require.LessOrEqual(t, rate, 93.0)
	require.InDelta(t, rate, math.Round(rate*100)/100, 1e-12)
	require.InDelta(t, rate, float64(read)/float64(total)*100, 1e-12)
}

func TestPublicTransitPresentationCacheReusesStoredPublicValues(t *testing.T) {
	pool := &publicTransitPresentationCacheStub{
		found: true,
		state: PublicTransitPresentationState{
			Version:     publicTransitPresentationAlgorithmVersion,
			SourceInput: 100, SourceRead: 400,
			Input: 60, Created: 60, Read: 880,
			Policy: DefaultPublicTransitCachePolicy(),
		},
	}
	svc := &PublicTransitService{presentationCache: pool}
	ctx := context.Background()

	input, created, read, rate, err := svc.publicCachePresentation(ctx, "group/1/last_24h", 100, 0, 400)
	require.NoError(t, err)
	require.Equal(t, int64(60), input)
	require.Equal(t, int64(60), created)
	require.Equal(t, int64(880), read)
	require.InDelta(t, 88, rate, 0.1)
	require.Equal(t, 1, pool.writes)

	inputAgain, createdAgain, readAgain, rateAgain, err := svc.publicCachePresentation(ctx, "group/1/last_24h", 100, 0, 400)
	require.NoError(t, err)
	require.Equal(t, input, inputAgain)
	require.Equal(t, created, createdAgain)
	require.Equal(t, read, readAgain)
	require.InDelta(t, rate, rateAgain, 0.1)
	require.Equal(t, 2, pool.writes)

	updatedPolicy := DefaultPublicTransitCachePolicy()
	updatedPolicy.MaximumRate = 87
	_, _, _, rateAfterPolicyChange, err := svc.publicCachePresentationWithPolicy(ctx, "group/1/last_24h", 100, 0, 400, updatedPolicy)
	require.NoError(t, err)
	require.InDelta(t, 87, rateAfterPolicyChange, 0.1)
	require.Equal(t, updatedPolicy, pool.state.Policy)

	_, _, _, _, err = svc.publicCachePresentation(ctx, "group/1/last_24h", 101, 0, 400)
	require.NoError(t, err)
	require.Equal(t, int64(101), pool.state.SourceInput)
}

func TestPublicTransitPresentationCacheRefreshesLegacyAlgorithmValues(t *testing.T) {
	pool := &publicTransitPresentationCacheStub{
		found: true,
		state: PublicTransitPresentationState{
			SourceInput: 1636, SourceRead: 8364,
			Input: 80, Read: 920,
			Policy: DefaultPublicTransitCachePolicy(),
		},
	}
	svc := &PublicTransitService{presentationCache: pool}

	input, created, read, rate, err := svc.publicCachePresentation(context.Background(), "group/high-cache", 1636, 0, 8364)
	require.NoError(t, err)
	require.Equal(t, publicTransitPresentationAlgorithmVersion, pool.state.Version)
	require.Equal(t, input, pool.state.Input)
	require.Equal(t, created, pool.state.Created)
	require.Equal(t, read, pool.state.Read)
	require.GreaterOrEqual(t, rate, 90.0)
	require.LessOrEqual(t, rate, 93.0)
	require.Equal(t, 1, pool.writes)
}

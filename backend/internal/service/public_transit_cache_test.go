package service

import (
	"context"
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

func TestPublicTransitPresentationCacheReusesStoredPublicValues(t *testing.T) {
	pool := &publicTransitPresentationCacheStub{
		found: true,
		state: PublicTransitPresentationState{
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

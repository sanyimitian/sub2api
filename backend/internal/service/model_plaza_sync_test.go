//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type plazaSyncSettingRepo struct {
	SettingRepository
	value             string
	channelValue      string
	readErr, writeErr error
	writes            int
}

func (r *plazaSyncSettingRepo) GetValue(_ context.Context, key string) (string, error) {
	if r.readErr != nil {
		return "", r.readErr
	}
	value := r.value
	if key == settingKeyAvailableChannelCatalog {
		value = r.channelValue
	}
	if value == "" {
		return "", ErrSettingNotFound
	}
	return value, nil
}

func (r *plazaSyncSettingRepo) Set(_ context.Context, key, value string) error {
	if r.writeErr != nil {
		return r.writeErr
	}
	if key == settingKeyAvailableChannelCatalog {
		r.channelValue = value
	} else {
		r.value = value
	}
	r.writes++
	return nil
}

func TestModelPlazaSync_AvailableChannelsHaveIndependentManualSnapshot(t *testing.T) {
	ctx := context.Background()
	channel := plazaPricedChannel(1, "shared", []int64{10, 20}, PlatformOpenAI, "gpt-6-sol", "gpt-image-2", "unused")
	svc := newPlazaService([]Channel{channel}, []Group{{ID: 10, Platform: PlatformOpenAI}, {ID: 20, Platform: PlatformOpenAI}}, nil)
	repo := &plazaSupportAccountRepo{accounts: []Account{plazaSupportAccount(1, 10, PlatformOpenAI, "gpt-6-sol"), plazaSupportAccount(2, 20, PlatformOpenAI, "gpt-image-2")}}
	settings := &plazaSyncSettingRepo{}
	svc.accountRepo, svc.settingRepo = repo, settings
	channels := []AvailableChannel{{ID: 1, Groups: []AvailableGroupRef{{ID: 10}, {ID: 20}}, SupportedModels: channel.SupportedModels()}}
	require.NoError(t, svc.ApplyAvailableChannelCatalog(ctx, channels))
	require.Nil(t, channels[0].GroupModels)
	require.Zero(t, repo.calls)
	_, err := svc.SyncModels(ctx)
	require.NoError(t, err)
	require.NoError(t, svc.ApplyAvailableChannelCatalog(ctx, channels))
	require.Nil(t, channels[0].GroupModels, "syncing the plaza must not sync channels")
	savedPlaza := settings.value
	result, err := svc.SyncAvailableChannelModels(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, result.ModelCount)
	require.Equal(t, savedPlaza, settings.value)
	repo.accounts = nil
	repo.err = errors.New("public reads must not query accounts")
	require.NoError(t, svc.ApplyAvailableChannelCatalog(ctx, channels))
	require.Len(t, channels[0].GroupModels[10], 1)
	require.Equal(t, "gpt-6-sol", channels[0].GroupModels[10][0].Name)
	require.Equal(t, "gpt-image-2", channels[0].GroupModels[20][0].Name)
	require.Equal(t, 2, repo.calls)
	savedChannels := settings.channelValue
	_, err = svc.SyncAvailableChannelModels(ctx)
	require.Error(t, err)
	require.Equal(t, savedChannels, settings.channelValue)
}

func TestModelPlazaSync_OnlyExplicitActionUpdatesSavedModels(t *testing.T) {
	ctx := context.Background()
	channel := plazaPricedChannel(1, "shared", []int64{10}, PlatformOpenAI, "gpt-6-sol", "gpt-image-2")
	groups := []Group{{ID: 10, Platform: PlatformOpenAI}}
	repo := &plazaSupportAccountRepo{accounts: []Account{plazaSupportAccount(1, 10, PlatformOpenAI, "gpt-6-sol")}}
	settings := &plazaSyncSettingRepo{}
	svc := newPlazaService([]Channel{channel}, groups, nil)
	svc.accountRepo, svc.settingRepo = repo, settings
	before, err := svc.ListGroups(ctx)
	require.NoError(t, err)
	require.Len(t, before[0].Models, 2)
	require.Zero(t, repo.calls)
	require.Zero(t, settings.writes)

	result, err := svc.SyncModels(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, result.GroupCount)
	require.Equal(t, 1, result.ModelCount)
	require.False(t, result.SyncedAt.IsZero())
	require.Equal(t, 1, repo.calls)
	require.Equal(t, 1, settings.writes)

	// Account edits, refreshes and a process restart must not resync membership.
	repo.accounts = []Account{plazaSupportAccount(2, 10, PlatformOpenAI, "gpt-image-2")}
	restarted := newPlazaService([]Channel{channel}, groups, nil)
	restarted.accountRepo, restarted.settingRepo = repo, settings
	for i := 0; i < 2; i++ {
		out, err := restarted.ListGroups(ctx)
		require.NoError(t, err)
		require.Len(t, out[0].Models, 1)
		require.Equal(t, "gpt-6-sol", out[0].Models[0].Name)
	}
	require.Equal(t, 1, repo.calls)
	require.Equal(t, 1, settings.writes)
	_, err = restarted.SyncModels(ctx)
	require.NoError(t, err)
	out, err := restarted.ListGroups(ctx)
	require.NoError(t, err)
	require.Equal(t, "gpt-image-2", out[0].Models[0].Name)

	// Saving an empty result is distinct from having never synced.
	repo.accounts = nil
	result, err = restarted.SyncModels(ctx)
	require.NoError(t, err)
	require.Zero(t, result.ModelCount)
	out, err = restarted.ListGroups(ctx)
	require.NoError(t, err)
	require.Empty(t, out)
}

func TestModelPlazaSync_FailuresPreserveSavedCatalog(t *testing.T) {
	ctx := context.Background()
	svc := newPlazaService([]Channel{plazaPricedChannel(1, "ch", []int64{10}, PlatformOpenAI, "gpt-6-sol")}, []Group{{ID: 10, Platform: PlatformOpenAI}}, nil)
	repo := &plazaSupportAccountRepo{accounts: []Account{plazaSupportAccount(1, 10, PlatformOpenAI, "gpt-6-sol")}}
	settings := &plazaSyncSettingRepo{}
	svc.accountRepo, svc.settingRepo = repo, settings
	_, err := svc.SyncModels(ctx)
	require.NoError(t, err)
	saved := settings.value
	sentinel := errors.New("database unavailable")
	repo.err = sentinel
	_, err = svc.SyncModels(ctx)
	require.ErrorIs(t, err, sentinel)
	require.Equal(t, saved, settings.value)
	repo.err = nil
	repo.accounts = nil
	settings.writeErr = sentinel
	_, err = svc.SyncModels(ctx)
	require.ErrorIs(t, err, sentinel)
	require.Equal(t, saved, settings.value)
	settings.readErr = sentinel
	out, err := svc.ListGroups(ctx)
	require.ErrorIs(t, err, sentinel)
	require.Nil(t, out)
	settings.readErr = nil
	settings.value = "null"
	_, err = svc.ListGroups(ctx)
	require.Error(t, err)
}

func TestModelPlazaSync_UnrestrictedAccountsUseGatewaySupport(t *testing.T) {
	svc := newPlazaService([]Channel{plazaPricedChannel(1, "ch", []int64{10}, PlatformOpenAI, "gpt-6-sol", "gpt-image-2")}, []Group{{ID: 10, Platform: PlatformOpenAI}}, nil)
	// An empty mapping means unrestricted support for an API-key account.
	svc.accountRepo = &plazaSupportAccountRepo{accounts: []Account{plazaSupportAccount(1, 10, PlatformOpenAI)}}
	svc.settingRepo = &plazaSyncSettingRepo{}
	result, err := svc.SyncModels(context.Background())
	require.NoError(t, err)
	require.Equal(t, 2, result.ModelCount)
}

func TestModelPlazaSync_GroupMappingIsolation(t *testing.T) {
	first := plazaPricedChannel(1, "first", []int64{10}, PlatformOpenAI, "gpt-6-sol")
	first.ModelMapping = map[string]map[string]string{PlatformOpenAI: {"alias": "gpt-6-sol"}}
	second := plazaPricedChannel(2, "second", []int64{20}, PlatformOpenAI, "gpt-image-2")
	second.ModelMapping = map[string]map[string]string{PlatformOpenAI: {"alias": "gpt-image-2"}}
	svc := newPlazaService([]Channel{first, second}, []Group{{ID: 10, Platform: PlatformOpenAI}, {ID: 20, Platform: PlatformOpenAI}}, nil)
	svc.accountRepo = &plazaSupportAccountRepo{accounts: []Account{
		plazaSupportAccount(1, 10, PlatformOpenAI, "gpt-6-sol"),
		plazaSupportAccount(2, 20, PlatformOpenAI, "gpt-6-sol"),
	}}
	svc.settingRepo = &plazaSyncSettingRepo{}
	_, err := svc.SyncModels(context.Background())
	require.NoError(t, err)
	out, err := svc.ListGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Equal(t, int64(10), out[0].ID)
	require.Equal(t, "alias", out[0].Models[0].Name)
}

func TestModelPlazaSync_MixedAccountsRequireOptIn(t *testing.T) {
	channel := plazaPricedChannel(1, "mixed", []int64{10, 20}, PlatformAnthropic, "claude-sonnet-4-5")
	svc := newPlazaService([]Channel{channel}, []Group{{ID: 10, Platform: PlatformAnthropic}, {ID: 20, Platform: PlatformAnthropic}}, nil)
	first := plazaSupportAccount(1, 10, PlatformAntigravity, "claude-sonnet-4-5")
	first.Extra = map[string]any{"mixed_scheduling": true}
	second := plazaSupportAccount(2, 20, PlatformAntigravity, "claude-sonnet-4-5")
	svc.accountRepo = &plazaSupportAccountRepo{accounts: []Account{first, second}}
	svc.settingRepo = &plazaSyncSettingRepo{}
	_, err := svc.SyncModels(context.Background())
	require.NoError(t, err)
	out, err := svc.ListGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Equal(t, int64(10), out[0].ID)
}

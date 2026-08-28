//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type channelMonitorCooldownSettingsRepoStub struct {
	value     string
	readError error
	setError  error
	setCalls  int
}

func (r *channelMonitorCooldownSettingsRepoStub) Get(context.Context, string) (*Setting, error) {
	return nil, errors.New("unexpected Get")
}

func (r *channelMonitorCooldownSettingsRepoStub) GetValue(context.Context, string) (string, error) {
	if r.readError != nil {
		return "", r.readError
	}
	return r.value, nil
}

func (r *channelMonitorCooldownSettingsRepoStub) Set(_ context.Context, _, value string) error {
	r.setCalls++
	if r.setError != nil {
		return r.setError
	}
	r.value = value
	return nil
}

func (r *channelMonitorCooldownSettingsRepoStub) GetMultiple(context.Context, []string) (map[string]string, error) {
	return nil, errors.New("unexpected GetMultiple")
}

func (r *channelMonitorCooldownSettingsRepoStub) SetMultiple(context.Context, map[string]string) error {
	return errors.New("unexpected SetMultiple")
}

func (r *channelMonitorCooldownSettingsRepoStub) GetAll(context.Context) (map[string]string, error) {
	return nil, errors.New("unexpected GetAll")
}

func (r *channelMonitorCooldownSettingsRepoStub) Delete(context.Context, string) error {
	return errors.New("unexpected Delete")
}

func TestChannelMonitorCooldownSettings_DefaultsAndClone(t *testing.T) {
	defaults := DefaultChannelMonitorCooldownSettings()
	require.Equal(t, 1, defaults.Version)
	require.Equal(t, []int{2, 5, 30, 60, 120}, defaults.CooldownMinutes)
	require.Equal(t, 12, defaults.SlowResponseThresholdSeconds)
	require.Equal(t, 1, defaults.PriorityIncrement)
	require.Equal(t, 3, defaults.MaxPriorityIncrease)
	require.Equal(t, 3600, defaults.PriorityAutoRecoverySeconds)

	defaults.CooldownMinutes[0] = 99
	require.Equal(t, 2, DefaultChannelMonitorCooldownSettings().CooldownMinutes[0])
}

func TestNormalizeChannelMonitorCooldownSettings_RejectsInvalidValues(t *testing.T) {
	valid := DefaultChannelMonitorCooldownSettings()
	cases := []struct {
		name string
		edit func(*ChannelMonitorCooldownSettings)
	}{
		{name: "wrong version", edit: func(v *ChannelMonitorCooldownSettings) { v.Version = 2 }},
		{name: "wrong ladder length", edit: func(v *ChannelMonitorCooldownSettings) { v.CooldownMinutes = []int{2, 5} }},
		{name: "not increasing", edit: func(v *ChannelMonitorCooldownSettings) { v.CooldownMinutes = []int{2, 5, 5, 60, 120} }},
		{name: "non positive", edit: func(v *ChannelMonitorCooldownSettings) { v.CooldownMinutes[0] = 0 }},
		{name: "threshold out of range", edit: func(v *ChannelMonitorCooldownSettings) { v.SlowResponseThresholdSeconds = 0 }},
		{name: "increment out of range", edit: func(v *ChannelMonitorCooldownSettings) { v.PriorityIncrement = 0 }},
		{name: "max increase out of range", edit: func(v *ChannelMonitorCooldownSettings) { v.MaxPriorityIncrease = 0 }},
		{name: "recovery out of range", edit: func(v *ChannelMonitorCooldownSettings) { v.PriorityAutoRecoverySeconds = 0 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			candidate := *valid
			candidate.CooldownMinutes = append([]int(nil), valid.CooldownMinutes...)
			tc.edit(&candidate)
			_, err := NormalizeChannelMonitorCooldownSettings(&candidate)
			require.Error(t, err)
		})
	}
}

func TestSettingService_ChannelMonitorCooldownSettings_UsesLastValidOnReadFailure(t *testing.T) {
	repo := &channelMonitorCooldownSettingsRepoStub{}
	svc := NewSettingService(repo, nil)

	first, err := svc.SetChannelMonitorCooldownSettings(context.Background(), &ChannelMonitorCooldownSettings{
		Version:                      1,
		CooldownMinutes:              []int{3, 6, 9, 12, 15},
		SlowResponseThresholdSeconds: 20,
		PriorityIncrement:            2,
		MaxPriorityIncrease:          4,
		PriorityAutoRecoverySeconds:  7200,
	})
	require.NoError(t, err)
	require.Equal(t, []int{3, 6, 9, 12, 15}, first.CooldownMinutes)

	repo.readError = errors.New("temporary store outage")
	got, err := svc.GetChannelMonitorCooldownSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, first, got)

	got.CooldownMinutes[0] = 100
	again, err := svc.GetChannelMonitorCooldownSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, 3, again.CooldownMinutes[0])
}

func TestSettingService_ChannelMonitorCooldownSettings_ReadFailureBeforeAnyValidUsesDefaults(t *testing.T) {
	repo := &channelMonitorCooldownSettingsRepoStub{readError: errors.New("temporary store outage")}
	svc := NewSettingService(repo, nil)
	got, err := svc.GetChannelMonitorCooldownSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, DefaultChannelMonitorCooldownSettings(), got)
}

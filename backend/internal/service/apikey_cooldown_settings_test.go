package service

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type apiKeyCooldownSettingRepo struct {
	mu      sync.Mutex
	values  map[string]string
	readErr error
	setErr  error
}

func (r *apiKeyCooldownSettingRepo) Get(_ context.Context, key string) (*Setting, error) {
	value, err := r.GetValue(context.Background(), key)
	if err != nil {
		return nil, err
	}
	return &Setting{Key: key, Value: value}, nil
}

func (r *apiKeyCooldownSettingRepo) GetValue(_ context.Context, key string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.readErr != nil {
		return "", r.readErr
	}
	return r.values[key], nil
}

func (r *apiKeyCooldownSettingRepo) Set(_ context.Context, key, value string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.setErr != nil {
		return r.setErr
	}
	r.values[key] = value
	return nil
}

func (r *apiKeyCooldownSettingRepo) GetMultiple(ctx context.Context, keys []string) (map[string]string, error) {
	result := make(map[string]string, len(keys))
	for _, key := range keys {
		value, err := r.GetValue(ctx, key)
		if err != nil {
			return nil, err
		}
		result[key] = value
	}
	return result, nil
}

func (r *apiKeyCooldownSettingRepo) SetMultiple(ctx context.Context, values map[string]string) error {
	for key, value := range values {
		if err := r.Set(ctx, key, value); err != nil {
			return err
		}
	}
	return nil
}

func (r *apiKeyCooldownSettingRepo) GetAll(context.Context) (map[string]string, error) {
	return nil, nil
}

func (r *apiKeyCooldownSettingRepo) Delete(_ context.Context, key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.values, key)
	return nil
}

func TestAPIKeyFailureCooldownSettings_DefaultAndRuntimeRefresh(t *testing.T) {
	repo := &apiKeyCooldownSettingRepo{values: map[string]string{}}
	svc := NewSettingService(repo, &config.Config{})

	got, err := svc.GetAPIKeyFailureCooldownSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, DefaultAPIKeyFailureCooldownSettings(), got)

	override := DefaultAPIKeyFailureCooldownSettings()
	policy := override.Policies[APIKeyFailureRateLimit]
	policy.Cooldowns = []int{7, 19}
	override.Policies[APIKeyFailureRateLimit] = policy
	raw, err := json.Marshal(override)
	require.NoError(t, err)
	repo.values[SettingKeyAPIKeyFailureCooldownSettings] = string(raw)

	got, err = svc.GetAPIKeyFailureCooldownSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, []int{7, 19}, got.Policies[APIKeyFailureRateLimit].Cooldowns)
}

func TestAPIKeyFailureCooldownSettings_InvalidReadUsesLastValidThenDefault(t *testing.T) {
	repo := &apiKeyCooldownSettingRepo{values: map[string]string{}}
	svc := NewSettingService(repo, &config.Config{})

	valid := DefaultAPIKeyFailureCooldownSettings()
	policy := valid.Policies[APIKeyFailureUnknown]
	policy.Cooldowns = []int{3, 5, 8}
	valid.Policies[APIKeyFailureUnknown] = policy
	raw, err := json.Marshal(valid)
	require.NoError(t, err)
	repo.values[SettingKeyAPIKeyFailureCooldownSettings] = string(raw)

	got, err := svc.GetAPIKeyFailureCooldownSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, []int{3, 5, 8}, got.Policies[APIKeyFailureUnknown].Cooldowns)

	repo.values[SettingKeyAPIKeyFailureCooldownSettings] = `{"version":99,"policies":{}}`
	got, err = svc.GetAPIKeyFailureCooldownSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, []int{3, 5, 8}, got.Policies[APIKeyFailureUnknown].Cooldowns)

	repo.readErr = errors.New("settings unavailable")
	got, err = svc.GetAPIKeyFailureCooldownSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, []int{3, 5, 8}, got.Policies[APIKeyFailureUnknown].Cooldowns)

	fresh := NewSettingService(&apiKeyCooldownSettingRepo{values: map[string]string{}, readErr: errors.New("down")}, &config.Config{})
	got, err = fresh.GetAPIKeyFailureCooldownSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, DefaultAPIKeyFailureCooldownSettings(), got)
}

func TestSetAPIKeyFailureCooldownSettings_ValidatesNormalizesAndUpdatesCache(t *testing.T) {
	repo := &apiKeyCooldownSettingRepo{values: map[string]string{}}
	svc := NewSettingService(repo, &config.Config{})

	valid := DefaultAPIKeyFailureCooldownSettings()
	valid.Policies[APIKeyFailureRateLimit] = APIKeyCooldownPolicy{
		Enabled: true, Cooldowns: []int{9, 3, 9, 6}, Mode: APIKeyCooldownModeHoldLast,
	}
	updated, err := svc.SetAPIKeyFailureCooldownSettings(context.Background(), valid)
	require.NoError(t, err)
	require.Equal(t, []int{3, 6, 9}, updated.Policies[APIKeyFailureRateLimit].Cooldowns)

	repo.readErr = errors.New("down after save")
	got, err := svc.GetAPIKeyFailureCooldownSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, []int{3, 6, 9}, got.Policies[APIKeyFailureRateLimit].Cooldowns)
}

func TestSetAPIKeyFailureCooldownSettings_RejectsInvalidWithoutReplacingLastValid(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*APIKeyFailureCooldownSettings)
	}{
		{name: "unsupported version", mutate: func(s *APIKeyFailureCooldownSettings) { s.Version = 2 }},
		{name: "missing family", mutate: func(s *APIKeyFailureCooldownSettings) { delete(s.Policies, APIKeyFailureUnknown) }},
		{name: "unknown family", mutate: func(s *APIKeyFailureCooldownSettings) {
			s.Policies["other"] = APIKeyCooldownPolicy{Enabled: true, Cooldowns: []int{1}, Mode: APIKeyCooldownModeHoldLast}
		}},
		{name: "empty ladder", mutate: func(s *APIKeyFailureCooldownSettings) {
			p := s.Policies[APIKeyFailureUnknown]
			p.Cooldowns = nil
			s.Policies[APIKeyFailureUnknown] = p
		}},
		{name: "non positive duration", mutate: func(s *APIKeyFailureCooldownSettings) {
			p := s.Policies[APIKeyFailureUnknown]
			p.Cooldowns = []int{0}
			s.Policies[APIKeyFailureUnknown] = p
		}},
		{name: "unknown mode", mutate: func(s *APIKeyFailureCooldownSettings) {
			p := s.Policies[APIKeyFailureUnknown]
			p.Mode = "random"
			s.Policies[APIKeyFailureUnknown] = p
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &apiKeyCooldownSettingRepo{values: map[string]string{}}
			svc := NewSettingService(repo, &config.Config{})
			baseline, err := svc.SetAPIKeyFailureCooldownSettings(context.Background(), DefaultAPIKeyFailureCooldownSettings())
			require.NoError(t, err)

			invalid := cloneAPIKeyFailureCooldownSettings(baseline)
			tt.mutate(invalid)
			_, err = svc.SetAPIKeyFailureCooldownSettings(context.Background(), invalid)
			require.Error(t, err)

			repo.readErr = errors.New("force cache")
			got, err := svc.GetAPIKeyFailureCooldownSettings(context.Background())
			require.NoError(t, err)
			require.Equal(t, baseline, got)
		})
	}
}

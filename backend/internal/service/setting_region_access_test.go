package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestRegionAccessSettings_DefaultAndInvalidStoredValues(t *testing.T) {
	ctx := context.Background()
	repo := newRuntimeSettingRepoStub()
	svc := NewSettingService(repo, &config.Config{})
	settings, err := svc.GetRegionAccessSettings(ctx)
	require.NoError(t, err)
	require.False(t, settings.Enabled)

	for _, raw := range []string{"", " ", "null", "{}", `{"enabled":null}`, `{"enabled":"true"}`, "{bad-json"} {
		t.Run(raw, func(t *testing.T) {
			repo.values[SettingKeyRegionAccessSettings] = raw
			settings, err := svc.GetRegionAccessSettings(ctx)
			require.Error(t, err)
			require.Nil(t, settings, "损坏配置不能转换为关闭状态")
		})
	}
}

func TestRegionAccessSettings_CacheAndImmediateSave(t *testing.T) {
	ctx := context.Background()
	repo := newRuntimeSettingRepoStub()
	svc := NewSettingService(repo, &config.Config{})
	for i := 0; i < 3; i++ {
		settings, err := svc.GetRegionAccessSettingsCached(ctx)
		require.NoError(t, err)
		require.False(t, settings.Enabled)
	}
	require.Equal(t, 1, repo.getValueCalls)

	require.NoError(t, svc.SetRegionAccessSettings(ctx, &RegionAccessSettings{Enabled: true}))
	settings, err := svc.GetRegionAccessSettingsCached(ctx)
	require.NoError(t, err)
	require.True(t, settings.Enabled)
	require.JSONEq(t, `{"enabled":true}`, repo.values[SettingKeyRegionAccessSettings])
	settings.Enabled = false
	settings, err = svc.GetRegionAccessSettingsCached(ctx)
	require.NoError(t, err)
	require.True(t, settings.Enabled, "调用者不能修改共享缓存")

	require.NoError(t, svc.SetRegionAccessSettings(ctx, &RegionAccessSettings{Enabled: false}))
	settings, err = svc.GetRegionAccessSettingsCached(ctx)
	require.NoError(t, err)
	require.False(t, settings.Enabled)
	require.Equal(t, 1, repo.getValueCalls)
}

func TestRegionAccessSettings_ReadFailureNeverReturnsDisabledFallback(t *testing.T) {
	ctx := context.Background()
	repo := newRuntimeSettingRepoStub()
	svc := NewSettingService(repo, &config.Config{})
	require.NoError(t, svc.SetRegionAccessSettings(ctx, &RegionAccessSettings{}))
	svc.regionAccessCache.Store(&cachedRegionAccessSettings{settings: RegionAccessSettings{}, expires: time.Now().Add(-time.Second)})
	wantErr := errors.New("database unavailable")
	repo.getValueFn = func(string) (string, error) { return "", wantErr }
	for i := 0; i < 3; i++ {
		settings, err := svc.GetRegionAccessSettingsCached(ctx)
		require.ErrorIs(t, err, wantErr)
		require.Nil(t, settings)
	}
	require.Equal(t, 1, repo.getValueCalls, "短暂缓存错误以限制数据库故障放大")

	repo.getValueFn = nil
	require.NoError(t, svc.SetRegionAccessSettings(ctx, &RegionAccessSettings{Enabled: true}))
	settings, err := svc.GetRegionAccessSettingsCached(ctx)
	require.NoError(t, err)
	require.True(t, settings.Enabled, "保存成功须立即替换错误缓存")
}

func TestRegionAccessSettings_WriteFailurePreservesEnabledCache(t *testing.T) {
	ctx := context.Background()
	repo := newRuntimeSettingRepoStub()
	svc := NewSettingService(repo, &config.Config{})
	require.NoError(t, svc.SetRegionAccessSettings(ctx, &RegionAccessSettings{Enabled: true}))
	wantErr := errors.New("write failed")
	repo.setFn = func(string, string) error { return wantErr }
	require.ErrorIs(t, svc.SetRegionAccessSettings(ctx, &RegionAccessSettings{}), wantErr)
	settings, err := svc.GetRegionAccessSettingsCached(ctx)
	require.NoError(t, err)
	require.True(t, settings.Enabled)
	require.JSONEq(t, `{"enabled":true}`, repo.values[SettingKeyRegionAccessSettings])
	require.Error(t, svc.SetRegionAccessSettings(ctx, nil))
}

func TestRegionAccessSettings_ConcurrentRefreshCannotOverwriteSave(t *testing.T) {
	ctx := context.Background()
	repo := &panelRateLimitSettingRepo{}
	entered := make(chan struct{})
	release := make(chan struct{})
	blocking := &regionAccessBlockingRepo{SettingRepository: repo, entered: entered, release: release}
	svc := NewSettingService(blocking, &config.Config{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = svc.GetRegionAccessSettingsCached(ctx)
	}()
	<-entered
	saved := make(chan error, 1)
	go func() {
		defer wg.Done()
		saved <- svc.SetRegionAccessSettings(ctx, &RegionAccessSettings{Enabled: true})
	}()
	close(release)
	wg.Wait()
	require.NoError(t, <-saved)
	settings, err := svc.GetRegionAccessSettingsCached(ctx)
	require.NoError(t, err)
	require.True(t, settings.Enabled)
}

type regionAccessBlockingRepo struct {
	SettingRepository
	entered chan struct{}
	release chan struct{}
}

func (r *regionAccessBlockingRepo) GetValue(ctx context.Context, key string) (string, error) {
	value, err := r.SettingRepository.GetValue(ctx, key)
	close(r.entered)
	<-r.release
	return value, err
}

func TestRegionAccessSettings_MissingRepositoryFailsClosed(t *testing.T) {
	for _, svc := range []*SettingService{nil, {}} {
		settings, err := svc.GetRegionAccessSettingsCached(context.Background())
		require.Error(t, err)
		require.Nil(t, settings)
	}
}

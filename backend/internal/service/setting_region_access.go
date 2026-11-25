package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// RegionAccessSettings 控制是否禁止中国大陆所有账号新登录；首次安装默认关闭。
// 管理员没有例外；不限制接口密钥调用或已有登录会话。
type RegionAccessSettings struct {
	Enabled bool `json:"enabled"`
}

const (
	regionAccessCacheTTL  = 5 * time.Second
	regionAccessErrorTTL  = time.Second
	regionAccessDBTimeout = 5 * time.Second
)

type cachedRegionAccessSettings struct {
	settings RegionAccessSettings
	err      error
	expires  time.Time
}

// GetRegionAccessSettings 直接读取配置；只有从未配置时使用关闭的默认值。
// 已存在但为空、损坏或读取失败的配置一律报错，不得导致地区限制自动关闭。
func (s *SettingService) GetRegionAccessSettings(ctx context.Context) (*RegionAccessSettings, error) {
	if s == nil || s.settingRepo == nil {
		return nil, fmt.Errorf("region access settings repository is unavailable")
	}
	value, err := s.settingRepo.GetValue(ctx, SettingKeyRegionAccessSettings)
	if errors.Is(err, ErrSettingNotFound) {
		return &RegionAccessSettings{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get region access settings: %w", err)
	}
	var stored struct {
		Enabled *bool `json:"enabled"`
	}
	if err := json.Unmarshal([]byte(value), &stored); err != nil {
		return nil, fmt.Errorf("decode region access settings: %w", err)
	}
	if stored.Enabled == nil {
		return nil, fmt.Errorf("region access settings is missing enabled")
	}
	return &RegionAccessSettings{Enabled: *stored.Enabled}, nil
}

// SetRegionAccessSettings 持久化开关并刷新本节点缓存；其他节点至多在 5 秒后读取新值。
func (s *SettingService) SetRegionAccessSettings(ctx context.Context, settings *RegionAccessSettings) error {
	if s == nil || s.settingRepo == nil {
		return fmt.Errorf("region access settings repository is unavailable")
	}
	if settings == nil {
		return fmt.Errorf("region access settings cannot be nil")
	}
	snapshot := *settings
	data, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("encode region access settings: %w", err)
	}

	s.regionAccessMu.Lock()
	defer s.regionAccessMu.Unlock()
	if err := s.settingRepo.Set(ctx, SettingKeyRegionAccessSettings, string(data)); err != nil {
		return fmt.Errorf("save region access settings: %w", err)
	}
	s.regionAccessCache.Store(&cachedRegionAccessSettings{
		settings: snapshot,
		expires:  time.Now().Add(regionAccessCacheTTL),
	})
	return nil
}

// GetRegionAccessSettingsCached 在请求热路径使用短缓存，读取失败原样返回错误。
// 错误仅缓存 1 秒以避免数据库故障时请求堆积，不返回之前的关闭值来放行登录。
func (s *SettingService) GetRegionAccessSettingsCached(ctx context.Context) (*RegionAccessSettings, error) {
	if s == nil || s.settingRepo == nil {
		return nil, fmt.Errorf("region access settings repository is unavailable")
	}
	if cached := s.regionAccessCache.Load(); cached != nil && time.Now().Before(cached.expires) {
		return copyRegionAccessCache(cached)
	}

	s.regionAccessMu.Lock()
	defer s.regionAccessMu.Unlock()
	if cached := s.regionAccessCache.Load(); cached != nil && time.Now().Before(cached.expires) {
		return copyRegionAccessCache(cached)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), regionAccessDBTimeout)
	defer cancel()
	settings, err := s.GetRegionAccessSettings(dbCtx)
	cached := &cachedRegionAccessSettings{err: err, expires: time.Now().Add(regionAccessErrorTTL)}
	if err == nil {
		cached.settings = *settings
		cached.expires = time.Now().Add(regionAccessCacheTTL)
	}
	s.regionAccessCache.Store(cached)
	return copyRegionAccessCache(cached)
}

func copyRegionAccessCache(cached *cachedRegionAccessSettings) (*RegionAccessSettings, error) {
	if cached.err != nil {
		return nil, cached.err
	}
	settings := cached.settings
	return &settings, nil
}

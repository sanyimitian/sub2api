package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// RegionAccessSettings 控制是否禁止中国大陆所有账号新登录；首次安装默认关闭。
// 管理员没有例外；SiteWide 关闭时只限制新登录，开启时限制全部请求。
// 同时保存首次访问者使用的默认界面语言。
type RegionAccessSettings struct {
	Enabled         bool   `json:"enabled"`
	SiteWide        bool   `json:"site_wide"`
	DefaultLanguage string `json:"default_language"`
}

const (
	regionAccessCacheTTL  = 5 * time.Second
	regionAccessErrorTTL  = time.Second
	regionAccessDBTimeout = 5 * time.Second
)

type cachedRegionAccessSettings struct {
	settings RegionAccessSettings
	known    bool // 保留最近确认的作用范围；登录校验仍严格返回读取错误。
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
		return &RegionAccessSettings{DefaultLanguage: "zh"}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get region access settings: %w", err)
	}
	var stored struct {
		Enabled         *bool           `json:"enabled"`
		SiteWide        json.RawMessage `json:"site_wide"`
		DefaultLanguage string          `json:"default_language"`
	}
	if err := json.Unmarshal([]byte(value), &stored); err != nil {
		return nil, fmt.Errorf("decode region access settings: %w", err)
	}
	if stored.Enabled == nil {
		return nil, fmt.Errorf("region access settings is missing enabled")
	}
	var siteWide bool
	if len(stored.SiteWide) > 0 {
		var value *bool
		if err := json.Unmarshal(stored.SiteWide, &value); err != nil || value == nil {
			return nil, fmt.Errorf("region access settings site_wide must be a boolean")
		}
		siteWide = *value
	}
	return &RegionAccessSettings{Enabled: *stored.Enabled, SiteWide: siteWide, DefaultLanguage: normalizeDefaultLanguage(stored.DefaultLanguage)}, nil
}

// SetRegionAccessSettings 持久化设置并刷新本节点缓存及页面注入配置。
func (s *SettingService) SetRegionAccessSettings(ctx context.Context, settings *RegionAccessSettings) error {
	if s == nil || s.settingRepo == nil {
		return fmt.Errorf("region access settings repository is unavailable")
	}
	if settings == nil {
		return fmt.Errorf("region access settings cannot be nil")
	}
	snapshot := *settings
	if snapshot.DefaultLanguage != "" && snapshot.DefaultLanguage != "zh" && snapshot.DefaultLanguage != "en" {
		return fmt.Errorf("unsupported default language: %s", snapshot.DefaultLanguage)
	}
	snapshot.DefaultLanguage = normalizeDefaultLanguage(snapshot.DefaultLanguage)
	data, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("encode region access settings: %w", err)
	}

	s.regionAccessMu.Lock()
	if err := s.settingRepo.Set(ctx, SettingKeyRegionAccessSettings, string(data)); err != nil {
		s.regionAccessMu.Unlock()
		return fmt.Errorf("save region access settings: %w", err)
	}
	s.regionAccessCache.Store(&cachedRegionAccessSettings{
		settings: snapshot,
		known:    true,
		expires:  time.Now().Add(regionAccessCacheTTL),
	})
	s.regionAccessMu.Unlock()
	if s.onUpdate != nil {
		s.onUpdate()
	}
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
	if previous := s.regionAccessCache.Load(); previous != nil {
		cached.settings = previous.settings
		cached.known = previous.known
	}
	if err == nil {
		cached.known = true
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

// 旧配置没有默认语言时保持原有中文界面；无效语言不影响地区限制判断。
func normalizeDefaultLanguage(language string) string {
	if language == "en" {
		return "en"
	}
	return "zh"
}

// 公开设置仅提取语言，不暴露地区限制配置，也不参与登录准入判断。
func defaultLanguageFromRegionSettings(raw string) string {
	var settings struct {
		DefaultLanguage string `json:"default_language"`
	}
	if err := json.Unmarshal([]byte(raw), &settings); err != nil {
		return "zh"
	}
	return normalizeDefaultLanguage(settings.DefaultLanguage)
}

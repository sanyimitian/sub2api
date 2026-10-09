package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type cachedOpenAICodexTicketEnabled struct {
	value     bool
	expiresAt int64
}

const openAICodexTicketEnabledCacheTTL = 5 * time.Second

func (s *SettingService) GetOpenAICodexTicketEnabled(ctx context.Context, fallback bool) bool {
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil || s == nil || s.settingRepo == nil {
		return fallback
	}
	if cached, ok := s.openAICodexTicketEnabledCache.Load().(*cachedOpenAICodexTicketEnabled); ok && cached != nil && time.Now().UnixNano() < cached.expiresAt {
		return cached.value
	}
	resultCh := s.openAICodexTicketEnabledSF.DoChan(SettingKeyOpenAICodexTicketEnabled, func() (any, error) {
		snapshot := s.openAICodexTicketEnabledCache.Load()
		if cached, ok := snapshot.(*cachedOpenAICodexTicketEnabled); ok && cached != nil && time.Now().UnixNano() < cached.expiresAt {
			return cached.value, nil
		}
		dbCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		value, err := s.settingRepo.GetValue(dbCtx, SettingKeyOpenAICodexTicketEnabled)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err != nil && !errors.Is(err, ErrSettingNotFound) {
			if cached, ok := s.openAICodexTicketEnabledCache.Load().(*cachedOpenAICodexTicketEnabled); ok && cached != nil {
				return cached.value, nil
			}
			return fallback, nil
		}
		enabled := fallback
		if err == nil && strings.TrimSpace(value) != "" {
			enabled = strings.TrimSpace(value) == "true"
		}
		s.openAICodexTicketEnabledCache.CompareAndSwap(snapshot, &cachedOpenAICodexTicketEnabled{value: enabled, expiresAt: time.Now().Add(openAICodexTicketEnabledCacheTTL).UnixNano()})
		return enabled, nil
	})
	select {
	case <-ctx.Done():
		return fallback
	case result := <-resultCh:
		if value, ok := result.Val.(bool); ok && result.Err == nil {
			return value
		}
		return fallback
	}
}

func (s *SettingService) InvalidateOpenAICodexTicketEnabledCache() {
	if s == nil {
		return
	}
	s.openAICodexTicketEnabledSF.Forget(SettingKeyOpenAICodexTicketEnabled)
	s.openAICodexTicketEnabledCache.Store(&cachedOpenAICodexTicketEnabled{expiresAt: 0})
}

type cachedOpenAICodexTicketFailClosed struct {
	value     bool
	expiresAt int64
}

const openAICodexTicketFailClosedCacheTTL = 5 * time.Second

func (s *SettingService) GetOpenAICodexTicketFailClosed(ctx context.Context) bool {
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil || s == nil || s.settingRepo == nil {
		return false
	}
	if cached, ok := s.openAICodexTicketFailClosedCache.Load().(*cachedOpenAICodexTicketFailClosed); ok && cached != nil && time.Now().UnixNano() < cached.expiresAt {
		return cached.value
	}
	resultCh := s.openAICodexTicketFailClosedSF.DoChan(SettingKeyOpenAICodexTicketFailClosed, func() (any, error) {
		snapshot := s.openAICodexTicketFailClosedCache.Load()
		dbCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		value, err := s.settingRepo.GetValue(dbCtx, SettingKeyOpenAICodexTicketFailClosed)
		if errors.Is(err, ErrSettingNotFound) {
			value, err = "false", nil
		}
		if err != nil {
			if cached, ok := s.openAICodexTicketFailClosedCache.Load().(*cachedOpenAICodexTicketFailClosed); ok && cached != nil {
				return cached.value, nil
			}
			return false, nil
		}
		failClosed := strings.TrimSpace(value) == "true"
		s.openAICodexTicketFailClosedCache.CompareAndSwap(snapshot, &cachedOpenAICodexTicketFailClosed{value: failClosed, expiresAt: time.Now().Add(openAICodexTicketFailClosedCacheTTL).UnixNano()})
		return failClosed, nil
	})
	select {
	case <-ctx.Done():
		return false
	case result := <-resultCh:
		if value, ok := result.Val.(bool); ok && result.Err == nil {
			return value
		}
		return false
	}
}

func (s *SettingService) InvalidateOpenAICodexTicketFailClosedCache() {
	if s == nil {
		return
	}
	s.openAICodexTicketFailClosedSF.Forget(SettingKeyOpenAICodexTicketFailClosed)
	s.openAICodexTicketFailClosedCache.Store(&cachedOpenAICodexTicketFailClosed{expiresAt: 0})
}

type cachedOpenAICodexTicketModels struct {
	models     []string
	configured bool
	expiresAt  int64
}

const openAICodexTicketModelsCacheTTL = 5 * time.Second

func (s *SettingService) GetOpenAICodexTicketModels(ctx context.Context, fallback []string) []string {
	if len(fallback) == 0 {
		fallback = []string{openAICodexTicketDefaultModel, openAICodexTicketDefaultSolModel}
	}
	fallback = NormalizeOpenAICodexTicketModels(fallback)
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil || s.settingRepo == nil || ctx.Err() != nil {
		return fallback
	}
	if cached, ok := s.openAICodexTicketModelsCache.Load().(*cachedOpenAICodexTicketModels); ok && cached != nil && time.Now().UnixNano() < cached.expiresAt {
		if cached.configured {
			return NormalizeOpenAICodexTicketModels(cached.models)
		}
		return fallback
	}
	resultCh := s.openAICodexTicketModelsSF.DoChan(SettingKeyOpenAICodexTicketModels, func() (any, error) {
		snapshot := s.openAICodexTicketModelsCache.Load()
		dbCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		value, err := s.settingRepo.GetValue(dbCtx, SettingKeyOpenAICodexTicketModels)
		if errors.Is(err, ErrSettingNotFound) {
			s.openAICodexTicketModelsCache.CompareAndSwap(snapshot, &cachedOpenAICodexTicketModels{expiresAt: time.Now().Add(openAICodexTicketModelsCacheTTL).UnixNano()})
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		var models []string
		if err := json.Unmarshal([]byte(value), &models); err != nil {
			return nil, err
		}
		models = NormalizeOpenAICodexTicketModels(models)
		s.openAICodexTicketModelsCache.CompareAndSwap(snapshot, &cachedOpenAICodexTicketModels{models: models, configured: true, expiresAt: time.Now().Add(openAICodexTicketModelsCacheTTL).UnixNano()})
		return models, nil
	})
	select {
	case <-ctx.Done():
		return fallback
	case result := <-resultCh:
		if result.Err == nil {
			if models, ok := result.Val.([]string); ok {
				return NormalizeOpenAICodexTicketModels(models)
			}
			return fallback
		}
		if cached, ok := s.openAICodexTicketModelsCache.Load().(*cachedOpenAICodexTicketModels); ok && cached != nil && cached.configured {
			return NormalizeOpenAICodexTicketModels(cached.models)
		}
		return fallback
	}
}

func (s *SettingService) InvalidateOpenAICodexTicketModelsCache() {
	if s == nil {
		return
	}
	s.openAICodexTicketModelsSF.Forget(SettingKeyOpenAICodexTicketModels)
	s.openAICodexTicketModelsCache.Store(&cachedOpenAICodexTicketModels{expiresAt: 0})
}

type cachedOpenAICodexTicketHarvestProxy struct {
	value     string
	expiresAt int64
}

const openAICodexTicketHarvestProxyCacheTTL = 5 * time.Second

func (s *SettingService) GetOpenAICodexTicketHarvestProxyURL(ctx context.Context) string {
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil || s == nil || s.settingRepo == nil {
		return ""
	}
	if cached, ok := s.openAICodexTicketHarvestProxyCache.Load().(*cachedOpenAICodexTicketHarvestProxy); ok && cached != nil && time.Now().UnixNano() < cached.expiresAt {
		return cached.value
	}
	resultCh := s.openAICodexTicketHarvestProxySF.DoChan(SettingKeyOpenAICodexTicketHarvestProxyURL, func() (any, error) {
		snapshot := s.openAICodexTicketHarvestProxyCache.Load()
		if cached, ok := snapshot.(*cachedOpenAICodexTicketHarvestProxy); ok && cached != nil && time.Now().UnixNano() < cached.expiresAt {
			return cached.value, nil
		}
		dbCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		value, err := s.settingRepo.GetValue(dbCtx, SettingKeyOpenAICodexTicketHarvestProxyURL)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err != nil && !errors.Is(err, ErrSettingNotFound) {
			if cached, ok := s.openAICodexTicketHarvestProxyCache.Load().(*cachedOpenAICodexTicketHarvestProxy); ok && cached != nil {
				value = cached.value
			}
			s.openAICodexTicketHarvestProxyCache.CompareAndSwap(snapshot, &cachedOpenAICodexTicketHarvestProxy{value: value, expiresAt: time.Now().Add(time.Second).UnixNano()})
			return value, nil
		}
		value = strings.TrimSpace(value)
		s.openAICodexTicketHarvestProxyCache.CompareAndSwap(snapshot, &cachedOpenAICodexTicketHarvestProxy{value: value, expiresAt: time.Now().Add(openAICodexTicketHarvestProxyCacheTTL).UnixNano()})
		return value, nil
	})
	select {
	case <-ctx.Done():
		return ""
	case result := <-resultCh:
		if value, ok := result.Val.(string); ok && result.Err == nil {
			return value
		}
		return ""
	}
}

func (s *SettingService) InvalidateOpenAICodexTicketHarvestProxyCache() {
	if s == nil {
		return
	}
	s.openAICodexTicketHarvestProxySF.Forget(SettingKeyOpenAICodexTicketHarvestProxyURL)
	s.openAICodexTicketHarvestProxyCache.Store(&cachedOpenAICodexTicketHarvestProxy{expiresAt: 0})
}

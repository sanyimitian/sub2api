package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

type OpenAISessionPoolCandidateView struct {
	QualityPassed bool                    `json:"quality_passed"`
	ResultID      string                  `json:"result_id"`
	AccountID     int64                   `json:"account_id"`
	AccountName   string                  `json:"account_name"`
	Model         string                  `json:"model"`
	Transport     OpenAIUpstreamTransport `json:"transport"`
	FirstTokenMS  int                     `json:"first_token_ms"`
	ExpiresAt     time.Time               `json:"expires_at"`
}

type OpenAISessionPoolSnapshot struct {
	Enabled    bool                             `json:"enabled"`
	TargetSize int                              `json:"target_size"`
	ObservedAt time.Time                        `json:"observed_at"`
	Candidates []OpenAISessionPoolCandidateView `json:"candidates"`
}

func (s *OpsService) GetOpenAISessionPoolSnapshot(ctx context.Context) (*OpenAISessionPoolSnapshot, error) {
	if s.openAIGatewayService == nil {
		return nil, fmt.Errorf("备用会话池服务不可用")
	}
	return s.openAIGatewayService.GetOpenAISessionPoolSnapshot(ctx)
}

// GetOpenAISessionPoolSnapshot 只读当前可领取候选，不修改租约，也不输出上游会话标识。
func (s *OpenAIGatewayService) GetOpenAISessionPoolSnapshot(ctx context.Context) (*OpenAISessionPoolSnapshot, error) {
	snapshot := &OpenAISessionPoolSnapshot{Enabled: s.OpenAISessionPoolEnabled(ctx), ObservedAt: time.Now(), Candidates: []OpenAISessionPoolCandidateView{}}
	if s.settingService != nil {
		snapshot.TargetSize = s.settingService.GetOpenAISessionPoolSize(ctx)
	}
	if !snapshot.Enabled {
		return snapshot, nil
	}
	cache, ok := s.cache.(OpenAIWSLatencyCache)
	if !ok {
		return nil, fmt.Errorf("备用会话池缓存不可用")
	}
	routes, err := s.openAISessionPoolRoutes(ctx)
	if err != nil {
		return nil, fmt.Errorf("读取备用会话池账号失败: %w", err)
	}
	cacheCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	raw, err := cache.GetOpenAIWSLatencyState(cacheCtx, openAISessionPoolKey)
	if err != nil {
		return nil, fmt.Errorf("读取备用会话池失败: %w", err)
	}
	var pool openAIWSLatencyCandidatePool
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &pool); err != nil {
			return nil, fmt.Errorf("解析备用会话池失败: %w", err)
		}
	}
	snapshot.ObservedAt = time.Now()
	now := snapshot.ObservedAt.UnixMilli()
	pruneOpenAISessionPool(&pool, routes, now)
	for _, candidate := range pool.Candidates {
		if candidate.Committed || (candidate.LeaseID != "" && candidate.LeaseUntil > now) {
			continue
		}
		for _, route := range routes {
			if openAISessionPoolCandidateMatchesRoute(candidate, route) {
				model := candidate.ProbeModel
				if model == "" {
					model = candidate.UpstreamModel
				}
				snapshot.Candidates = append(snapshot.Candidates, OpenAISessionPoolCandidateView{
					QualityPassed: candidate.QualityPassed,
					ResultID:      candidate.ResultID, AccountID: candidate.AccountID, AccountName: route.account.Name,
					Model: model, Transport: candidate.Transport, FirstTokenMS: candidate.FirstTokenMS,
					ExpiresAt: time.UnixMilli(candidate.ExpiresAt),
				})
				break
			}
		}
	}
	sort.SliceStable(snapshot.Candidates, func(i, j int) bool { return snapshot.Candidates[i].FirstTokenMS < snapshot.Candidates[j].FirstTokenMS })
	return snapshot, nil
}

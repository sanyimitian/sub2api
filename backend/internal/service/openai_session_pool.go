package service

import (
	"context"
	"errors"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

// 低延迟备用会话池的需求背景与目的：
// 本系统通过上游接口密钥调用中转站，无法直接选择中转站内部的 OpenAI 账号。
// 在用户接入的上游中，新会话可能被分配到不同内部账号；用户确认该上游支持粘性会话，
// 因此保留探测会话的上游标识，作为后续请求延续原分配的依据。这里不查询或控制内部账号。
//
// 目标是提前筛选首字延迟低的会话，让新对话首次请求就能领取并使用，减少先遇到慢会话
// 再重连切换的情况。所有显式启用探测的 OpenAI 接口密钥账号合计准备默认 6 个空闲会话，
// 数量可配置；只接纳探测成功且首字严格低于 10 秒的会话，领取后由后台补充。
//
// 探测模型列表控制后台使用哪些模型测速，不限制候选可以服务的用户请求模型。
// 例如使用 gpt-6-sol 探测合格后，只要账号模型白名单允许 gpt-6.1-sol，就可领取同一
// 候选会话来调用 gpt-6.1-sol。实际调用遵循用户请求及已有模型映射，不能改成探测模型。
// 这是一项复用粘性会话的业务规则，不代表其他模型同样快速。开启降智探测时，
// 质量结论也只针对探测模型的本次答题，不能代表其他模型已经通过相同检查。
//
// 有权调用候选所属账号的分组可以共享未使用的候选，不按来源分组隔离；账号权限、
// 请求模型支持情况和连接方式等条件仍须校验。发送用户内容前必须独占候选，之后不能
// 再交给其他用户。无适用候选时，新对话走原调度，已有慢对话保留原线路。
//
// 总开关和账号探测开关均默认关闭。总开关开启时，已有首字慢切换也只领取此共享池，
// 不另起按需探测；总开关关闭时保持原有调度和原首字慢切换流程。

// 仅后台备用池探测携带此标记，旧按需探测保持原处理。
type openAIPreparedProbeContextKey struct{}

func isOpenAIPreparedProbe(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	enabled, _ := ctx.Value(openAIPreparedProbeContextKey{}).(bool)
	return enabled
}

func (s *OpenAIGatewayService) OpenAISessionPoolEnabled(ctx context.Context) bool {
	return s != nil && s.settingService != nil && s.settingService.IsOpenAISessionPoolEnabled(ctx)
}

const openAISessionPoolKey = "candidate_pool:openai-prepared-sessions:v1"
const openAISessionPoolRetryInterval = 30 * time.Minute

type openAISessionPoolRoute struct {
	account       *Account
	model         string
	upstreamModel string
	transport     OpenAIUpstreamTransport
	fingerprint   string
}

func (s *OpenAIGatewayService) StartOpenAISessionPool() {
	if s == nil || s.accountRepo == nil || s.settingService == nil {
		return
	}
	if _, ok := s.cache.(OpenAIWSLatencyCache); !ok {
		return
	}
	s.openaiSessionPoolOnce.Do(func() {
		ctx, cancel := context.WithCancel(context.Background())
		s.openaiSessionPoolCancel = cancel
		s.openaiSessionPoolDone = make(chan struct{})
		s.openaiSessionPoolWake = make(chan struct{}, 1)
		go func() {
			defer close(s.openaiSessionPoolDone)
			ticker := time.NewTicker(5 * time.Second)
			defer ticker.Stop()
			for {
				s.refillOpenAISessionPool(ctx)
				select {
				case <-ctx.Done():
					return
				case <-s.openaiSessionPoolWake:
				case <-ticker.C:
				}
			}
		}()
	})
}

func (s *OpenAIGatewayService) wakeOpenAISessionPool() {
	if s == nil || s.openaiSessionPoolWake == nil {
		return
	}
	select {
	case s.openaiSessionPoolWake <- struct{}{}:
	default:
	}
}

func (s *OpenAIGatewayService) openAISessionPoolRoutes(ctx context.Context) ([]openAISessionPoolRoute, error) {
	accounts, err := s.accountRepo.ListSchedulableByPlatform(ctx, PlatformOpenAI)
	if err != nil {
		return nil, err
	}
	checker := &defaultOpenAIAccountScheduler{service: s}
	var routes []openAISessionPoolRoute
	for i := range accounts {
		account := &accounts[i]
		if !account.OpenAISessionPoolEnabled() || !account.IsSchedulable() {
			continue
		}
		if err := ValidateOpenAISessionPoolExtra(account.Platform, account.Type, account.Extra); err != nil {
			continue
		}
		for _, model := range account.OpenAISessionPoolModels() {
			request := OpenAIAccountScheduleRequest{Platform: PlatformOpenAI, RequestedModel: model, RequiredCapability: OpenAIEndpointCapabilityResponses}
			if !checker.isAccountRequestCompatible(ctx, account, request) {
				continue
			}
			upstream := resolveOpenAIAccountUpstreamModelForRequest(account, model, false)
			if upstream == "" {
				upstream = model
			}
			seen := make(map[OpenAIUpstreamTransport]bool)
			for _, ingress := range []OpenAIUpstreamTransport{OpenAIUpstreamTransportHTTPSSE, OpenAIUpstreamTransportResponsesWebsocketV2Ingress} {
				transport := s.openAISessionPoolTransport(account, ingress)
				if !seen[transport] && checker.isAccountTransportCompatible(account, ingress) {
					seen[transport] = true
					routes = append(routes, openAISessionPoolRoute{account: account, model: model, upstreamModel: upstream, transport: transport, fingerprint: openAISessionPoolAccountFingerprint(account)})
				}
			}
		}
	}
	return routes, nil
}

func openAISessionPoolCandidateMatchesRoute(candidate openAIWSLatencyReusableCandidate, route openAISessionPoolRoute) bool {
	if quality, err := route.account.OpenAISessionQualityConfig(); err != nil || (quality != nil && quality.Enabled && !candidate.QualityPassed) {
		return false
	}
	return candidate.AccountID == route.account.ID && candidate.UpstreamModel == route.upstreamModel && candidate.Transport == route.transport && candidate.AccountFingerprint == route.fingerprint && (candidate.ProbeModel == "" || candidate.ProbeModel == route.model)
}

func pruneOpenAISessionPool(pool *openAIWSLatencyCandidatePool, routes []openAISessionPoolRoute, now int64) int {
	kept := pool.Candidates[:0]
	available := 0
	for _, candidate := range pool.Candidates {
		if candidate.ExpiresAt <= now {
			continue
		}
		valid := false
		for _, route := range routes {
			if openAISessionPoolCandidateMatchesRoute(candidate, route) {
				// 兼容尚未保存探测模型的旧候选，按仍然有效的配置归类。
				if candidate.ProbeModel == "" {
					candidate.ProbeModel = route.model
				}
				valid = true
				break
			}
		}
		if !valid || candidate.UpstreamSessionID == "" || candidate.FirstTokenMS < 0 || candidate.FirstTokenMS >= openAIWSLatencyCandidateMaxFirstTokenMS {
			continue
		}
		kept = append(kept, candidate)
		if !candidate.Committed && (candidate.LeaseID == "" || candidate.LeaseUntil <= now) {
			available++
		}
	}
	pool.Candidates = kept
	return available
}

func openAISessionPoolIdle(candidate openAIWSLatencyReusableCandidate, now int64) bool {
	return !candidate.Committed && (candidate.LeaseID == "" || candidate.LeaseUntil <= now)
}

func openAISessionPoolModelCounts(pool *openAIWSLatencyCandidatePool, now int64) map[string]int {
	counts := make(map[string]int)
	for _, candidate := range pool.Candidates {
		if openAISessionPoolIdle(candidate, now) {
			counts[candidate.ProbeModel]++
		}
	}
	return counts
}

// 名额按勾选的模型型号均分，同型号的多个账号或协议不增加权重。
// 余数优先沿用已有会话，避免每次补充都丢弃可用结果；其余按本次随机线路顺序分配。
func openAISessionPoolModelTargets(routes []openAISessionPoolRoute, counts map[string]int, target int) map[string]int {
	models := make([]string, 0)
	quotas := make(map[string]int)
	for _, route := range routes {
		if _, exists := quotas[route.model]; !exists {
			models = append(models, route.model)
			quotas[route.model] = 0
		}
	}
	if len(models) == 0 {
		return quotas
	}
	sort.SliceStable(models, func(i, j int) bool { return counts[models[i]] > counts[models[j]] })
	for i, model := range models {
		quotas[model] = target / len(models)
		if i < target%len(models) {
			quotas[model]++
		}
	}
	return quotas
}

// 既有偏斜、租约归还或管理员调低目标时，按模型只保留最快的空闲候选。
// 正在接管或已独占的线路不占空闲配额，也不会被裁剪。
func trimOpenAISessionPool(pool *openAIWSLatencyCandidatePool, routes []openAISessionPoolRoute, now int64, target int) int {
	available := pruneOpenAISessionPool(pool, routes, now)
	counts := openAISessionPoolModelCounts(pool, now)
	quotas := openAISessionPoolModelTargets(routes, counts, target)
	excess := false
	for model, count := range counts {
		if count > quotas[model] {
			excess = true
			break
		}
	}
	if !excess {
		return available
	}
	sort.SliceStable(pool.Candidates, func(i, j int) bool { return pool.Candidates[i].FirstTokenMS < pool.Candidates[j].FirstTokenMS })
	counts = make(map[string]int)
	kept, count := pool.Candidates[:0], 0
	for _, candidate := range pool.Candidates {
		if openAISessionPoolIdle(candidate, now) {
			if counts[candidate.ProbeModel] >= quotas[candidate.ProbeModel] {
				continue
			}
			counts[candidate.ProbeModel]++
			count++
		}
		kept = append(kept, candidate)
	}
	pool.Candidates = kept
	return count
}

// 每轮重新按空闲缺口安排；候选数量相同时优先较少尝试的模型，避免失败模型阻塞其他模型。
func planOpenAISessionPoolProbes(pool *openAIWSLatencyCandidatePool, routes []openAISessionPoolRoute, now int64, target, limit int, attempts map[string]int) []openAISessionPoolRoute {
	counts := openAISessionPoolModelCounts(pool, now)
	quotas := openAISessionPoolModelTargets(routes, counts, target)
	byModel := make(map[string][]openAISessionPoolRoute)
	models := make([]string, 0)
	for _, route := range routes {
		if len(byModel[route.model]) == 0 {
			models = append(models, route.model)
		}
		byModel[route.model] = append(byModel[route.model], route)
	}
	var planned []openAISessionPoolRoute
	for len(planned) < limit {
		selected := ""
		found := false
		for _, model := range models {
			if counts[model] >= quotas[model] {
				continue
			}
			if !found || counts[model] < counts[selected] || (counts[model] == counts[selected] && attempts[model] < attempts[selected]) {
				selected, found = model, true
			}
		}
		if !found {
			break
		}
		choices := byModel[selected]
		planned = append(planned, choices[attempts[selected]%len(choices)])
		counts[selected]++
		attempts[selected]++
	}
	return planned
}

// 全局补充租约覆盖所有账号、模型和协议，避免多副本各自准备一整池。
func (s *OpenAIGatewayService) refillOpenAISessionPool(ctx context.Context) {
	if ctx.Err() != nil || !s.OpenAISessionPoolEnabled(ctx) {
		return
	}
	routes, err := s.openAISessionPoolRoutes(ctx)
	if err != nil {
		writeOpenAISessionPoolDiagnostic(ctx, logger.LevelWarn, "openai.session_pool_accounts_unavailable", "account_lookup_failed", openAISessionPoolErrorFields(err)...)
		return
	}
	target := s.settingService.GetOpenAISessionPoolSize(ctx)
	settings := s.settingService.GetOpenAIWSLatencySwitchSettings(ctx)
	refillLease := 2 * time.Minute
	for _, route := range routes {
		if timeout := openAISessionProbeTimeout(route.account) + 30*time.Second; timeout > refillLease {
			refillLease = timeout
		}
	}
	rand.Shuffle(len(routes), func(i, j int) { routes[i], routes[j] = routes[j], routes[i] })
	// 型号单独随机排序，避免多账号或多协议的型号更容易获得余数名额。
	modelOrder := make(map[string]int)
	for _, route := range routes {
		if _, exists := modelOrder[route.model]; !exists {
			modelOrder[route.model] = len(modelOrder)
		}
	}
	order := rand.Perm(len(modelOrder))
	sort.SliceStable(routes, func(i, j int) bool { return order[modelOrder[routes[i].model]] < order[modelOrder[routes[j].model]] })
	owner := uuid.NewString()
	claimed := false
	availableBefore, skipReason := 0, "cache_update_failed"
	_, updated := s.updateOpenAIWSLatencyCandidatePool(ctx, openAISessionPoolKey, func(pool *openAIWSLatencyCandidatePool) bool {
		claimed = false
		now := time.Now().UnixMilli()
		available := trimOpenAISessionPool(pool, routes, now, target)
		availableBefore = available
		switch {
		case len(routes) == 0:
			skipReason = "no_eligible_routes"
		case available >= target:
			skipReason = "pool_full"
		case pool.ProbeUntil > now:
			skipReason = "refill_in_progress"
		case pool.RetryAfter > now:
			skipReason = "retry_backoff"
		default:
			skipReason = ""
		}
		if skipReason != "" {
			return true
		}
		pool.ProbeID, pool.ProbeUntil = owner, now+refillLease.Milliseconds()
		claimed = true
		return true
	})
	if !updated || !claimed {
		if !updated {
			skipReason = "cache_update_failed"
		}
		writeOpenAISessionPoolDiagnostic(ctx, logger.LevelDebug, "openai.session_pool_refill_skipped", skipReason,
			zap.Int("target_size", target), zap.Int("available_count", availableBefore), zap.Int("route_count", len(routes)))
		return
	}
	diagnostic := openAISessionPoolRefillDiagnostic{started: time.Now(), id: owner, reason: "target_reached",
		target: target, before: availableBefore, available: availableBefore, outcomes: make(map[string]int)}
	defer diagnostic.write(ctx)
	writeOpenAISessionPoolDiagnostic(ctx, logger.LevelDebug, "openai.session_pool_refill_started", "refill_claimed",
		zap.String("refill_id", owner), zap.Int("target_size", target), zap.Int("available_count", availableBefore),
		zap.Int("route_count", len(routes)), zap.Int("probe_concurrency", settings.ProbeCount), zap.Int("probe_reuse_seconds", settings.ProbeReuseSeconds))
	defer func() {
		done, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		s.updateOpenAIWSLatencyCandidatePool(done, openAISessionPoolKey, func(pool *openAIWSLatencyCandidatePool) bool {
			if pool.ProbeID != owner {
				return false
			}
			pool.ProbeID, pool.ProbeUntil = "", 0
			return true
		})
	}()
	attempts := make(map[string]int)
	for round := 0; round < openAIWSLatencyMaxProbeRounds && ctx.Err() == nil && s.OpenAISessionPoolEnabled(ctx); round++ {
		available, owned, leaseChecked := 0, false, false
		pool, ok := s.updateOpenAIWSLatencyCandidatePool(ctx, openAISessionPoolKey, func(pool *openAIWSLatencyCandidatePool) bool {
			leaseChecked = true
			owned = pool.ProbeID == owner
			if !owned {
				return false
			}
			available = trimOpenAISessionPool(pool, routes, time.Now().UnixMilli(), target)
			pool.ProbeUntil = time.Now().Add(refillLease).UnixMilli()
			return true
		})
		if leaseChecked && owned {
			diagnostic.available = available
		}
		if !ok || !owned || available >= target {
			if leaseChecked && !owned {
				diagnostic.reason = "refill_lease_lost"
			} else if !ok {
				diagnostic.reason = "cache_update_failed"
			}
			return
		}
		diagnostic.rounds = round + 1
		planned := planOpenAISessionPoolProbes(&pool, routes, time.Now().UnixMilli(), target, settings.ProbeCount, attempts)
		count := len(planned)
		type outcome struct {
			route  openAISessionPoolRoute
			result openAIWSLatencyProbeResult
			number int
			record *openAISessionPoolProbeRecord
		}
		results := make(chan outcome, count)
		publish := func(item outcome) {
			route, result := item.route, item.result
			if !result.ok || result.ms < 0 || result.ms >= openAIWSLatencyCandidateMaxFirstTokenMS || result.upstreamSessionID == "" || result.upstreamModel != route.upstreamModel {
				return
			}
			if !s.OpenAISessionPoolEnabled(ctx) {
				diagnostic.notPublished++
				item.record.Reason = "feature_disabled"
				return
			}
			// 探测和判题期间配置可能变化，不能把旧规则下的结果当成当前合格会话。
			fresh, err := s.accountRepo.GetByID(ctx, route.account.ID)
			if err != nil || fresh == nil {
				diagnostic.notPublished++
				item.record.Reason = "account_lookup_failed"
				return
			}
			if !fresh.OpenAISessionPoolEnabled() || !fresh.IsSchedulable() || openAISessionPoolAccountFingerprint(fresh) != route.fingerprint || !openAISessionPoolModelConfigured(fresh, route.upstreamModel) {
				diagnostic.notPublished++
				item.record.Reason = "account_configuration_changed"
				return
			}
			resultID, publishReason := uuid.NewString(), "cache_update_failed"
			_, published := s.updateOpenAIWSLatencyCandidatePool(ctx, openAISessionPoolKey, func(pool *openAIWSLatencyCandidatePool) bool {
				publishReason = "cache_update_failed"
				if pool.ProbeID != owner || pool.ProbeUntil <= time.Now().UnixMilli() {
					publishReason = "refill_lease_lost"
					return false
				}
				diagnostic.available = trimOpenAISessionPool(pool, routes, time.Now().UnixMilli(), target)
				if diagnostic.available >= target {
					publishReason = "pool_full"
					return false
				}
				counts := openAISessionPoolModelCounts(pool, time.Now().UnixMilli())
				if counts[route.model] >= openAISessionPoolModelTargets(routes, counts, target)[route.model] {
					publishReason = "model_quota_full"
					return false
				}
				pool.Candidates = append(pool.Candidates, openAIWSLatencyReusableCandidate{ResultID: resultID, AccountID: route.account.ID, UpstreamSessionID: result.upstreamSessionID, UpstreamModel: result.upstreamModel, ProbeModel: route.model, FirstTokenMS: result.ms, ExpiresAt: time.Now().Add(time.Duration(settings.ProbeReuseSeconds) * time.Second).UnixMilli(), AccountFingerprint: route.fingerprint, Transport: route.transport, QualityPassed: result.qualityJudgment != nil && result.qualityJudgment.Verdict == "correct"})
				return true
			})
			if published {
				item.record.Published = true
				item.record.ResultID = resultID
				diagnostic.published++
				diagnostic.available++
				publishReason = "candidate_published"
			} else {
				diagnostic.notPublished++
				item.record.Reason = publishReason
			}
			// 原始上游会话不进入日志；候选编号关联后续领取与接管。
			sessionHash, _ := deriveOpenAISessionHashes(result.upstreamSessionID)
			writeOpenAISessionPoolDiagnostic(ctx, logger.LevelDebug, "openai.session_pool_candidate_publish", publishReason,
				zap.String("refill_id", owner), zap.Int("round", round+1), zap.String("target_result_id", resultID),
				zap.Int64("account_id", route.account.ID), zap.String("model", route.model), zap.String("transport", string(route.transport)),
				zap.String("upstream_session_hash", sessionHash), zap.Int("first_token_ms", result.ms), zap.Bool("published", published))
		}
		for i, route := range planned {
			probeCtx := logger.IntoContext(ctx, logger.FromContext(ctx).With(zap.String("refill_id", owner), zap.Int("round", round+1), zap.Int("probe_number", i+1)))
			go func(route openAISessionPoolRoute, probeCtx context.Context, number int) {
				results <- outcome{route: route, result: s.probeOpenAISessionPoolRoute(probeCtx, route), number: number}
			}(route, probeCtx, i+1)
		}
		pending := make([]outcome, 0, count)
		for i := 0; i < count; i++ {
			item := <-results
			item.record = &openAISessionPoolProbeRecord{Round: round + 1, ProbeNumber: item.number,
				AccountID: item.route.account.ID, AccountName: item.route.account.Name, Model: item.route.model,
				Transport: item.route.transport, Success: item.result.ok, Qualified: item.result.failure == "qualified", Reason: item.result.failure, QualityJudgment: item.result.qualityJudgment, QualityProbeEnabled: item.result.qualityProbeEnabled}
			if (item.result.ok || item.result.qualityJudgment != nil) && item.result.ms >= 0 {
				ms := item.result.ms
				item.record.FirstTokenMS = &ms
			}
			diagnostic.results = append(diagnostic.results, item.record)
			diagnostic.probes++
			if item.result.ok && item.result.ms < openAIWSLatencyFastCandidateMS {
				publish(item)
			} else {
				pending = append(pending, item)
			}
		}
		// 5 至 10 秒的结果等待本轮完成后按速度入池；低于 5 秒的结果已可提前领取。
		sort.SliceStable(pending, func(i, j int) bool { return pending[i].result.ms < pending[j].result.ms })
		for _, item := range pending {
			publish(item)
		}
	}
	_, saved := s.updateOpenAIWSLatencyCandidatePool(ctx, openAISessionPoolKey, func(pool *openAIWSLatencyCandidatePool) bool {
		if pool.ProbeID != owner {
			diagnostic.reason = "refill_lease_lost"
			return false
		}
		diagnostic.available = trimOpenAISessionPool(pool, routes, time.Now().UnixMilli(), target)
		if diagnostic.available < target {
			pool.RetryAfter = time.Now().Add(openAISessionPoolRetryInterval).UnixMilli()
		}
		return true
	})
	switch {
	case ctx.Err() != nil:
		diagnostic.reason = "refill_canceled"
	case !s.OpenAISessionPoolEnabled(ctx):
		diagnostic.reason = "feature_disabled"
	case !saved && diagnostic.reason != "refill_lease_lost":
		diagnostic.reason = "cache_update_failed"
	case saved && diagnostic.available < target:
		diagnostic.reason = "probe_rounds_exhausted"
	}
	if saved && diagnostic.available < target {
		diagnostic.retryAfterMS = openAISessionPoolRetryInterval.Milliseconds()
	}
}

func (s *OpenAIGatewayService) probeOpenAISessionPoolRoute(ctx context.Context, route openAISessionPoolRoute) (result openAIWSLatencyProbeResult) {
	if !s.OpenAISessionPoolEnabled(ctx) {
		return openAIWSLatencyProbeResult{failure: "feature_disabled"}
	}
	started := time.Now()
	var probeErr error
	defer func() {
		fields := []zap.Field{zap.Int64("account_id", route.account.ID), zap.String("model", route.model),
			zap.String("transport", string(route.transport)), zap.Bool("success", result.ok),
			zap.Bool("qualified", result.failure == "qualified"), zap.Bool("first_token_known", result.ok && result.ms >= 0),
			zap.Int("first_token_ms", result.ms), zap.Int("response_status", result.responseStatus), zap.Int64("duration_ms", time.Since(started).Milliseconds())}
		writeOpenAISessionPoolDiagnostic(ctx, logger.LevelDebug, "openai.session_pool_probe_completed", result.failure, append(fields, openAISessionPoolErrorFields(probeErr)...)...)
	}()
	fresh, err := s.accountRepo.GetByID(ctx, route.account.ID)
	if err != nil || fresh == nil {
		probeErr = err
		return openAIWSLatencyProbeResult{failure: "account_lookup_failed"}
	}
	// 账号带有延迟初始化的模型/请求头缓存，每路使用自己的副本。
	snapshot := *fresh
	fresh = &snapshot
	if !fresh.OpenAISessionPoolEnabled() || !fresh.IsSchedulable() || openAISessionPoolAccountFingerprint(fresh) != route.fingerprint || !openAISessionPoolModelConfigured(fresh, route.upstreamModel) {
		return openAIWSLatencyProbeResult{failure: "account_configuration_changed"}
	}
	checker := &defaultOpenAIAccountScheduler{service: s}
	if !checker.isAccountRequestCompatible(ctx, fresh, OpenAIAccountScheduleRequest{Platform: PlatformOpenAI, RequestedModel: route.model, RequiredCapability: OpenAIEndpointCapabilityResponses}) {
		return openAIWSLatencyProbeResult{failure: "account_request_incompatible"}
	}
	probeCtx, cancel := context.WithTimeout(ctx, openAISessionProbeTimeout(fresh))
	defer cancel()
	slot, err := s.tryAcquireAccountSlot(probeCtx, fresh.ID, fresh.Concurrency)
	if err != nil || slot == nil || !slot.Acquired {
		probeErr = err
		return openAIWSLatencyProbeResult{failure: "account_slot_unavailable"}
	}
	releaseSlot := func() {}
	if slot.ReleaseFunc != nil {
		releaseSlot = sync.OnceFunc(slot.ReleaseFunc)
	}
	defer releaseSlot()
	probeCtx = context.WithValue(probeCtx, openAIPreparedProbeContextKey{}, true)
	probeCtx = context.WithValue(probeCtx, openAIWSLatencyProbeSessionIDContextKey{}, "sub2api-prepared-"+uuid.NewString())
	result, probeErr = s.forwardOpenAIWSLatencyProbeWithRelease(probeCtx, fresh, route.model, route.transport, releaseSlot)
	if probeErr == nil {
		probeErr = probeCtx.Err()
	}
	result.failure = openAISessionPoolProbeReason(result, probeErr, route.upstreamModel)
	return result
}

// 按当前调用者权限和账号对请求模型的支持情况过滤，不要求请求模型与探测模型相同。
// 来源分组不参与池键，不能凭池条目绕过正常调度。
func (session *OpenAIWSLatencySession) reservePreparedCandidate(ctx context.Context, model string) (openAIWSLatencyReusableCandidate, string, bool) {
	s := session.service
	if !s.OpenAISessionPoolEnabled(ctx) {
		return openAIWSLatencyReusableCandidate{}, "", false
	}
	reason, level := "candidate_unavailable", logger.LevelInfo
	accountCount, eligibleCount, candidateCount := 0, 0, 0
	accountRejections := make(map[string]int)
	rejections := make(map[string]int)
	defer func() {
		session.poolDiagnostic(ctx, level, "openai.session_pool_reservation", reason,
			zap.Int("schedulable_account_count", accountCount), zap.Int("eligible_account_count", eligibleCount),
			zap.Any("account_rejections", accountRejections), zap.Int("candidate_count", candidateCount), zap.Any("rejections", rejections))
	}()
	accounts, err := s.listSchedulableAccounts(ctx, session.groupID, PlatformOpenAI)
	if err != nil {
		reason, level = "account_lookup_failed", logger.LevelWarn
		return openAIWSLatencyReusableCandidate{}, "", false
	}
	accountCount = len(accounts)
	if s.checkChannelPricingRestriction(ctx, session.groupID, model) {
		reason = "channel_pricing_restricted"
		return openAIWSLatencyReusableCandidate{}, "", false
	}
	mapping, _ := s.ResolveChannelMappingAndRestrict(ctx, session.groupID, model)
	requested := strings.TrimSpace(mapping.MappedModel)
	if requested == "" {
		requested = model
	}
	checker := &defaultOpenAIAccountScheduler{service: s}
	req := OpenAIAccountScheduleRequest{GroupID: session.groupID, Platform: PlatformOpenAI, RequestedModel: requested, RequiredTransport: session.requiredTransport, RequiredCapability: session.requiredCapability, RequirePrivacySet: s.openAIGroupRequiresPrivacySet(ctx, session.groupID)}
	accountRejectionReason := func(account *Account) string {
		switch {
		case !account.OpenAISessionPoolEnabled():
			return "session_pool_disabled"
		case !account.IsSchedulable():
			return "account_not_schedulable"
		case !s.openAIAccountMatchesSchedulingGroup(account, session.groupID):
			return "group_not_authorized"
		}
		if compatible, why := checker.isAccountRequestCompatibleReason(ctx, account, req); !compatible {
			return why
		}
		if !checker.isAccountTransportCompatible(account, session.requiredTransport) {
			return "transport_mismatch"
		}
		return ""
	}
	eligible := make(map[int64]*Account)
	for i := range accounts {
		account := &accounts[i]
		if why := accountRejectionReason(account); why != "" {
			accountRejections["snapshot_"+why]++
			continue
		}
		// 不在缓存 CAS 回调里查数据库，避免占用原子更新的短超时窗口。
		fresh, err := s.accountRepo.GetByID(ctx, account.ID)
		if err != nil || fresh == nil {
			accountRejections["fresh_account_lookup_failed"]++
			continue
		}
		snapshot := *fresh
		fresh = &snapshot
		if why := accountRejectionReason(fresh); why != "" {
			accountRejections["fresh_"+why]++
			continue
		}
		account = fresh
		eligible[account.ID] = account
	}
	eligibleCount = len(eligible)
	if eligibleCount == 0 {
		reason = "no_eligible_accounts"
		return openAIWSLatencyReusableCandidate{}, "", false
	}
	leaseSeconds := s.settingService.GetOpenAIWSLatencySwitchSettings(ctx).WaitSeconds
	lease := uuid.NewString()
	var selected openAIWSLatencyReusableCandidate
	_, updated := s.updateOpenAIWSLatencyCandidatePool(ctx, openAISessionPoolKey, func(pool *openAIWSLatencyCandidatePool) bool {
		selected = openAIWSLatencyReusableCandidate{}
		reason = "no_matching_candidate"
		rejections = make(map[string]int) // 原子重试只保留最后一次快照，不能重复累计。
		candidateCount = len(pool.Candidates)
		if candidateCount == 0 {
			reason = "pool_empty"
		}
		now := time.Now().UnixMilli()
		for _, candidate := range pool.Candidates {
			if candidate.ExpiresAt <= now {
				rejections["expired"]++
			}
		}
		pruneOpenAIWSLatencyCandidates(pool, now)
		sort.SliceStable(pool.Candidates, func(i, j int) bool { return pool.Candidates[i].FirstTokenMS < pool.Candidates[j].FirstTokenMS })
		for i := range pool.Candidates {
			c := &pool.Candidates[i]
			a := eligible[c.AccountID]
			rejected := ""
			// eligible 已按当前请求模型校验账号白名单；此处只校验候选本身仍然有效，
			// 不再比较探测模型与请求模型，否则会阻止同一快速会话服务其他获准模型。
			switch {
			case a == nil:
				rejected = "account_not_eligible"
			case c.Committed:
				rejected = "already_committed"
			case c.LeaseID != "" && c.LeaseUntil > now:
				rejected = "leased"
			case c.UpstreamSessionID == "":
				rejected = "upstream_session_missing"
			case c.Transport != s.openAISessionPoolTransport(a, session.requiredTransport):
				rejected = "transport_mismatch"
			case c.AccountFingerprint != openAISessionPoolAccountFingerprint(a):
				rejected = "account_changed"
			case !openAISessionPoolQualityCandidateAllowed(a, *c):
				rejected = "quality_not_passed"
			case c.FirstTokenMS < 0 || c.FirstTokenMS >= openAIWSLatencyCandidateMaxFirstTokenMS:
				rejected = "invalid_latency"
			case !openAISessionPoolModelConfigured(a, c.UpstreamModel):
				rejected = "model_not_configured"
			}
			if rejected != "" {
				rejections[rejected]++
				continue
			}
			c.LeaseID = lease
			c.LeaseUntil = now + (time.Duration(leaseSeconds) * time.Second).Milliseconds()
			selected = *c
			return true
		}
		return false
	})
	if updated && selected.AccountID > 0 {
		reason, level = "candidate_reserved", logger.LevelDebug
		s.wakeOpenAISessionPool()
		return selected, lease, true
	}
	if selected.AccountID > 0 || reason == "candidate_unavailable" {
		reason, level = "cache_update_failed", logger.LevelWarn
	}
	s.wakeOpenAISessionPool()
	return openAIWSLatencyReusableCandidate{}, "", false
}

func (session *OpenAIWSLatencySession) assignPreparedCandidate(ctx context.Context, model string, currentID int64, initial bool) bool {
	candidate, lease, ok := session.reservePreparedCandidate(ctx, model)
	if !ok {
		return false
	}
	settings := session.service.settingService.GetOpenAIWSLatencySwitchSettings(ctx)
	_, assigned := session.service.updateOpenAIWSLatencyState(ctx, session.key(model), func(state *openAIWSLatencyState) bool {
		if state.CandidateCommitted || state.PendingUntil > time.Now().UnixMilli() || (initial && (state.AccountID > 0 || state.InitialDecisionMade)) || (!initial && state.AccountID != currentID) {
			return false
		}
		if state.TargetID == 0 && state.ActiveUpstreamSessionID == "" {
			state.ActiveUpstreamSessionID = state.UpstreamSessionID
		}
		state.AccountID = currentID
		state.TargetID = candidate.AccountID
		state.TargetResultID = candidate.ResultID
		state.TargetUpstreamModel = candidate.UpstreamModel
		state.TargetPoolKey = openAISessionPoolKey
		state.TargetFingerprint = candidate.AccountFingerprint
		state.UpstreamSessionID = candidate.UpstreamSessionID
		state.CandidateLeaseID = lease
		state.CandidateCommitted = false
		state.InitialAssignment = initial
		state.InitialDecisionMade = true
		state.ReconnectFrom = ""
		state.PendingUntil = time.Now().Add(time.Duration(settings.WaitSeconds) * time.Second).UnixMilli()
		state.ProbeID = ""
		state.ProbeUntil = 0
		return true
	})
	if !assigned {
		session.service.releaseOpenAIWSLatencyCandidate(ctx, openAISessionPoolKey, lease, candidate.AccountID, false)
		session.poolDiagnostic(ctx, logger.LevelInfo, "openai.session_pool_assignment_skipped", "pending_state_not_updated",
			zap.Int64("target_account_id", candidate.AccountID), zap.String("target_result_id", candidate.ResultID), zap.Bool("initial_assignment", initial))
		return false
	}
	event := "openai.ws_latency_switch_pending"
	if initial {
		event = "openai.session_pool_assigned"
	}
	session.actor.writeEvent(event, map[string]any{"from_account_id": currentID, "target_account_id": candidate.AccountID, "target_result_id": candidate.ResultID, "model": model, "reused_probe_result": true, "probe_first_token_ms": candidate.FirstTokenMS, "candidate_remaining_ms": openAIWSLatencyRemainingMS(candidate.ExpiresAt, time.Now().UnixMilli()), "transport": string(candidate.Transport)})
	return true
}

// 在首次发送用户内容之前领取；工具回传、带旧响应引用的请求不会被误认为新对话。
func (session *OpenAIWSLatencySession) prepareInitialSession(ctx context.Context, payload []byte) (bool, error) {
	if !session.enabled(ctx) {
		return false, nil
	}
	state, err := session.preparedState(ctx)
	if err != nil {
		return false, err
	}
	confirmed := openAIWSLatencyIsUserRequest(payload)
	// 已独占发送后，等待期限不再决定绑定寿命；失败重试沿用同一会话。
	if state.CandidateCommitted && state.TargetID > 0 && state.UpstreamSessionID != "" {
		// 沿用同一上游会话时，工具结果及响应引用仍有效，无需再次恢复历史。
		// 是否计慢或确认接管成功，仍由该轮是否为用户请求独立判断。
		session.setRecovery(true, true)
		return true, nil
	}
	if state.InitialAssignment && state.TargetID > 0 && (state.PendingUntil > time.Now().UnixMilli() || state.CandidateCommitted) {
		if !state.CandidateCommitted {
			coverage := AnalyzeToolCallOutputContextCoverageBytes(payload)
			if strings.TrimSpace(gjson.GetBytes(payload, "previous_response_id").String()) != "" || (coverage.HasFunctionCallOutput && !coverage.ContextCoversAllCallIDs) {
				// 未发出过请求的备用会话不能承接其他线路的增量引用。
				session.abandonPreparedCandidate(ctx, state, "incremental_context_incompatible")
				return false, nil
			}
		}
		session.setRecovery(true, confirmed)
		return true, nil
	}
	if state.AccountID > 0 || state.TargetID > 0 || state.InitialAssignment || state.InitialDecisionMade || (!confirmed && openAIWSLatencyRequestKind(payload) != "prewarm") || strings.TrimSpace(gjson.GetBytes(payload, "previous_response_id").String()) != "" {
		return false, nil
	}
	coverage := AnalyzeToolCallOutputContextCoverageBytes(payload)
	if coverage.HasFunctionCallOutput && !coverage.ContextCoversAllCallIDs {
		return false, nil
	}
	// 已有粘性绑定的历史对话不能因缺少新功能状态而被当作首次对话迁移。
	if accountID, err := session.service.getStickySessionAccountID(ctx, session.groupID, session.sessionHash); (err != nil && !errors.Is(err, ErrStickySessionNotFound)) || accountID > 0 {
		return false, nil
	}
	if session.assignPreparedCandidate(ctx, session.model, 0, true) {
		session.setRecovery(true, confirmed)
		return true, nil
	}
	// 池空也记录首次决定，避免首次普通请求尚在执行时，后续请求中途领取。
	session.service.updateOpenAIWSLatencyState(ctx, session.key(session.model), func(current *openAIWSLatencyState) bool {
		if current.InitialDecisionMade || current.AccountID > 0 || current.TargetID > 0 {
			return false
		}
		current.InitialDecisionMade = true
		return true
	})
	// 同一对话同时到达的首次请求必须接管胜出的那份分配，不能各走一条线路。
	state, err = session.preparedState(ctx)
	if err != nil {
		return false, err
	}
	if state.InitialAssignment && state.TargetID > 0 {
		session.setRecovery(true, confirmed)
		return true, nil
	}
	if !state.InitialDecisionMade && state.AccountID == 0 && state.TargetID == 0 {
		// 首次决定未持久化，不能先发送普通线路再让后续请求领取另一个会话。
		return false, ErrOpenAIPreparedSessionUnavailable
	}
	return state.TargetID == 0 && state.ActiveUpstreamSessionID != "", nil
}

// 压缩请求只沿用已有上游会话，不领取候选，也不触发慢切换。
func (session *OpenAIWSLatencySession) PrepareExistingBinding(requireCompact bool) {
	if session != nil {
		session.existingBindingOnly = true
		session.requireCompact = requireCompact
	}
}

func openAIExistingPreparedBinding(state openAIWSLatencyState) openAIWSLatencyState {
	if state.CandidateCommitted && state.TargetID > 0 {
		state.AccountID = state.TargetID
		state.ActiveUpstreamSessionID = state.UpstreamSessionID
	} else if state.TargetID == 0 && state.ActiveUpstreamSessionID == "" {
		state.ActiveUpstreamSessionID = state.UpstreamSessionID
	}
	clearOpenAISessionPoolPending(&state)
	return state
}

func (session *OpenAIWSLatencySession) selectPreparedBoundAccount(ctx context.Context, state openAIWSLatencyState, excluded map[int64]struct{}, transport OpenAIUpstreamTransport) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
	target := state.TargetID
	if target <= 0 {
		target = state.AccountID
	}
	decision := OpenAIAccountScheduleDecision{Layer: "prepared_session"}
	if _, failed := excluded[target]; failed {
		session.abandonPreparedCandidate(ctx, state, "target_excluded")
		return nil, decision, nil
	}
	if state.TargetID > 0 && !state.CandidateCommitted && state.PendingUntil <= time.Now().UnixMilli() {
		session.abandonPreparedCandidate(ctx, state, "pending_expired")
		return nil, decision, nil
	}
	request := OpenAIAccountScheduleRequest{GroupID: session.groupID, Platform: PlatformOpenAI, SessionHash: session.sessionHash, StickyAccountID: target, RequestedModel: session.upstreamModel, RequiredTransport: transport, RequiredCapability: session.requiredCapability, RequireCompact: session.requireCompact, ExcludedIDs: excluded, DisableStickyEscape: true, PreserveStickyBinding: true, RequirePrivacySet: session.service.openAIGroupRequiresPrivacySet(ctx, session.groupID)}
	scheduler := &defaultOpenAIAccountScheduler{service: session.service}
	selection, _, err := scheduler.selectBySessionHash(ctx, request)
	if selection != nil && selection.Account != nil {
		if selection.Account.Type != AccountTypeAPIKey || (state.TargetID > 0 && !state.CandidateCommitted && !session.preparedCandidateMatchesRequest(selection.Account, state, session.upstreamModel)) {
			if selection.ReleaseFunc != nil {
				selection.ReleaseFunc()
			}
			session.abandonPreparedCandidate(ctx, state, "candidate_no_longer_eligible")
			return nil, decision, nil
		}
		decision.SelectedAccountID = selection.Account.ID
		decision.SelectedAccountType = selection.Account.Type
		decision.StickyPreviousHit = state.TargetID == 0 || state.CandidateCommitted
		_ = session.service.bindOpenAIStickySessionDuringSelection(ctx, session.groupID, session.sessionHash, selection.Account.ID)
	} else if err == nil && state.TargetID > 0 {
		session.abandonPreparedCandidate(ctx, state, "target_unavailable")
	}
	if err != nil {
		session.poolDiagnostic(ctx, logger.LevelWarn, "openai.session_pool_selection_failed", "target_selection_failed",
			append([]zap.Field{zap.Int64("target_account_id", target), zap.String("target_result_id", state.TargetResultID)}, openAISessionPoolErrorFields(err)...)...)
	}
	return selection, decision, err
}

// 探测模型只用于筛选会话；领取和发送时按账号白名单校验用户请求的模型，
// 不要求与探测模型一致，也不把用户请求改为探测模型。
// 此处是发送前的复核，必须与领取规则一致，避免已领取的跨模型候选又被误拒绝。
func (session *OpenAIWSLatencySession) preparedCandidateMatchesRequest(account *Account, state openAIWSLatencyState, requestedModel string) bool {
	if !account.OpenAISessionPoolEnabled() || !session.service.openAIAccountMatchesSchedulingGroup(account, session.groupID) || openAISessionPoolAccountFingerprint(account) != state.TargetFingerprint || !openAISessionPoolModelConfigured(account, state.TargetUpstreamModel) {
		return false
	}
	return account.IsModelSupported(requestedModel)
}

// HTTP 桥接与普通 HTTP 使用同一类探测结果；原生长连接使用实际 WS 协议结果。
func (s *OpenAIGatewayService) openAISessionPoolTransport(account *Account, ingress OpenAIUpstreamTransport) OpenAIUpstreamTransport {
	if ingress == OpenAIUpstreamTransportHTTPSSE {
		return OpenAIUpstreamTransportHTTPSSE
	}
	return s.getOpenAIWSProtocolResolver().Resolve(account).Transport
}

func openAISessionPoolModelConfigured(account *Account, upstreamModel string) bool {
	for _, model := range account.OpenAISessionPoolModels() {
		mapped := resolveOpenAIAccountUpstreamModelForRequest(account, model, false)
		if mapped == "" {
			mapped = model
		}
		if mapped == upstreamModel {
			return true
		}
	}
	return false
}

// 未发送用户内容的租约可以归还；已经独占的失败候选只能废弃，不能再次共享。
func (session *OpenAIWSLatencySession) abandonPreparedCandidate(ctx context.Context, old openAIWSLatencyState, reason string) bool {
	_, changed := session.service.updateOpenAIWSLatencyState(ctx, session.key(session.model), func(state *openAIWSLatencyState) bool {
		if old.TargetID == 0 || state.TargetResultID != old.TargetResultID || state.CandidateLeaseID != old.CandidateLeaseID || state.CandidateCommitted != old.CandidateCommitted {
			return false
		}
		clearOpenAISessionPoolPending(state)
		return true
	})
	if changed {
		session.service.releaseOpenAIWSLatencyCandidate(ctx, old.TargetPoolKey, old.CandidateLeaseID, old.TargetID, old.CandidateCommitted)
		session.setRecovery(false, false)
		session.poolDiagnostic(ctx, logger.LevelInfo, "openai.session_pool_candidate_abandoned", reason,
			zap.Int64("target_account_id", old.TargetID), zap.String("target_result_id", old.TargetResultID),
			zap.Bool("candidate_committed", old.CandidateCommitted), zap.Bool("initial_assignment", old.InitialAssignment))
		if old.CandidateCommitted {
			session.actor.writeEvent("openai.session_pool_candidate_discarded", map[string]any{"target_account_id": old.TargetID, "target_result_id": old.TargetResultID, "model": session.model, "reason": reason})
		}
		session.service.wakeOpenAISessionPool()
	}
	return changed
}

func clearOpenAISessionPoolPending(state *openAIWSLatencyState) {
	state.TargetID, state.TargetResultID, state.TargetUpstreamModel, state.TargetPoolKey, state.TargetFingerprint = 0, "", "", "", ""
	state.PendingUntil, state.ReconnectFrom = 0, ""
	state.CandidateLeaseID, state.CandidateCommitted, state.InitialAssignment = "", false, false
	state.UpstreamSessionID = state.ActiveUpstreamSessionID
}

package service

import (
	"context"
	"errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"time"
)

// 默认关闭备用池时，保留原有按需探测与结果复用策略。
type openAIWSLatencyProbeLogResult struct {
	ProbeNumber         int     `json:"probe_number"`
	RequestedAccountID  int64   `json:"requested_account_id"`
	AccountID           int64   `json:"account_id,omitempty"`
	AttemptedAccountIDs []int64 `json:"attempted_account_ids,omitempty"`
	FirstTokenMS        *int    `json:"first_token_ms,omitempty"`
	Success             bool    `json:"success"`
	TimedOut            bool    `json:"timed_out"`
	Failure             string  `json:"failure,omitempty"`
}

type openAIWSLatencyProbeLimiter struct {
	mu     sync.Mutex
	active int
}

func openAIWSLatencyProbeWindow(probeCount int) time.Duration {
	if probeCount < 1 {
		probeCount = 1
	}
	return time.Duration(openAIWSLatencyMaxProbeRounds)*(time.Duration(probeCount)*openAIWSLatencyProbeTimeout+time.Second) + 5*time.Second
}

func (limiter *openAIWSLatencyProbeLimiter) tryAcquire(limit int) bool {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	if limiter.active >= limit {
		return false
	}
	limiter.active++
	return true
}

func (limiter *openAIWSLatencyProbeLimiter) acquire(ctx context.Context, limit int) bool {
	for {
		if limiter.tryAcquire(limit) {
			return true
		}
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return false
		case <-timer.C:
		}
	}
}

func (limiter *openAIWSLatencyProbeLimiter) release() {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	limiter.active--
}

func chooseOpenAIWSLatencyTarget(results []openAIWSLatencyProbeResult, _ int64) int64 {
	bestID, bestMS := int64(0), openAIWSLatencyCandidateMaxFirstTokenMS
	for _, result := range results {
		if result.ok && result.accountID != 0 && result.ms >= 0 && result.ms < bestMS {
			bestID, bestMS = result.accountID, result.ms
		}
	}
	return bestID
}

func isOpenAIWSLatencyFastCandidate(result openAIWSLatencyProbeResult, _ int64) bool {
	return result.ok && result.accountID != 0 && result.ms >= 0 && result.ms < openAIWSLatencyFastCandidateMS
}

func writeOpenAIWSLatencyProbeCompleted(actor openAIWSLatencyActor, currentID int64, probeCount, round, notWaitedProbes int, results []openAIWSLatencyProbeResult, targetID int64, targetResultID, model string, transport OpenAIUpstreamTransport) {
	probeLogResults := make([]openAIWSLatencyProbeLogResult, 0, len(results))
	forwardedAttempts := 0
	for _, result := range results {
		probeLog := openAIWSLatencyProbeLogResult{
			ProbeNumber:         result.probeNumber,
			RequestedAccountID:  result.requestedAccountID,
			AccountID:           result.accountID,
			AttemptedAccountIDs: result.attemptedAccountIDs,
			Success:             result.ok,
			TimedOut:            result.timedOut,
			Failure:             result.failure,
		}
		forwardedAttempts += len(result.attemptedAccountIDs)
		if result.ok || result.timedOut {
			firstTokenMS := result.ms
			probeLog.FirstTokenMS = &firstTokenMS
		}
		probeLogResults = append(probeLogResults, probeLog)
	}
	sort.Slice(probeLogResults, func(i, j int) bool { return probeLogResults[i].ProbeNumber < probeLogResults[j].ProbeNumber })
	actor.writeEvent("openai.ws_latency_switch_probe_completed", map[string]any{
		"from_account_id":    currentID,
		"probe_limit":        probeCount,
		"probe_round":        round,
		"max_probe_rounds":   openAIWSLatencyMaxProbeRounds,
		"probes":             len(results),
		"not_waited_probes":  notWaitedProbes,
		"forwarded_attempts": forwardedAttempts,
		"probe_results":      probeLogResults,
		"target_account_id":  targetID,
		"target_result_id":   targetResultID,
		"model":              model,
		"transport":          string(transport),
	})
}

func (s *OpenAIGatewayService) probeOpenAIWSLatencyRoutes(ctx context.Context, key string, groupID *int64, currentID int64, poolModel, model string, capability OpenAIEndpointCapability, requiredTransport OpenAIUpstreamTransport, probeID string, actor openAIWSLatencyActor) {
	settings := s.settingService.GetOpenAIWSLatencySwitchSettings(ctx)
	probeCount := settings.ProbeCount
	if probeCount < 1 {
		probeCount = 1
	}
	leaseID := uuid.NewString()
	ctx, cancel := context.WithTimeout(ctx, openAIWSLatencyProbeWindow(probeCount)-5*time.Second)
	defer cancel()
	if requiredTransport == "" {
		requiredTransport = OpenAIUpstreamTransportResponsesWebsocketV2Ingress
	}
	poolModel = strings.TrimSpace(poolModel)
	if poolModel == "" {
		poolModel = model
	}
	poolKey := openAIWSLatencyCandidatePoolKey(groupID, poolModel, requiredTransport)
	diagnostic := func(reason string, fields ...zap.Field) {
		base := append(actor.diagnosticFields(), zap.String("stage", "probe"), zap.String("model", model),
			zap.String("transport", string(requiredTransport)), zap.Int64("account_id", currentID), zap.String("probe_id", probeID),
			zap.String("pool_key_hash", hashOpenAIWSLatencyModel(poolKey)), zap.String("pool_model", poolModel),
			zap.Int("switch_wait_seconds", settings.WaitSeconds), zap.Int("probe_reuse_seconds", settings.ProbeReuseSeconds))
		writeOpenAIWSLatencyDiagnostic(ctx, logger.LevelInfo, reason, append(base, fields...)...)
	}
	var targetID int64
	var candidateLeaseID string
	var targetResultID string
	releaseProbeOnReturn := true
	defer func() {
		updateCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer stop()
		if targetID == 0 {
			s.updateOpenAIWSLatencyState(updateCtx, key, func(state *openAIWSLatencyState) bool {
				if state.ProbeID != probeID || state.AccountID != currentID {
					return false
				}
				state.ProbeID, state.ProbeUntil = "", 0
				return true
			})
		}
		if releaseProbeOnReturn {
			s.releaseOpenAIWSLatencyProbe(updateCtx, poolKey, probeID)
		}
	}()
	accounts, err := s.listSchedulableAccounts(ctx, groupID, PlatformOpenAI)
	if err != nil {
		diagnostic("probe_account_lookup_failed")
		return
	}
	if s.checkChannelPricingRestriction(ctx, groupID, model) {
		diagnostic("probe_channel_pricing_restricted")
		return
	}
	request := OpenAIAccountScheduleRequest{GroupID: groupID, Platform: PlatformOpenAI, RequestedModel: model, RequiredTransport: requiredTransport, RequiredCapability: capability, RequirePrivacySet: s.openAIGroupRequiresPrivacySet(ctx, groupID)}
	checker := &defaultOpenAIAccountScheduler{service: s}
	eligibleAccounts := make([]*Account, 0, len(accounts))
	eligibleAccountIDs := make(map[int64]struct{}, len(accounts))
	expectedUpstreamModels := make(map[int64]string, len(accounts))
	currentFound := false
	for i := range accounts {
		account := &accounts[i]
		if !account.IsSchedulable() || account.Platform != PlatformOpenAI || !s.openAIAccountMatchesSchedulingGroup(account, groupID) || !checker.isAccountRequestCompatible(ctx, account, request) || !checker.isAccountTransportCompatible(account, request.RequiredTransport) {
			continue
		}
		eligibleAccounts = append(eligibleAccounts, account)
		eligibleAccountIDs[account.ID] = struct{}{}
		expectedModel := resolveOpenAIAccountUpstreamModelForRequest(account, model, false)
		if expectedModel == "" {
			expectedModel = model
		}
		expectedUpstreamModels[account.ID] = expectedModel
		currentFound = currentFound || account.ID == currentID
	}
	if !currentFound || len(eligibleAccounts) == 0 {
		diagnostic("probe_no_eligible_accounts", zap.Int("account_count", len(accounts)), zap.Int("eligible_account_count", len(eligibleAccounts)), zap.Bool("current_account_eligible", currentFound))
		return
	}
	pendingRecorded := false
	setPending := func(candidate openAIWSLatencyReusableCandidate, leaseID string) bool {
		updateCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer stop()
		state, changed := s.updateOpenAIWSLatencyState(updateCtx, key, func(state *openAIWSLatencyState) bool {
			if state.ProbeID != probeID || state.AccountID != currentID || candidate.AccountID <= 0 || candidate.UpstreamSessionID == "" {
				return false
			}
			state.ProbeID, state.ProbeUntil = "", 0
			state.TargetID = candidate.AccountID
			state.TargetResultID = candidate.ResultID
			state.TargetUpstreamModel = candidate.UpstreamModel
			state.TargetPoolKey = poolKey
			state.UpstreamSessionID = candidate.UpstreamSessionID
			state.PendingUntil = time.Now().Add(time.Duration(settings.WaitSeconds) * time.Second).UnixMilli()
			state.CandidateLeaseID = leaseID
			state.CandidateCommitted = false
			return true
		})
		if changed {
			targetID = candidate.AccountID
			targetResultID = candidate.ResultID
			candidateLeaseID = leaseID
			return state.TargetID == candidate.AccountID && state.TargetResultID == candidate.ResultID
		}
		diagnostic("pending_state_not_updated", zap.Int64("target_account_id", candidate.AccountID))
		return false
	}
	writePending := func(reused bool) {
		fields := map[string]any{
			"from_account_id":   currentID,
			"target_account_id": targetID,
			"target_result_id":  targetResultID,
			"model":             model,
			"transport":         string(requiredTransport),
		}
		if reused {
			fields["reused_probe_result"] = true
		}
		diagnostic("candidate_reserved", zap.Bool("reused_probe_result", reused), zap.Int64("target_account_id", targetID), zap.String("target_result_id", targetResultID))
		actor.writeEvent("openai.ws_latency_switch_pending", fields)
		pendingRecorded = true
	}
	reusedProbeResult := false
	stopForStateChange := false
	reserveReusableCandidate := func() bool {
		candidate, ok := s.reserveOpenAIWSLatencyCandidate(ctx, poolKey, currentID, leaseID, settings.WaitSeconds, eligibleAccountIDs, expectedUpstreamModels)
		if !ok {
			return false
		}
		if !setPending(candidate, leaseID) {
			s.releaseOpenAIWSLatencyCandidate(context.WithoutCancel(ctx), poolKey, leaseID, candidate.AccountID, false)
			stopForStateChange = true
			return false
		}
		targetID = candidate.AccountID
		targetResultID = candidate.ResultID
		candidateLeaseID = leaseID
		reusedProbeResult = true
		return true
	}
	defer func() {
		updateCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer stop()
		if !pendingRecorded && targetID > 0 && s.settingService.GetOpenAIWSLatencySwitchSettings(updateCtx).Enabled {
			writePending(reusedProbeResult)
		}
	}()
	if !settings.Enabled {
		diagnostic("probe_feature_disabled")
		return
	}
	if reserveReusableCandidate() {
		writePending(true)
		return
	}
	if stopForStateChange {
		return
	}
	claimed := false
	waitLogged := false
	var waitStarted time.Time
	var lastPool openAIWSLatencyCandidatePool
	var snapshotAt int64
	waitEnded := false
	// 复用现有开始/结束事件，只在离开等待时汇总最后快照，不逐次轮询输出。
	finishWait := func() {
		if !waitLogged || waitEnded {
			return
		}
		waitEnded = true
		outcome := "probe_not_claimed"
		switch {
		case targetID > 0:
			outcome = "candidate_reserved"
		case stopForStateChange:
			outcome = "state_changed"
		case claimed:
			outcome = "new_probe_started"
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			outcome = "deadline_exceeded"
		case ctx.Err() != nil:
			outcome = "canceled"
		}
		fields := openAIWSLatencyPoolDiagnosticFields(lastPool, eligibleAccountIDs, expectedUpstreamModels, snapshotAt)
		fields = append(fields, zap.String("wait_outcome", outcome), zap.Int64("wait_duration_ms", time.Since(waitStarted).Milliseconds()),
			zap.Int64("pool_snapshot_age_ms", time.Now().UnixMilli()-snapshotAt),
			zap.Bool("deadline_exceeded", errors.Is(ctx.Err(), context.DeadlineExceeded)), zap.Bool("canceled", errors.Is(ctx.Err(), context.Canceled)))
		diagnostic("shared_pool_wait_ended", fields...)
	}
	defer finishWait()
	for !claimed && ctx.Err() == nil {
		pool := s.getOpenAIWSLatencyCandidatePool(ctx, poolKey)
		now := time.Now().UnixMilli()
		lastPool, snapshotAt = pool, now
		validCandidates := false
		for _, candidate := range pool.Candidates {
			if candidate.ExpiresAt > now && openAIWSLatencyCandidateAllowed(candidate, eligibleAccountIDs, expectedUpstreamModels) {
				validCandidates = true
				break
			}
		}
		if !validCandidates && pool.ProbeUntil <= now {
			claimed = s.claimOpenAIWSLatencyProbe(ctx, poolKey, currentID, probeID, openAIWSLatencyProbeWindow(probeCount), eligibleAccountIDs, expectedUpstreamModels)
			if claimed {
				break
			}
		}
		if validCandidates && reserveReusableCandidate() {
			writePending(true)
			return
		}
		if stopForStateChange {
			return
		}
		if !waitLogged {
			diagnostic("shared_pool_wait", openAIWSLatencyPoolDiagnosticFields(pool, eligibleAccountIDs, expectedUpstreamModels, now)...)
			waitStarted = time.Now()
			waitLogged = true
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
	if !claimed {
		diagnostic("probe_not_claimed")
		return
	}
	finishWait()
	diagnostic("new_probe_started", zap.Int("probe_count", probeCount), zap.Int("eligible_account_count", len(eligibleAccounts)))
	for round := 1; round <= openAIWSLatencyMaxProbeRounds; round++ {
		if len(eligibleAccounts) == 0 {
			break
		}
		rand.Shuffle(len(eligibleAccounts), func(i, j int) { eligibleAccounts[i], eligibleAccounts[j] = eligibleAccounts[j], eligibleAccounts[i] })
		candidates := eligibleAccounts
		if len(candidates) > probeCount {
			candidates = candidates[:probeCount]
		}
		roundCtx, cancelRound := context.WithCancel(ctx)
		results := make(chan openAIWSLatencyProbeResult, probeCount)
		var workers sync.WaitGroup
		for i := 0; i < probeCount; i++ {
			startIndex := i % len(candidates)
			probeNumber := i + 1
			probeSessionID := "sub2api-latency-" + uuid.NewString()
			workers.Add(1)
			go func(probeNumber int, startIndex int, probeSessionID string) {
				defer workers.Done()
				probeCtx := context.WithValue(roundCtx, openAIWSLatencyProbeSessionIDContextKey{}, probeSessionID)
				if !s.openaiWSLatencyProbeLimiter.acquire(probeCtx, probeCount) {
					results <- openAIWSLatencyProbeResult{
						probeNumber:        probeNumber,
						requestedAccountID: candidates[startIndex].ID,
						failure:            "probe_capacity_wait_timeout",
					}
					return
				}
				defer s.openaiWSLatencyProbeLimiter.release()
				result := s.probeOpenAIWSLatencyCandidates(probeCtx, candidates, startIndex, model, request)
				result.probeNumber = probeNumber
				if result.upstreamSessionID != "" && result.resultID == "" {
					result.resultID = uuid.NewString()
				}
				results <- result
			}(probeNumber, startIndex, probeSessionID)
		}
		observed := make([]openAIWSLatencyProbeResult, 0, probeCount)
		var selected *openAIWSLatencyProbeResult
		for len(observed) < probeCount && !stopForStateChange {
			result := <-results
			observed = append(observed, result)
			if selected == nil && isOpenAIWSLatencyFastCandidate(result, currentID) {
				candidateLeaseID = uuid.NewString()
				candidate, reserved := s.addOpenAIWSLatencyCandidates(ctx, poolKey, []openAIWSLatencyProbeResult{result}, settings.ProbeReuseSeconds, result.accountID, candidateLeaseID, settings.WaitSeconds, result.resultID)
				if reserved && setPending(candidate, candidateLeaseID) {
					selected = &observed[len(observed)-1]
					break
				} else {
					if reserved {
						s.releaseOpenAIWSLatencyCandidate(context.WithoutCancel(ctx), poolKey, candidateLeaseID, candidate.AccountID, false)
						stopForStateChange = true
					}
					candidateLeaseID = ""
				}
			}
		}
		if selected != nil {
			selectedResult := *selected
			targetID, targetResultID = selectedResult.accountID, selectedResult.resultID
			others := make([]openAIWSLatencyProbeResult, 0, len(observed)-1)
			for _, result := range observed {
				if result.resultID != selectedResult.resultID {
					others = append(others, result)
				}
			}
			_, _ = s.addOpenAIWSLatencyCandidates(ctx, poolKey, others, settings.ProbeReuseSeconds, 0, "", settings.WaitSeconds)
			notWaitedProbes := probeCount - len(observed)
			writePending(reusedProbeResult)
			cancelRound()
			releaseProbeOnReturn = false
			observedResults := append([]openAIWSLatencyProbeResult(nil), observed...)
			go func(selectedResult openAIWSLatencyProbeResult, observedResults []openAIWSLatencyProbeResult) {
				workers.Wait()
				close(results)
				completedResults := append([]openAIWSLatencyProbeResult(nil), observedResults...)
				for result := range results {
					completedResults = append(completedResults, result)
				}
				lateResults := make([]openAIWSLatencyProbeResult, 0, len(completedResults))
				for _, result := range completedResults {
					if result.resultID != selectedResult.resultID {
						lateResults = append(lateResults, result)
					}
				}
				collectorCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
				defer stop()
				_, _ = s.addOpenAIWSLatencyCandidates(collectorCtx, poolKey, lateResults, settings.ProbeReuseSeconds, 0, "", settings.WaitSeconds)
				writeOpenAIWSLatencyProbeCompleted(actor, currentID, probeCount, round, notWaitedProbes, completedResults, targetID, targetResultID, model, requiredTransport)
				s.releaseOpenAIWSLatencyProbe(collectorCtx, poolKey, probeID)
			}(*selected, observedResults)
			return
		}
		cancelRound()
		workers.Wait()
		close(results)
		for result := range results {
			observed = append(observed, result)
		}
		if stopForStateChange {
			_, _ = s.addOpenAIWSLatencyCandidates(ctx, poolKey, observed, settings.ProbeReuseSeconds, 0, "", settings.WaitSeconds)
			writeOpenAIWSLatencyProbeCompleted(actor, currentID, probeCount, round, 0, observed, 0, "", model, requiredTransport)
			return
		}
		best := chooseOpenAIWSLatencyTargetResult(observed)
		if best != nil {
			candidateLeaseID = uuid.NewString()
			candidate, reserved := s.addOpenAIWSLatencyCandidates(ctx, poolKey, observed, settings.ProbeReuseSeconds, best.accountID, candidateLeaseID, settings.WaitSeconds, best.resultID)
			if reserved && setPending(candidate, candidateLeaseID) {
				selected = best
			} else {
				if reserved {
					s.releaseOpenAIWSLatencyCandidate(context.WithoutCancel(ctx), poolKey, candidateLeaseID, candidate.AccountID, false)
					stopForStateChange = true
				}
				candidateLeaseID = ""
			}
		} else {
			_, _ = s.addOpenAIWSLatencyCandidates(ctx, poolKey, observed, settings.ProbeReuseSeconds, 0, "", settings.WaitSeconds)
		}
		if selected != nil {
			targetID, targetResultID = selected.accountID, selected.resultID
		}
		writeOpenAIWSLatencyProbeCompleted(actor, currentID, probeCount, round, 0, observed, targetID, targetResultID, model, requiredTransport)
		if targetID > 0 {
			writePending(reusedProbeResult)
			return
		}
		if ctx.Err() != nil || stopForStateChange {
			diagnostic("probe_interrupted", zap.Bool("context_done", ctx.Err() != nil), zap.Bool("state_changed", stopForStateChange))
			return
		}
	}
	diagnostic("probe_rounds_exhausted", zap.Int("max_probe_rounds", openAIWSLatencyMaxProbeRounds))
}

func chooseOpenAIWSLatencyTargetResult(results []openAIWSLatencyProbeResult) *openAIWSLatencyProbeResult {
	var best *openAIWSLatencyProbeResult
	for i := range results {
		result := &results[i]
		if !result.ok || result.accountID <= 0 || result.upstreamSessionID == "" || result.ms < 0 || result.ms >= openAIWSLatencyCandidateMaxFirstTokenMS {
			continue
		}
		if best == nil || result.ms < best.ms {
			best = result
		}
	}
	return best
}

func (s *OpenAIGatewayService) probeOpenAIWSLatencyAccount(ctx context.Context, account *Account, model string) openAIWSLatencyProbeResult {
	request := OpenAIAccountScheduleRequest{
		Platform:           PlatformOpenAI,
		RequestedModel:     model,
		RequiredTransport:  OpenAIUpstreamTransportResponsesWebsocketV2Ingress,
		RequiredCapability: OpenAIEndpointCapabilityResponses,
	}
	return s.probeOpenAIWSLatencyCandidates(ctx, []*Account{account}, 0, model, request)
}

func (s *OpenAIGatewayService) probeOpenAIWSLatencyCandidates(
	ctx context.Context,
	candidates []*Account,
	startIndex int,
	model string,
	request OpenAIAccountScheduleRequest,
) openAIWSLatencyProbeResult {
	if len(candidates) == 0 {
		return openAIWSLatencyProbeResult{failure: "no_candidates"}
	}
	startIndex = (startIndex%len(candidates) + len(candidates)) % len(candidates)
	observed := openAIWSLatencyProbeResult{
		requestedAccountID: candidates[startIndex].ID,
		upstreamSessionID:  openAIWSLatencyProbeSessionID(ctx),
		failure:            "no_eligible_account",
	}
	checker := &defaultOpenAIAccountScheduler{service: s}
	attempted := make(map[int64]struct{}, len(candidates))
	unusable := make(map[int64]struct{}, len(candidates))
	maxAccountSwitches := 3
	if s.cfg != nil && s.cfg.Gateway.MaxAccountSwitches > 0 {
		maxAccountSwitches = s.cfg.Gateway.MaxAccountSwitches
	}
	maxAttempts := min(len(candidates), maxAccountSwitches+1)
	for offset := 0; offset < len(candidates); offset++ {
		account := candidates[(startIndex+offset)%len(candidates)]
		if account == nil {
			continue
		}
		if _, done := attempted[account.ID]; done {
			continue
		}
		if _, done := unusable[account.ID]; done {
			continue
		}

		fresh := s.resolveFreshSchedulableOpenAIAccount(ctx, account, request.Platform, model, false, request.RequiredCapability)
		if fresh == nil || !s.openAIAccountMatchesSchedulingGroup(fresh, request.GroupID) ||
			!checker.isAccountRequestCompatible(ctx, fresh, request) || !checker.isAccountTransportCompatible(fresh, request.RequiredTransport) {
			unusable[account.ID] = struct{}{}
			continue
		}
		fresh = s.recheckSelectedOpenAIAccountFromDB(ctx, fresh, request.GroupID, request.Platform, model, false, request.RequiredCapability)
		if fresh == nil || !checker.isAccountRequestCompatible(ctx, fresh, request) || !checker.isAccountTransportCompatible(fresh, request.RequiredTransport) {
			unusable[account.ID] = struct{}{}
			continue
		}

		attempted[account.ID] = struct{}{}
		observed.attemptedAccountIDs = append(observed.attemptedAccountIDs, account.ID)
		observed.accountID = account.ID
		attemptResult, forwardErr := s.forwardOpenAIWSLatencyProbe(ctx, fresh, model, request.RequiredTransport)
		observed.ms, observed.ok, observed.timedOut = attemptResult.ms, attemptResult.ok, attemptResult.timedOut
		observed.upstreamSessionID = attemptResult.upstreamSessionID
		observed.upstreamModel = attemptResult.upstreamModel
		if attemptResult.ok {
			observed.failure = ""
			return observed
		}
		if attemptResult.timedOut {
			observed.failure = "upstream_timeout"
			return observed
		}
		var failoverErr *UpstreamFailoverError
		if errors.As(forwardErr, &failoverErr) && failoverErr.ShouldRetryNextAccount() {
			if len(attempted) >= maxAttempts {
				observed.failure = "upstream_failover_limit"
				return observed
			}
			observed.failure = "upstream_failover"
			continue
		}
		observed.failure = "upstream_error"
		return observed
	}
	if len(attempted) == 0 {
		observed.failure = "no_eligible_account"
	} else if len(unusable)+len(attempted) == len(candidates) {
		observed.failure = "upstream_failover_exhausted"
	}
	return observed
}

func (s *OpenAIGatewayService) claimOpenAIWSLatencyProbe(ctx context.Context, poolKey string, _ int64, probeID string, probeWindow time.Duration, eligible map[int64]struct{}, expectedUpstreamModels map[int64]string) bool {
	claimed := false
	_, updated := s.updateOpenAIWSLatencyCandidatePool(ctx, poolKey, func(pool *openAIWSLatencyCandidatePool) bool {
		now := time.Now().UnixMilli()
		pruneOpenAIWSLatencyCandidates(pool, now)
		if pool.ProbeUntil > now {
			return false
		}
		for _, candidate := range pool.Candidates {
			if candidate.ExpiresAt > now && openAIWSLatencyCandidateAllowed(candidate, eligible, expectedUpstreamModels) {
				return false
			}
		}
		pool.ProbeID = probeID
		pool.ProbeUntil = now + probeWindow.Milliseconds()
		claimed = true
		return true
	})
	return claimed && updated
}

func (s *OpenAIGatewayService) releaseOpenAIWSLatencyProbe(ctx context.Context, poolKey, probeID string) {
	_, _ = s.updateOpenAIWSLatencyCandidatePool(ctx, poolKey, func(pool *openAIWSLatencyCandidatePool) bool {
		if pool.ProbeID != probeID {
			return false
		}
		pool.ProbeID, pool.ProbeUntil = "", 0
		return true
	})
}

func (s *OpenAIGatewayService) reserveOpenAIWSLatencyCandidate(ctx context.Context, poolKey string, _ int64, leaseID string, leaseSeconds int, eligible map[int64]struct{}, expectedUpstreamModels map[int64]string) (openAIWSLatencyReusableCandidate, bool) {
	var selected openAIWSLatencyReusableCandidate
	_, updated := s.updateOpenAIWSLatencyCandidatePool(ctx, poolKey, func(pool *openAIWSLatencyCandidatePool) bool {
		selected = openAIWSLatencyReusableCandidate{}
		now := time.Now().UnixMilli()
		pruneOpenAIWSLatencyCandidates(pool, now)
		selectedIndex := -1
		for i, candidate := range pool.Candidates {
			if candidate.FirstTokenMS < 0 || candidate.FirstTokenMS >= openAIWSLatencyCandidateMaxFirstTokenMS || candidate.Committed || (candidate.LeaseID != "" && candidate.LeaseUntil > now) || !openAIWSLatencyCandidateAllowed(candidate, eligible, expectedUpstreamModels) {
				continue
			}
			if selectedIndex < 0 || candidate.FirstTokenMS < pool.Candidates[selectedIndex].FirstTokenMS {
				selectedIndex = i
			}
		}
		if selectedIndex < 0 {
			return len(pool.Candidates) > 0
		}
		selected = pool.Candidates[selectedIndex]
		selected.LeaseID = leaseID
		selected.LeaseUntil = now + (time.Duration(leaseSeconds) * time.Second).Milliseconds()
		pool.Candidates[selectedIndex] = selected
		return true
	})
	return selected, updated && selected.AccountID > 0
}

func (s *OpenAIGatewayService) addOpenAIWSLatencyCandidates(ctx context.Context, poolKey string, results []openAIWSLatencyProbeResult, reuseSeconds int, leaseAccountID int64, leaseID string, leaseSeconds int, preferredResultID ...string) (openAIWSLatencyReusableCandidate, bool) {
	var leased openAIWSLatencyReusableCandidate
	preferred := ""
	if len(preferredResultID) > 0 {
		preferred = preferredResultID[0]
	}
	_, updated := s.updateOpenAIWSLatencyCandidatePool(ctx, poolKey, func(pool *openAIWSLatencyCandidatePool) bool {
		now := time.Now().UnixMilli()
		pruneOpenAIWSLatencyCandidates(pool, now)
		for _, result := range results {
			if !result.ok || result.accountID <= 0 || result.ms < 0 || result.ms >= openAIWSLatencyCandidateMaxFirstTokenMS || result.upstreamSessionID == "" || strings.TrimSpace(result.upstreamModel) == "" {
				continue
			}
			resultID := result.resultID
			if resultID == "" {
				resultID = uuid.NewString()
			}
			candidate := openAIWSLatencyReusableCandidate{ResultID: resultID, AccountID: result.accountID, UpstreamModel: result.upstreamModel, UpstreamSessionID: result.upstreamSessionID, FirstTokenMS: result.ms, ExpiresAt: now + (time.Duration(reuseSeconds) * time.Second).Milliseconds()}
			foundIndex := -1
			for i := range pool.Candidates {
				if pool.Candidates[i].ResultID != candidate.ResultID {
					continue
				}
				foundIndex = i
				break
			}
			if foundIndex < 0 {
				pool.Candidates = append(pool.Candidates, candidate)
				continue
			}
			existing := pool.Candidates[foundIndex]
			if existing.Committed || (existing.LeaseID != "" && existing.LeaseUntil > now) {
				continue
			}
			pool.Candidates[foundIndex] = candidate
		}
		for i := range pool.Candidates {
			if pool.Candidates[i].AccountID != leaseAccountID || leaseAccountID <= 0 || (pool.Candidates[i].LeaseID != "" && pool.Candidates[i].LeaseUntil > now) || pool.Candidates[i].Committed {
				continue
			}
			if preferred != "" && pool.Candidates[i].ResultID != preferred {
				continue
			}
			pool.Candidates[i].LeaseID = leaseID
			pool.Candidates[i].LeaseUntil = now + (time.Duration(leaseSeconds) * time.Second).Milliseconds()
			leased = pool.Candidates[i]
			break
		}
		return true
	})
	return leased, updated && leased.AccountID > 0
}

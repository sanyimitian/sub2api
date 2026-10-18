package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

type openAIWSLatencyProbeResult struct {
	probeNumber         int
	requestedAccountID  int64
	accountID           int64
	attemptedAccountIDs []int64
	failure             string
	ms                  int
	ok                  bool
	timedOut            bool
}

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

// The limit is read for each batch, so settings changes apply without rebuilding
// the service or replacing a semaphore that still has active probes.
type openAIWSLatencyProbeLimiter struct {
	mu     sync.Mutex
	active int
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

func chooseOpenAIWSLatencyTarget(results []openAIWSLatencyProbeResult, currentID int64) int64 {
	measurements := make(map[int64][]int)
	for _, result := range results {
		if result.ok || (result.accountID == currentID && result.timedOut) {
			measurements[result.accountID] = append(measurements[result.accountID], result.ms)
		}
	}
	baseline, ok := medianOpenAIWSLatencyMS(measurements[currentID])
	if !ok {
		return 0
	}
	accountIDs := make([]int64, 0, len(measurements))
	for accountID := range measurements {
		if accountID != currentID {
			accountIDs = append(accountIDs, accountID)
		}
	}
	sort.Slice(accountIDs, func(i, j int) bool { return accountIDs[i] < accountIDs[j] })
	bestID, bestMS := int64(0), baseline
	for _, accountID := range accountIDs {
		ms, ok := medianOpenAIWSLatencyMS(measurements[accountID])
		// Require both a 20% and 250ms improvement to avoid switching on jitter.
		if ok && ms >= 0 && ms+250 < baseline && int64(ms)*5 < int64(baseline)*4 && ms < bestMS {
			bestID, bestMS = accountID, ms
		}
	}
	return bestID
}

func medianOpenAIWSLatencyMS(samples []int) (int, bool) {
	if len(samples) == 0 {
		return 0, false
	}
	sort.Ints(samples)
	middle := len(samples) / 2
	if len(samples)%2 == 1 {
		return samples[middle], true
	}
	return (samples[middle-1] + samples[middle]) / 2, true
}

func (s *OpenAIGatewayService) probeOpenAIWSLatencyRoutes(ctx context.Context, key string, groupID *int64, currentID int64, model string, capability OpenAIEndpointCapability, requiredTransport OpenAIUpstreamTransport, probeID string, actor openAIWSLatencyActor) {
	settings := s.settingService.GetOpenAIWSLatencySwitchSettings(ctx)
	probeCount := settings.ProbeCount
	if probeCount < 1 {
		probeCount = 1
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(probeCount)*openAIWSLatencyProbeTimeout+time.Second)
	defer cancel()
	var targetID int64
	defer func() {
		if !s.settingService.GetOpenAIWSLatencySwitchSettings(ctx).Enabled {
			targetID = 0
		}
		updateCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer stop()
		state, changed := s.updateOpenAIWSLatencyState(updateCtx, key, func(state *openAIWSLatencyState) bool {
			if state.ProbeID != probeID || state.AccountID != currentID {
				return false
			}
			state.ProbeID, state.ProbeUntil = "", 0
			if targetID > 0 {
				state.TargetID, state.PendingUntil = targetID, time.Now().Add(openAIWSLatencyPendingTTL).UnixMilli()
			}
			return true
		})
		if changed && state.TargetID > 0 {
			actor.writeEvent("openai.ws_latency_switch_pending", map[string]any{
				"from_account_id":   currentID,
				"target_account_id": targetID,
				"model":             model,
				"transport":         string(requiredTransport),
			})
		}
	}()
	if !settings.Enabled {
		return
	}
	accounts, err := s.listSchedulableAccounts(ctx, groupID, PlatformOpenAI)
	if err != nil || s.checkChannelPricingRestriction(ctx, groupID, model) {
		return
	}
	checker := &defaultOpenAIAccountScheduler{service: s}
	if requiredTransport == "" {
		requiredTransport = OpenAIUpstreamTransportResponsesWebsocketV2Ingress
	}
	request := OpenAIAccountScheduleRequest{GroupID: groupID, Platform: PlatformOpenAI, RequestedModel: model, RequiredTransport: requiredTransport, RequiredCapability: capability, RequirePrivacySet: s.openAIGroupRequiresPrivacySet(ctx, groupID)}
	var current *Account
	alternatives := make([]*Account, 0, len(accounts))
	for i := range accounts {
		account := &accounts[i]
		if !account.IsSchedulable() || account.Platform != PlatformOpenAI || !s.openAIAccountMatchesSchedulingGroup(account, groupID) || !checker.isAccountRequestCompatible(ctx, account, request) || !checker.isAccountTransportCompatible(account, request.RequiredTransport) {
			continue
		}
		if account.ID == currentID {
			current = account
		} else {
			alternatives = append(alternatives, account)
		}
	}
	if current == nil {
		return
	}
	rand.Shuffle(len(alternatives), func(i, j int) { alternatives[i], alternatives[j] = alternatives[j], alternatives[i] })
	if len(alternatives) > probeCount-1 {
		alternatives = alternatives[:probeCount-1]
	}
	candidates := append([]*Account{current}, alternatives...)
	results := make(chan openAIWSLatencyProbeResult, probeCount)
	var workers sync.WaitGroup
	for i := 0; i < probeCount; i++ {
		startIndex := i % len(candidates)
		probeNumber := i + 1
		workers.Add(1)
		go func(probeNumber int, startIndex int) {
			defer workers.Done()
			if !s.openaiWSLatencyProbeLimiter.acquire(ctx, probeCount) {
				results <- openAIWSLatencyProbeResult{
					probeNumber:        probeNumber,
					requestedAccountID: candidates[startIndex].ID,
					failure:            "probe_capacity_wait_timeout",
				}
				return
			}
			defer s.openaiWSLatencyProbeLimiter.release()
			result := s.probeOpenAIWSLatencyCandidates(ctx, candidates, startIndex, model, request)
			result.probeNumber = probeNumber
			results <- result
		}(probeNumber, startIndex)
	}
	workers.Wait()
	close(results)
	observed := make([]openAIWSLatencyProbeResult, 0, probeCount)
	for result := range results {
		observed = append(observed, result)
	}
	targetID = chooseOpenAIWSLatencyTarget(observed, currentID)
	probeLogResults := make([]openAIWSLatencyProbeLogResult, 0, len(observed))
	forwardedAttempts := 0
	for _, result := range observed {
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
	sort.Slice(probeLogResults, func(i, j int) bool {
		return probeLogResults[i].ProbeNumber < probeLogResults[j].ProbeNumber
	})
	actor.writeEvent("openai.ws_latency_switch_probe_completed", map[string]any{
		"from_account_id":    currentID,
		"probe_limit":        probeCount,
		"probes":             len(observed),
		"forwarded_attempts": forwardedAttempts,
		"probe_results":      probeLogResults,
		"target_account_id":  targetID,
		"model":              model,
		"transport":          string(requiredTransport),
	})
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

func (s *OpenAIGatewayService) forwardOpenAIWSLatencyProbe(ctx context.Context, account *Account, model string, requiredTransport OpenAIUpstreamTransport) (openAIWSLatencyProbeResult, error) {
	observed := openAIWSLatencyProbeResult{accountID: account.ID}
	probeCtx, cancel := context.WithTimeout(ctx, openAIWSLatencyProbeTimeout)
	defer cancel()
	// Synthetic input never copies user messages, tools, response references or
	// session IDs. Probes consume upstream quota but are not billed to a user.
	body, _ := json.Marshal(map[string]any{"model": model, "stream": true, "store": false, "max_output_tokens": 64, "input": []any{map[string]any{"role": "user", "content": "Reply with the single word OK."}}})
	request, err := http.NewRequestWithContext(probeCtx, http.MethodPost, "http://localhost/v1/responses", bytes.NewReader(body))
	if err != nil {
		return observed, err
	}
	applyOpenAICodexProbeHeaders(request.Header)
	writer := &openAIWSLatencyProbeWriter{header: make(http.Header), mode: s.openAITTFTMode(ctx), started: time.Now(), firstMS: -1, closed: make(chan bool)}
	c := &gin.Context{Request: request, Writer: writer}
	if requiredTransport == OpenAIUpstreamTransportHTTPSSE {
		SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
	} else {
		SetOpenAIClientTransport(c, OpenAIClientTransportWS)
	}
	result, forwardErr := s.Forward(probeCtx, c, account, body)
	if writer.firstMS >= 0 && (forwardErr == nil || errors.Is(probeCtx.Err(), context.DeadlineExceeded)) {
		observed.ms, observed.ok = writer.firstMS, true
	} else if forwardErr == nil && result != nil && result.SucceededForScheduling() && result.FirstTokenMs != nil {
		observed.ms, observed.ok = *result.FirstTokenMs, true
	} else if errors.Is(probeCtx.Err(), context.DeadlineExceeded) {
		observed.ms, observed.timedOut = int(time.Since(writer.started).Milliseconds()), true
	}
	if writer.failed || writer.Status() >= 400 {
		observed.ok = false
	}
	return observed, forwardErr
}

// Discards generated text and retains at most one bounded SSE line. The timing
// uses the site's normal TTFT definition, including its visible-output option.
type openAIWSLatencyProbeWriter struct {
	header  http.Header
	status  int
	size    int
	line    []byte
	mode    string
	started time.Time
	firstMS int
	failed  bool
	closed  chan bool
}

func (w *openAIWSLatencyProbeWriter) Header() http.Header { return w.header }
func (w *openAIWSLatencyProbeWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *openAIWSLatencyProbeWriter) WriteHeaderNow() { w.WriteHeader(http.StatusOK) }
func (w *openAIWSLatencyProbeWriter) Status() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}
func (w *openAIWSLatencyProbeWriter) Size() int {
	if !w.Written() {
		return -1
	}
	return w.size
}
func (w *openAIWSLatencyProbeWriter) Written() bool            { return w.status != 0 }
func (w *openAIWSLatencyProbeWriter) Flush()                   { w.WriteHeaderNow() }
func (w *openAIWSLatencyProbeWriter) CloseNotify() <-chan bool { return w.closed }
func (w *openAIWSLatencyProbeWriter) Pusher() http.Pusher      { return nil }
func (w *openAIWSLatencyProbeWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, fmt.Errorf("latency probe does not support hijacking")
}
func (w *openAIWSLatencyProbeWriter) WriteString(s string) (int, error) { return w.Write([]byte(s)) }
func (w *openAIWSLatencyProbeWriter) Write(data []byte) (int, error) {
	w.WriteHeaderNow()
	w.size += len(data)
	for _, b := range data {
		if b != '\n' {
			if len(w.line) < 65536 {
				w.line = append(w.line, b)
			}
			continue
		}
		line := strings.TrimSpace(string(w.line))
		w.line = w.line[:0]
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		eventType := gjson.Get(payload, "type").String()
		if eventType == "error" || eventType == "response.failed" {
			w.failed = true
		}
		if w.firstMS < 0 && openAIStreamDataStartsTTFT(payload, eventType, false, w.mode) {
			w.firstMS = int(time.Since(w.started).Milliseconds())
		}
	}
	return len(data), nil
}

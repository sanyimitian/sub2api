package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

type openAIWSLatencyProbeResult struct {
	accountID int64
	ms        int
	ok        bool
	timedOut  bool
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

func (limiter *openAIWSLatencyProbeLimiter) release() {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	limiter.active--
}

func chooseOpenAIWSLatencyTarget(results []openAIWSLatencyProbeResult, currentID int64) int64 {
	baseline := 0
	for _, result := range results {
		if result.accountID == currentID && (result.ok || result.timedOut) {
			baseline = result.ms
		}
	}
	bestID, bestMS := int64(0), baseline
	for _, result := range results {
		// Require both a 20% and 250ms improvement to avoid switching on jitter.
		if result.accountID != currentID && result.ok && result.ms >= 0 && result.ms+250 < baseline && int64(result.ms)*5 < int64(baseline)*4 && result.ms < bestMS {
			bestID, bestMS = result.accountID, result.ms
		}
	}
	return bestID
}

func (s *OpenAIGatewayService) probeOpenAIWSLatencyRoutes(ctx context.Context, key string, groupID *int64, currentID int64, model string, capability OpenAIEndpointCapability, probeID string) {
	ctx, cancel := context.WithTimeout(ctx, openAIWSLatencyProbeTimeout+time.Second)
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
			slog.Info("openai.ws_latency_switch_pending", "from_account_id", currentID, "target_account_id", targetID, "model", model)
		}
	}()
	settings := s.settingService.GetOpenAIWSLatencySwitchSettings(ctx)
	if !settings.Enabled {
		return
	}
	probeCount := settings.ProbeCount
	accounts, err := s.listSchedulableAccounts(ctx, groupID, PlatformOpenAI)
	if err != nil || s.checkChannelPricingRestriction(ctx, groupID, model) {
		return
	}
	checker := &defaultOpenAIAccountScheduler{service: s}
	request := OpenAIAccountScheduleRequest{GroupID: groupID, Platform: PlatformOpenAI, RequestedModel: model, RequiredTransport: OpenAIUpstreamTransportResponsesWebsocketV2Ingress, RequiredCapability: capability, RequirePrivacySet: s.openAIGroupRequiresPrivacySet(ctx, groupID)}
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
	if current == nil || len(alternatives) == 0 {
		return
	}
	rand.Shuffle(len(alternatives), func(i, j int) { alternatives[i], alternatives[j] = alternatives[j], alternatives[i] })
	if len(alternatives) > probeCount-1 {
		alternatives = alternatives[:probeCount-1]
	}
	candidates := append([]*Account{current}, alternatives...)
	results := make(chan openAIWSLatencyProbeResult, len(candidates))
	var workers sync.WaitGroup
	for _, account := range candidates {
		// A global cap prevents many slow conversations from multiplying probes.
		if !s.openaiWSLatencyProbeLimiter.tryAcquire(probeCount) {
			continue
		}
		workers.Add(1)
		go func(account *Account) {
			defer workers.Done()
			defer s.openaiWSLatencyProbeLimiter.release()
			results <- s.probeOpenAIWSLatencyAccount(ctx, account, model)
		}(account)
	}
	workers.Wait()
	close(results)
	observed := make([]openAIWSLatencyProbeResult, 0, len(candidates))
	for result := range results {
		observed = append(observed, result)
	}
	targetID = chooseOpenAIWSLatencyTarget(observed, currentID)
	slog.Info("openai.ws_latency_probe_completed", "from_account_id", currentID, "probe_limit", probeCount, "probes", len(observed), "target_account_id", targetID, "model", model)
}

func (s *OpenAIGatewayService) probeOpenAIWSLatencyAccount(ctx context.Context, account *Account, model string) openAIWSLatencyProbeResult {
	observed := openAIWSLatencyProbeResult{accountID: account.ID}
	lease, err := s.tryAcquireAccountSlot(ctx, account.ID, account.Concurrency)
	if err != nil || lease == nil || !lease.Acquired {
		return observed
	}
	if lease.ReleaseFunc != nil {
		defer lease.ReleaseFunc()
	}
	probeCtx, cancel := context.WithTimeout(ctx, openAIWSLatencyProbeTimeout)
	defer cancel()
	// Synthetic input never copies user messages, tools, response references or
	// session IDs. Probes consume upstream quota but are not billed to a user.
	body, _ := json.Marshal(map[string]any{"model": model, "stream": true, "store": false, "max_output_tokens": 64, "input": []any{map[string]any{"role": "user", "content": "Reply with the single word OK."}}})
	request, err := http.NewRequestWithContext(probeCtx, http.MethodPost, "http://localhost/v1/responses", bytes.NewReader(body))
	if err != nil {
		return observed
	}
	applyOpenAICodexProbeHeaders(request.Header)
	writer := &openAIWSLatencyProbeWriter{header: make(http.Header), mode: s.openAITTFTMode(ctx), started: time.Now(), firstMS: -1, closed: make(chan bool)}
	c := &gin.Context{Request: request, Writer: writer}
	SetOpenAIClientTransport(c, OpenAIClientTransportWS)
	result, forwardErr := s.Forward(probeCtx, c, account, body)
	if writer.failed || writer.Status() >= 400 {
		return observed
	}
	if writer.firstMS >= 0 && (forwardErr == nil || errors.Is(probeCtx.Err(), context.DeadlineExceeded)) {
		observed.ms, observed.ok = writer.firstMS, true
	} else if forwardErr == nil && result != nil && result.SucceededForScheduling() && result.FirstTokenMs != nil {
		observed.ms, observed.ok = *result.FirstTokenMs, true
	} else if errors.Is(probeCtx.Err(), context.DeadlineExceeded) {
		observed.ms, observed.timedOut = int(time.Since(writer.started).Milliseconds()), true
	}
	return observed
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

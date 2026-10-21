package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func latencyDiagnosticContext() context.Context {
	ctx := context.WithValue(context.Background(), ctxkey.RequestID, "server-request")
	ctx = context.WithValue(ctx, ctxkey.ClientRequestID, "client-request")
	return logger.IntoContext(ctx, logger.With(zap.String("request_id", "server-request"), zap.String("client_request_id", "client-request")))
}

func latencyDiagnosticEvents(sink *inMemoryLogSink, stage string) []*logger.LogEvent {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	var events []*logger.LogEvent
	for _, event := range sink.events {
		if event.Message == "openai.latency_diagnostic" && event.Fields["stage"] == stage {
			events = append(events, event)
		}
	}
	return events
}

func TestOpenAIWSLatencyDiagnosticDecisions(t *testing.T) {
	tests := []struct {
		name, payload, reason                                                  string
		firstMS, tokens, expectedCount                                         int
		state                                                                  openAIWSLatencyState
		noResult, noFirst, failed, unsuccessful, recovering, noAccount, noTurn bool
	}{
		{name: "工具结果不计数", payload: `{"input":[{"role":"user","content":"private-context"},{"type":"function_call_output","call_id":"private-call","output":"private-output"}]}`, reason: "tool_output_excluded", firstMS: 17000, tokens: 208, state: openAIWSLatencyState{AccountID: 11, SlowCount: 1}, expectedCount: 1},
		{name: "预热不计数", payload: `{"generate":false,"input":"private-context"}`, reason: "prewarm_excluded", firstMS: 17000, tokens: 208},
		{name: "空输入", payload: `{"input":[]}`, reason: "empty_input", firstMS: 17000, tokens: 208},
		{name: "非用户输入", payload: `{"input":[{"role":"private-role","type":"private-type"}]}`, reason: "non_user_request", firstMS: 17000, tokens: 208},
		{name: "速度门槛满足但次数不足", reason: "consecutive_not_reached", firstMS: 15000, tokens: 208, expectedCount: 1},
		{name: "输出速度等于最低值不切换", reason: "output_tps_not_below_minimum", firstMS: 17000, tokens: 300, state: openAIWSLatencyState{AccountID: 11, SlowCount: 1}},
		{name: "首字未达门槛", reason: "first_token_below_threshold", firstMS: 14999, tokens: 208, state: openAIWSLatencyState{AccountID: 11, SlowCount: 1}},
		{name: "输出速度未知", reason: "output_tps_unknown", firstMS: 17000},
		{name: "首字未知", reason: "first_token_unknown", noFirst: true, tokens: 208},
		{name: "请求失败", reason: "request_failed", failed: true, firstMS: 17000, tokens: 208},
		{name: "上游未成功完成", reason: "request_unsuccessful", unsuccessful: true, firstMS: 17000, tokens: 208},
		{name: "结果缺失", reason: "result_missing", noResult: true},
		{name: "接管请求失败", reason: "recovery_request_unsuccessful", recovering: true, failed: true, firstMS: 17000, tokens: 208},
		{name: "账号缺失", reason: "account_missing", noAccount: true, firstMS: 17000, tokens: 208},
		{name: "轮次缺失", reason: "turn_classification_missing", noTurn: true, firstMS: 17000, tokens: 208},
		{name: "冷却中", reason: "cooldown_active", firstMS: 17000, tokens: 208, state: openAIWSLatencyState{AccountID: 11, SlowCount: 1, CooldownUntil: time.Now().Add(time.Hour).UnixMilli()}, expectedCount: 1},
		{name: "等待切换", reason: "switch_pending", firstMS: 17000, tokens: 208, state: openAIWSLatencyState{AccountID: 11, TargetID: 22, PendingUntil: time.Now().Add(time.Hour).UnixMilli(), CooldownUntil: time.Now().Add(time.Hour).UnixMilli()}},
		{name: "正在探测", reason: "probe_in_progress", firstMS: 17000, tokens: 208, state: openAIWSLatencyState{AccountID: 11, ProbeID: "active-probe", ProbeUntil: time.Now().Add(time.Hour).UnixMilli(), CooldownUntil: time.Now().Add(time.Hour).UnixMilli()}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, _ := newOpenAIWSLatencyTestService(t)
			sink, cleanup := captureStructuredLog(t)
			defer cleanup()
			gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{latencySwitch: OpenAIWSLatencySwitchSettings{Enabled: true, SlowMS: 15000, MinTPS: 15, Consecutive: 3}, expiresAt: time.Now().Add(time.Hour).UnixNano()})
			ctx := latencyDiagnosticContext()
			groupID := int64(49)
			if tt.payload == "" {
				tt.payload = `{"input":"private-context","previous_response_id":"private-response"}`
			}
			session := svc.NewOpenAIHTTPResponsesLatencySession(ctx, &groupID, &APIKey{ID: 859, UserID: 999, Key: "sk-private-key"}, "private-session", "test-model", "test-model", OpenAIEndpointCapabilityResponses, []byte(tt.payload))
			require.NotNil(t, session)
			svc.updateOpenAIWSLatencyState(ctx, session.key(session.model), func(state *openAIWSLatencyState) bool { *state = tt.state; return true })
			result := &OpenAIForwardResult{FirstTokenMs: &tt.firstMS, Duration: 20 * time.Second, Usage: OpenAIUsage{OutputTokens: tt.tokens}}
			if tt.noFirst {
				result.FirstTokenMs = nil
			}
			if tt.unsuccessful {
				result.OpenAIWSMode = true
				result.UpstreamTerminalEvent = "response.failed"
			}
			if tt.noResult {
				result = nil
			}
			var turnErr error
			if tt.failed {
				turnErr = errors.New("private-error-sk-secret")
			}
			account := &Account{ID: 11}
			if tt.noAccount {
				account = nil
			}
			if tt.noTurn {
				delete(session.turns, 1)
			}
			session.recovering, session.recoveryConfirmed = tt.recovering, tt.recovering
			session.Observe(ctx, 1, account, result, turnErr)
			events := latencyDiagnosticEvents(sink, "observe")
			require.Len(t, events, 1)
			fields := events[0].Fields
			require.Equal(t, tt.reason, fields["reason"])
			require.Equal(t, "server-request", fields["request_id"])
			require.Equal(t, "client-request", fields["client_request_id"])
			require.EqualValues(t, 999, fields["user_id"])
			require.EqualValues(t, 859, fields["api_key_id"])
			require.EqualValues(t, 49, fields["group_id"])
			require.Equal(t, session.scope, fields["scope"])
			require.EqualValues(t, 15000, fields["slow_threshold_ms"])
			require.EqualValues(t, 15, fields["min_output_tps"])
			require.Equal(t, true, fields[logger.OpsSystemLogSkipField])
			if tt.reason == "tool_output_excluded" {
				require.Equal(t, "info", events[0].Level)
				require.Equal(t, true, fields["first_token_threshold_met"])
				require.Equal(t, true, fields["output_tps_threshold_met"])
				require.InDelta(t, 10.4, fields["output_tps"], 0.001)
			}
			serialized, err := json.Marshal(events)
			require.NoError(t, err)
			require.NotContains(t, string(serialized), "private-")
			require.Equal(t, tt.expectedCount, session.state(ctx).SlowCount)
		})
	}
}

type latencyDiagnosticFailureCache struct {
	stubGatewayCache
	mode string
}

func (cache *latencyDiagnosticFailureCache) GetOpenAIWSLatencyState(context.Context, string) ([]byte, error) {
	switch cache.mode {
	case "read":
		return nil, errors.New("private-cache-password")
	case "decode":
		return []byte("private-cache-body"), nil
	default:
		return nil, nil
	}
}

func (cache *latencyDiagnosticFailureCache) CompareAndSwapOpenAIWSLatencyState(context.Context, string, []byte, []byte, time.Duration) (bool, error) {
	if cache.mode == "write" {
		return false, context.DeadlineExceeded
	}
	return false, nil
}

func TestOpenAIWSLatencyDiagnosticStateFailures(t *testing.T) {
	for _, mode := range []string{"read", "decode", "write", "conflict"} {
		for _, pool := range []bool{false, true} {
			t.Run(mode+map[bool]string{false: "状态", true: "共享池"}[pool], func(t *testing.T) {
				svc, _ := newOpenAIWSLatencyTestService(t)
				sink, cleanup := captureStructuredLog(t)
				defer cleanup()
				svc.cache = &latencyDiagnosticFailureCache{mode: mode}
				ctx := latencyDiagnosticContext()
				var updated bool
				if pool {
					_, updated = svc.updateOpenAIWSLatencyCandidatePool(ctx, "private-key", func(*openAIWSLatencyCandidatePool) bool { return true })
				} else {
					_, updated = svc.updateOpenAIWSLatencyState(ctx, "private-key", func(*openAIWSLatencyState) bool { return true })
				}
				require.False(t, updated)
				events := latencyDiagnosticEvents(sink, "state")
				require.Len(t, events, 1)
				reasons := map[string]string{"read": "state_read_failed", "decode": "state_decode_failed", "write": "state_write_failed", "conflict": "state_conflict_exhausted"}
				require.Equal(t, reasons[mode], events[0].Fields["reason"])
				require.Equal(t, "warn", events[0].Level)
				require.Equal(t, "server-request", events[0].Fields["request_id"])
				if mode == "write" {
					require.Equal(t, true, events[0].Fields["timeout"])
				}
				if mode == "conflict" {
					require.EqualValues(t, map[bool]int{false: 4, true: 8}[pool], events[0].Fields["attempt"])
				}
				serialized, err := json.Marshal(events)
				require.NoError(t, err)
				require.NotContains(t, string(serialized), "private-")
			})
		}
	}
}

func TestOpenAIWSLatencyDiagnosticSessionAndLifecycleCorrelation(t *testing.T) {
	svc, _ := newOpenAIWSLatencyTestService(t)
	sink, cleanup := captureStructuredLog(t)
	defer cleanup()
	ctx := latencyDiagnosticContext()
	groupID := int64(49)
	apiKey := &APIKey{ID: 859, UserID: 999}
	session := svc.NewOpenAIHTTPResponsesLatencySession(ctx, &groupID, apiKey, "", "test-model", "test-model", OpenAIEndpointCapabilityResponses, nil)
	require.Nil(t, session)
	events := latencyDiagnosticEvents(sink, "session")
	require.Len(t, events, 1)
	require.Equal(t, "session_hash_missing", events[0].Fields["reason"])
	session = svc.NewOpenAIHTTPResponsesLatencySession(ctx, &groupID, apiKey, "session", "test-model", "test-model", OpenAIEndpointCapabilityResponses, nil)
	require.NotNil(t, session)
	session.actor.writeEvent("openai.ws_latency_switch_pending", map[string]any{})
	sink.mu.Lock()
	defer sink.mu.Unlock()
	event := sink.events[len(sink.events)-1]
	require.Equal(t, "server-request", event.Fields["request_id"])
	require.Equal(t, "client-request", event.Fields["client_request_id"])
	require.EqualValues(t, 49, event.Fields["group_id"])
}

func TestOpenAIWSLatencyDiagnosticTriggerAndProbeExit(t *testing.T) {
	svc, _ := newOpenAIWSLatencyTestService(t)
	sink, cleanup := captureStructuredLog(t)
	defer cleanup()
	gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{latencySwitch: OpenAIWSLatencySwitchSettings{Enabled: true, SlowMS: 15000, MinTPS: 15, Consecutive: 1, ProbeCount: 6}, expiresAt: time.Now().Add(time.Hour).UnixNano()})
	ctx := latencyDiagnosticContext()
	groupID := int64(49)
	session := svc.NewOpenAIHTTPResponsesLatencySession(ctx, &groupID, &APIKey{ID: 859, UserID: 999}, "session", "test-model", "test-model", OpenAIEndpointCapabilityResponses, []byte(`{"input":"private-input"}`))
	firstMS := 17000
	session.Observe(ctx, 1, &Account{ID: 11}, &OpenAIForwardResult{FirstTokenMs: &firstMS, Duration: 20 * time.Second, Usage: OpenAIUsage{OutputTokens: 208}}, nil)
	require.Eventually(t, func() bool {
		return len(latencyDiagnosticEvents(sink, "probe")) > 0 && session.state(ctx).ProbeID == ""
	}, time.Second, time.Millisecond)
	observed := latencyDiagnosticEvents(sink, "observe")
	require.Len(t, observed, 1)
	require.Equal(t, "probe_triggered", observed[0].Fields["reason"])
	require.Equal(t, "thresholds_met", observed[0].Fields["threshold_decision"])
	probed := latencyDiagnosticEvents(sink, "probe")
	require.Len(t, probed, 1)
	require.Equal(t, "probe_no_eligible_accounts", probed[0].Fields["reason"])
	require.NotEmpty(t, probed[0].Fields["probe_id"])
	require.Equal(t, observed[0].Fields["probe_id"], probed[0].Fields["probe_id"])
	require.Equal(t, "server-request", probed[0].Fields["request_id"])
}

func TestOpenAIWSLatencyDiagnosticSharedCandidateReuse(t *testing.T) {
	svc, _ := newOpenAIWSLatencyTestService(t)
	sink, cleanup := captureStructuredLog(t)
	defer cleanup()
	ctx := latencyDiagnosticContext()
	groupID := int64(1)
	model := "test-model"
	gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{latencySwitch: OpenAIWSLatencySwitchSettings{Enabled: true, ProbeCount: 6, WaitSeconds: 60, ProbeReuseSeconds: 600}, expiresAt: time.Now().Add(time.Hour).UnixNano()})
	svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: []Account{{ID: 11, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, GroupIDs: []int64{1}}}}
	session := svc.NewOpenAIHTTPResponsesLatencySession(ctx, &groupID, &APIKey{ID: 7, UserID: 42}, "session", model, model, OpenAIEndpointCapabilityResponses, []byte(`{"input":"private-input"}`))
	poolKey := openAIWSLatencyCandidatePoolKey(&groupID, model, OpenAIUpstreamTransportHTTPSSE)
	svc.addOpenAIWSLatencyCandidates(ctx, poolKey, []openAIWSLatencyProbeResult{{resultID: "result-1", accountID: 11, upstreamModel: model, upstreamSessionID: "private-upstream-session", ms: 1000, ok: true}}, 600, 0, "", 60)
	svc.updateOpenAIWSLatencyState(ctx, session.key(model), func(state *openAIWSLatencyState) bool {
		*state = openAIWSLatencyState{AccountID: 11, ProbeID: "probe-1"}
		return true
	})
	svc.probeOpenAIWSLatencyRoutes(ctx, session.key(model), &groupID, 11, model, model, OpenAIEndpointCapabilityResponses, OpenAIUpstreamTransportHTTPSSE, "probe-1", session.actor)
	events := latencyDiagnosticEvents(sink, "probe")
	require.Len(t, events, 1)
	require.Equal(t, "candidate_reserved", events[0].Fields["reason"])
	require.Equal(t, true, events[0].Fields["reused_probe_result"])
	require.EqualValues(t, 11, session.state(ctx).TargetID)
	serialized, err := json.Marshal(events)
	require.NoError(t, err)
	require.NotContains(t, string(serialized), "private-")
}

func TestOpenAIWSLatencyDiagnosticFailedWriteDoesNotReportPersistedCount(t *testing.T) {
	svc, _ := newOpenAIWSLatencyTestService(t)
	sink, cleanup := captureStructuredLog(t)
	defer cleanup()
	svc.cache = &latencyDiagnosticFailureCache{mode: "write"}
	ctx := latencyDiagnosticContext()
	session := svc.NewOpenAIHTTPResponsesLatencySession(ctx, nil, &APIKey{ID: 1}, "session", "test-model", "test-model", OpenAIEndpointCapabilityResponses, []byte(`{"input":"private-input"}`))
	firstMS := 16000
	session.Observe(ctx, 1, &Account{ID: 11}, &OpenAIForwardResult{FirstTokenMs: &firstMS}, nil)
	events := latencyDiagnosticEvents(sink, "observe")
	require.Len(t, events, 1)
	require.Equal(t, "state_update_failed", events[0].Fields["reason"])
	require.Equal(t, false, events[0].Fields["state_updated"])
	require.NotContains(t, events[0].Fields, "slow_count_after")
}

func TestOpenAIWSLatencyDiagnosticReconnectDecisions(t *testing.T) {
	for _, tt := range []struct {
		name, payload, reason string
		expired, rejected     bool
	}{
		{"工具回传仍跳过恢复", `{"input":[{"type":"function_call_output","call_id":"private-call","output":"private-output"}]}`, "reconnect_non_user_request", false, false},
		{"等待过期", `{"input":"private-input"}`, "pending_expired", true, false},
		{"旧响应引用拒绝接管", `{"previous_response_id":"private-response","input":"private-input"}`, "reconnect_context_rejected", false, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			svc, _ := newOpenAIWSLatencyTestService(t)
			sink, cleanup := captureStructuredLog(t)
			defer cleanup()
			ctx := latencyDiagnosticContext()
			session := svc.NewOpenAIHTTPResponsesLatencySession(ctx, nil, &APIKey{ID: 7}, "private-session", "test-model", "test-model", OpenAIEndpointCapabilityResponses, []byte(tt.payload))
			state := openAIWSLatencyState{AccountID: 11, TargetID: 11, TargetResultID: "result-1", UpstreamSessionID: "private-upstream", CandidateLeaseID: "private-lease", ReconnectFrom: "private-connection", PendingUntil: time.Now().Add(time.Minute).UnixMilli()}
			if tt.expired {
				state.PendingUntil = time.Now().Add(-time.Second).UnixMilli()
			}
			svc.updateOpenAIWSLatencyState(ctx, session.key(session.model), func(s *openAIWSLatencyState) bool { *s = state; return true })
			retry, err := session.PrepareHTTP(ctx, []byte(tt.payload))
			require.Equal(t, tt.rejected, err != nil)
			require.False(t, retry)
			require.False(t, session.recovering)
			require.Equal(t, state, session.state(ctx), "诊断不能改变重连状态")
			events := latencyDiagnosticEvents(sink, "reconnect")
			require.Len(t, events, 1)
			require.Equal(t, tt.reason, events[0].Fields["reason"])
			require.Equal(t, "server-request", events[0].Fields["request_id"])
			require.Equal(t, session.scope, events[0].Fields["scope"])
			require.Equal(t, true, events[0].Fields["reconnect_requested"])
			if tt.reason == "reconnect_non_user_request" {
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				require.NoError(t, session.ApplyUpstreamSessionIdentity(ctx, c, &Account{ID: 11}, session.model))
				require.Equal(t, "private-upstream", OpenAIWSLatencyUpstreamSessionID(c))
				routes := latencyDiagnosticEvents(sink, "route")
				require.Len(t, routes, 1)
				require.Equal(t, "upstream_session_applied", routes[0].Fields["reason"])
				require.Equal(t, false, routes[0].Fields["recovering"])
				require.NotEmpty(t, routes[0].Fields["upstream_session_hash"])
				events = append(events, routes...)
			}
			serialized, err := json.Marshal(events)
			require.NoError(t, err)
			require.NotContains(t, string(serialized), "private-")
		})
	}
}

func TestOpenAIWSLatencyDiagnosticCompletionDecisions(t *testing.T) {
	for _, reason := range []string{"completed", "target_account_mismatch", "upstream_session_missing", "candidate_lease_missing", "non_user_request", "context_unconfirmed", "request_unsuccessful", "state_update_failed"} {
		t.Run(reason, func(t *testing.T) {
			svc, _ := newOpenAIWSLatencyTestService(t)
			sink, cleanup := captureStructuredLog(t)
			defer cleanup()
			ctx := latencyDiagnosticContext()
			payload := []byte(`{"input":"private-input"}`)
			if reason == "non_user_request" {
				payload = []byte(`{"input":[{"type":"function_call_output","output":"private-output"}]}`)
			}
			session := svc.NewOpenAIHTTPResponsesLatencySession(ctx, nil, &APIKey{ID: 7}, "private-session", "test-model", "test-model", OpenAIEndpointCapabilityResponses, payload)
			session.recovering, session.recoveryConfirmed = true, reason != "context_unconfirmed"
			state := openAIWSLatencyState{AccountID: 11, TargetID: 11, TargetResultID: "result-1", UpstreamSessionID: "private-upstream", CandidateLeaseID: "private-lease", PendingUntil: time.Now().Add(time.Minute).UnixMilli()}
			switch reason {
			case "target_account_mismatch":
				state.TargetID = 22
			case "upstream_session_missing":
				state.UpstreamSessionID = ""
			case "candidate_lease_missing":
				state.CandidateLeaseID = ""
			}
			svc.updateOpenAIWSLatencyState(ctx, session.key(session.model), func(s *openAIWSLatencyState) bool { *s = state; return true })
			if reason == "state_update_failed" {
				svc.cache = &latencyDiagnosticFailureCache{mode: "read"}
			}
			var turnErr error
			if reason == "request_unsuccessful" {
				turnErr = errors.New("private-error")
			}
			firstMS := 1000
			session.Observe(ctx, 1, &Account{ID: 11}, &OpenAIForwardResult{FirstTokenMs: &firstMS, Duration: 88 * time.Second, Usage: OpenAIUsage{OutputTokens: 100}}, turnErr)
			events := latencyDiagnosticEvents(sink, "observe")
			require.Len(t, events, 1, "完成判据合并进原统计事件")
			require.Equal(t, reason, events[0].Fields["switch_completion_decision"])
			require.Equal(t, true, events[0].Fields[logger.OpsSystemLogSkipField])
			require.Contains(t, []string{"info", "warn"}, events[0].Level)
			if reason == "completed" {
				require.Equal(t, true, events[0].Fields["switch_completed"])
				require.EqualValues(t, 88000, events[0].Fields["duration_ms"])
				require.Zero(t, session.state(ctx).TargetID)
			} else if reason != "state_update_failed" {
				require.Equal(t, state, session.state(ctx))
			}
			serialized, err := json.Marshal(events)
			require.NoError(t, err)
			require.NotContains(t, string(serialized), "private-")
		})
	}
}

func TestOpenAIWSLatencyDiagnosticOccupiedPoolWait(t *testing.T) {
	for _, outcome := range []string{"canceled", "deadline_exceeded", "candidate_reserved"} {
		t.Run(outcome, func(t *testing.T) {
			svc, cache := newOpenAIWSLatencyTestService(t)
			sink, cleanup := captureStructuredLog(t)
			defer cleanup()
			gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{latencySwitch: OpenAIWSLatencySwitchSettings{Enabled: true, ProbeCount: 6, WaitSeconds: 3600, ProbeReuseSeconds: 3600}, expiresAt: time.Now().Add(time.Hour).UnixNano()})
			ctx, cancel := context.WithCancel(latencyDiagnosticContext())
			defer cancel()
			if outcome == "deadline_exceeded" {
				var stop context.CancelFunc
				ctx, stop = context.WithTimeout(ctx, 350*time.Millisecond)
				defer stop()
			}
			groupID := int64(1)
			model := "test-model"
			svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: []Account{{ID: 11, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, GroupIDs: []int64{1}}}}
			session := svc.NewOpenAIHTTPResponsesLatencySession(ctx, &groupID, &APIKey{ID: 7}, "private-session", model, model, OpenAIEndpointCapabilityResponses, []byte(`{"input":"private-input"}`))
			poolKey := openAIWSLatencyCandidatePoolKey(&groupID, model, OpenAIUpstreamTransportHTTPSSE)
			svc.addOpenAIWSLatencyCandidates(ctx, poolKey, []openAIWSLatencyProbeResult{{resultID: "result-1", accountID: 11, upstreamModel: model, upstreamSessionID: "private-upstream-session", ms: 1000, ok: true}}, 3600, 11, "private-lease", 3600)
			svc.updateOpenAIWSLatencyState(ctx, session.key(model), func(state *openAIWSLatencyState) bool {
				*state = openAIWSLatencyState{AccountID: 11, ProbeID: "probe-wait"}
				return true
			})
			done := make(chan struct{})
			go func() {
				defer close(done)
				svc.probeOpenAIWSLatencyRoutes(ctx, session.key(model), &groupID, 11, model, model, OpenAIEndpointCapabilityResponses, OpenAIUpstreamTransportHTTPSSE, "probe-wait", session.actor)
			}()
			defer func() { cancel(); <-done }()
			require.Eventually(t, func() bool { return len(latencyDiagnosticEvents(sink, "probe")) > 0 }, time.Second, time.Millisecond)
			if outcome == "candidate_reserved" {
				svc.releaseOpenAIWSLatencyCandidate(ctx, poolKey, "private-lease", 11, false)
			} else if outcome == "canceled" {
				cancel()
			}
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("等待任务未及时退出")
			}
			events := latencyDiagnosticEvents(sink, "probe")
			counts := map[string]int{}
			for _, event := range events {
				counts[event.Fields["reason"].(string)]++
			}
			require.Equal(t, 1, counts["shared_pool_wait"])
			require.Equal(t, 1, counts["shared_pool_wait_ended"])
			require.Zero(t, counts["new_probe_started"], "日志补充不能让占用候选触发新探测")
			first := events[0].Fields
			require.EqualValues(t, 1, first["leased_candidate_count"])
			require.EqualValues(t, 0, first["available_candidate_count"])
			require.EqualValues(t, 3600, first["switch_wait_seconds"])
			require.EqualValues(t, 3600, first["probe_reuse_seconds"])
			require.NotEmpty(t, first["pool_key_hash"])
			last := events[len(events)-1].Fields
			require.Equal(t, outcome, last["wait_outcome"])
			require.GreaterOrEqual(t, last["wait_duration_ms"], int64(0))
			require.Equal(t, outcome == "deadline_exceeded", last["deadline_exceeded"])
			if outcome == "candidate_reserved" {
				require.Equal(t, 1, counts["candidate_reserved"])
				require.EqualValues(t, 11, session.state(context.Background()).TargetID)
			} else {
				require.Zero(t, session.state(context.Background()).TargetID)
			}
			cache.mu.Lock()
			starts := cache.probeStarts
			cache.mu.Unlock()
			require.Equal(t, 1, starts)
			serialized, err := json.Marshal(events)
			require.NoError(t, err)
			require.NotContains(t, string(serialized), "private-")
		})
	}
}

func TestOpenAIWSLatencyDiagnosticPoolSnapshot(t *testing.T) {
	sink, cleanup := captureStructuredLog(t)
	defer cleanup()
	now := time.Now().UnixMilli()
	base := openAIWSLatencyReusableCandidate{AccountID: 11, UpstreamModel: "test-model", UpstreamSessionID: "private-upstream", FirstTokenMS: 1000, ExpiresAt: now + 60000}
	pool := openAIWSLatencyCandidatePool{ProbeUntil: now + 1000}
	for i := 0; i < 8; i++ {
		c := base
		switch i {
		case 1:
			c.ExpiresAt = now
		case 2:
			c.AccountID = 22
		case 3:
			c.UpstreamModel = "other-model"
		case 4:
			c.LeaseID, c.LeaseUntil = "private-lease", now+5000
		case 5:
			c.Committed = true
		case 6:
			c.FirstTokenMS = 10000
		case 7:
			c.LeaseID, c.LeaseUntil = "private-expired-lease", now
		}
		pool.Candidates = append(pool.Candidates, c)
	}
	before, err := json.Marshal(pool)
	require.NoError(t, err)
	fields := openAIWSLatencyPoolDiagnosticFields(pool, map[int64]struct{}{11: {}}, map[int64]string{11: "test-model"}, now)
	fields = append(fields, zap.String("stage", "probe"))
	writeOpenAIWSLatencyDiagnostic(latencyDiagnosticContext(), logger.LevelInfo, "shared_pool_wait", fields...)
	events := latencyDiagnosticEvents(sink, "probe")
	require.Len(t, events, 1)
	for key, value := range map[string]int64{
		"candidate_count": 8, "expired_candidate_count": 1, "ineligible_candidate_count": 2,
		"eligible_candidate_count": 5, "available_candidate_count": 2, "leased_candidate_count": 1,
		"committed_candidate_count": 1, "invalid_latency_candidate_count": 1,
		"shared_probe_remaining_ms": 1000, "next_candidate_expiry_ms": 60000, "next_lease_expiry_ms": 5000,
	} {
		require.EqualValues(t, value, events[0].Fields[key], key)
	}
	after, err := json.Marshal(pool)
	require.NoError(t, err)
	require.Equal(t, before, after, "统计不能修改候选池")
	serialized, err := json.Marshal(events)
	require.NoError(t, err)
	require.NotContains(t, string(serialized), "private-")
}

func TestOpenAIWSLatencyDiagnosticRecoveryCorrelation(t *testing.T) {
	for _, transport := range []OpenAIUpstreamTransport{OpenAIUpstreamTransportHTTPSSE, OpenAIUpstreamTransportResponsesWebsocketV2Ingress} {
		t.Run(string(transport), func(t *testing.T) {
			svc, _ := newOpenAIWSLatencyTestService(t)
			sink, cleanup := captureStructuredLog(t)
			defer cleanup()
			ctx := latencyDiagnosticContext()
			payload := []byte(`{"input":"private-context"}`)
			session := newOpenAIWSLatencySession(ctx, svc, nil, &APIKey{ID: 7, UserID: 42}, "scope", "private-session", "test-model", "test-model", OpenAIEndpointCapabilityResponses, transport, payload)
			svc.updateOpenAIWSLatencyState(ctx, session.key(session.model), func(state *openAIWSLatencyState) bool {
				*state = openAIWSLatencyState{AccountID: 11, TargetID: 22, TargetResultID: "result-1", ReconnectFrom: "private-connection", PendingUntil: time.Now().Add(time.Minute).UnixMilli()}
				return true
			})
			if transport == OpenAIUpstreamTransportHTTPSSE {
				retry, err := session.PrepareHTTP(ctx, payload)
				require.NoError(t, err)
				require.False(t, retry)
			} else {
				require.NoError(t, session.Prepare(ctx, payload))
			}
			sink.mu.Lock()
			defer sink.mu.Unlock()
			count := 0
			for _, event := range sink.events {
				if event.Message != "openai.ws_latency_reconnect_received" {
					continue
				}
				count++
				require.Equal(t, "server-request", event.Fields["request_id"])
				require.Equal(t, "client-request", event.Fields["client_request_id"])
				require.Equal(t, "result-1", event.Fields["target_result_id"])
				require.Equal(t, string(transport), event.Fields["transport"])
			}
			require.Equal(t, 1, count, "补充字段不重复记录恢复事件")
		})
	}
}

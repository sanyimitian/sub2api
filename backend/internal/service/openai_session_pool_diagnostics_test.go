package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func poolDiagnosticTestContext() (context.Context, *observer.ObservedLogs) {
	core, logs := observer.New(zap.DebugLevel)
	return logger.IntoContext(context.Background(), zap.New(core)), logs
}

func TestOpenAISessionPoolDiagnosticRefillSummary(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			svc, _ := newOpenAIWSLatencyTestService(t)
			svc.cfg.Gateway.OpenAIWS.Enabled = false
			a := preparedTestAccount(11, 1)
			svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: []Account{a}}
			upstream := &preparedProbeUpstream{fail: fail}
			svc.httpUpstream = upstream
			gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{sessionPoolEnabled: true, sessionPoolSize: 6,
				latencySwitch: OpenAIWSLatencySwitchSettings{ProbeCount: 2}, expiresAt: time.Now().Add(time.Hour).UnixNano()})
			ctx, logs := poolDiagnosticTestContext()
			svc.refillOpenAISessionPool(ctx)
			summaries := logs.FilterMessage("openai.session_pool_refill_completed").All()
			require.Len(t, summaries, 1)
			fields := summaries[0].ContextMap()
			require.EqualValues(t, 6, fields["target_size"])
			require.EqualValues(t, 0, fields["available_before"])
			require.NotEmpty(t, fields["refill_id"])
			require.NotEqual(t, true, fields[logger.OpsSystemLogSkipField])
			require.True(t, (&OpsSystemLogSink{}).shouldIndex(&logger.LogEvent{Level: "info", Message: summaries[0].Message, Fields: fields}))
			encoded, err := json.Marshal(fields["probe_results"])
			require.NoError(t, err)
			var records []openAISessionPoolProbeRecord
			require.NoError(t, json.Unmarshal(encoded, &records))
			require.Len(t, records, int(fields["probe_count"].(int64)))
			for i, record := range records {
				require.EqualValues(t, 11, record.AccountID)
				require.Equal(t, "gpt-5.1", record.Model)
				require.Equal(t, !fail, record.Published)
				require.Equal(t, !fail, record.Qualified)
				if record.Published {
					require.NotEmpty(t, record.ResultID, "历史入池记录必须关联实际候选")
					require.NotNil(t, record.QualityProbeEnabled)
					require.False(t, *record.QualityProbeEnabled, "本次实际只进行了低延迟探测")
				}
				if i > 0 {
					require.GreaterOrEqual(t, record.Round, records[i-1].Round)
				}
			}
			for _, secret := range []string{"upstream_session_id", "test-only", "sub2api-prepared-"} {
				require.NotContains(t, string(encoded), secret)
			}
			if fail {
				require.Equal(t, zapcore.WarnLevel, summaries[0].Level)
				require.Equal(t, "probe_rounds_exhausted", fields["reason"])
				require.EqualValues(t, 5, fields["rounds"])
				require.EqualValues(t, 10, fields["probe_count"])
				require.EqualValues(t, 0, fields["published_count"])
				require.EqualValues(t, 30000, fields["retry_after_ms"])
				require.Equal(t, map[string]int{"upstream_request_failed": 10}, fields["probe_outcomes"])
			} else {
				require.Equal(t, zapcore.InfoLevel, summaries[0].Level)
				require.Equal(t, "target_reached", fields["reason"])
				require.EqualValues(t, 3, fields["rounds"])
				require.EqualValues(t, 6, fields["probe_count"])
				require.EqualValues(t, 6, fields["published_count"])
				require.EqualValues(t, 6, fields["available_last_observed"])
				require.Equal(t, map[string]int{"qualified": 6}, fields["probe_outcomes"])
				published := logs.FilterMessage("openai.session_pool_candidate_publish").All()
				require.Len(t, published, 6)
				for _, event := range published {
					f := event.ContextMap()
					require.Equal(t, fields["refill_id"], f["refill_id"])
					require.NotEmpty(t, f["target_result_id"])
					require.NotEmpty(t, f["upstream_session_hash"])
					require.NotContains(t, f, "upstream_session_id")
				}
			}
			for _, event := range logs.FilterMessage("openai.session_pool_probe_completed").All() {
				f := event.ContextMap()
				require.Equal(t, fields["refill_id"], f["refill_id"])
				require.NotEmpty(t, f["reason"])
				require.NotEmpty(t, f["round"])
				require.NotEmpty(t, f["probe_number"])
			}
			calls := upstream.calls.Load()
			svc.refillOpenAISessionPool(ctx)
			require.Equal(t, calls, upstream.calls.Load())
			require.Len(t, logs.FilterMessage("openai.session_pool_refill_completed").All(), 1)
			skipped := logs.FilterMessage("openai.session_pool_refill_skipped").All()
			require.Len(t, skipped, 1)
			require.Equal(t, zapcore.DebugLevel, skipped[0].Level)
			expected := "pool_full"
			if fail {
				expected = "retry_backoff"
			}
			require.Equal(t, expected, skipped[0].ContextMap()["reason"])
		})
	}
}

func TestOpenAISessionPoolDiagnosticReservationReasons(t *testing.T) {
	for _, kind := range []string{"pool_empty", "no_eligible_accounts", "expired", "leased", "already_committed", "model_not_configured", "transport_mismatch", "account_changed", "invalid_latency", "candidate_reserved"} {
		t.Run(kind, func(t *testing.T) {
			svc, _ := newOpenAIWSLatencyTestService(t)
			a := preparedTestAccount(11, 1)
			svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: []Account{a}}
			if kind != "pool_empty" {
				seedPreparedPool(t, svc, a, 1)
				svc.updateOpenAIWSLatencyCandidatePool(context.Background(), openAISessionPoolKey, func(pool *openAIWSLatencyCandidatePool) bool {
					c := &pool.Candidates[0]
					switch kind {
					case "expired":
						c.ExpiresAt = time.Now().Add(-time.Second).UnixMilli()
					case "leased":
						c.LeaseID, c.LeaseUntil = "private-lease", time.Now().Add(time.Minute).UnixMilli()
					case "already_committed":
						c.Committed = true
					case "model_not_configured":
						c.UpstreamModel = "other-model"
					case "transport_mismatch":
						c.Transport = OpenAIUpstreamTransportResponsesWebsocketV2
					case "account_changed":
						c.AccountFingerprint = "private-fingerprint"
					case "invalid_latency":
						c.FirstTokenMS = 10000
					}
					return true
				})
			}
			group := int64(1)
			if kind == "no_eligible_accounts" {
				group = 2
			}
			session := preparedHTTPSession(svc, group, "private-conversation")
			ctx, logs := poolDiagnosticTestContext()
			_, _, ok := session.reservePreparedCandidate(ctx, "gpt-5.1")
			require.Equal(t, kind == "candidate_reserved", ok)
			events := logs.FilterMessage("openai.session_pool_reservation").All()
			require.Len(t, events, 1)
			fields := events[0].ContextMap()
			require.EqualValues(t, group, fields["group_id"])
			switch kind {
			case "pool_empty", "no_eligible_accounts", "candidate_reserved":
				require.Equal(t, kind, fields["reason"])
			default:
				require.Equal(t, "no_matching_candidate", fields["reason"])
				require.Equal(t, map[string]int{kind: 1}, fields["rejections"])
			}
			encoded, err := json.Marshal(fields)
			require.NoError(t, err)
			for _, secret := range []string{"private-conversation", "private-lease", "private-fingerprint", "prepared-11-0", "test-only"} {
				require.NotContains(t, string(encoded), secret)
			}
		})
	}
}

func TestOpenAISessionPoolDiagnosticAccountRejections(t *testing.T) {
	tests := []struct {
		reason string
		change func(*Account)
	}{
		{"session_pool_disabled", func(a *Account) { a.Extra[OpenAISessionPoolEnabledExtraKey] = false }},
		{"account_not_schedulable", func(a *Account) { a.Schedulable = false }},
		{"group_not_authorized", func(a *Account) { a.GroupIDs = []int64{2} }},
		{"model_not_supported", func(a *Account) { a.Credentials["model_mapping"] = map[string]any{"other-model": "other-model"} }},
	}
	for _, tt := range tests {
		for _, stage := range []string{"snapshot", "fresh"} {
			t.Run(stage+"_"+tt.reason, func(t *testing.T) {
				svc, _ := newOpenAIWSLatencyTestService(t)
				snapshot, fresh := preparedTestAccount(11, 1), preparedTestAccount(11, 1)
				seedPreparedPool(t, svc, fresh, 1)
				if stage == "snapshot" {
					tt.change(&snapshot)
				} else {
					tt.change(&fresh)
				}
				svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: []Account{fresh}}
				svc.schedulerSnapshot = &SchedulerSnapshotService{cache: &openAISnapshotCacheStub{snapshotAccounts: []*Account{&snapshot}}}
				session := preparedHTTPSession(svc, 1, "private-conversation")
				ctx, logs := poolDiagnosticTestContext()
				_, _, ok := session.reservePreparedCandidate(ctx, "gpt-5.1")
				require.False(t, ok)
				events := logs.FilterMessage("openai.session_pool_reservation").All()
				require.Len(t, events, 1)
				fields := events[0].ContextMap()
				require.Equal(t, "no_eligible_accounts", fields["reason"])
				require.EqualValues(t, 1, fields["schedulable_account_count"])
				require.EqualValues(t, 0, fields["eligible_account_count"])
				require.Equal(t, map[string]int{stage + "_" + tt.reason: 1}, fields["account_rejections"])
				pool := svc.getOpenAIWSLatencyCandidatePool(ctx, openAISessionPoolKey)
				require.Len(t, pool.Candidates, 1)
				require.Empty(t, pool.Candidates[0].LeaseID, "过滤失败不能占用共享候选")
				encoded, err := json.Marshal(fields)
				require.NoError(t, err)
				require.NotContains(t, string(encoded), "private-conversation")
				require.NotContains(t, string(encoded), "test-only")
			})
		}
	}
}

func TestOpenAISessionPoolDiagnosticProbeReasonsAndPrivacy(t *testing.T) {
	tests := []struct {
		name   string
		result openAIWSLatencyProbeResult
		err    error
	}{
		{"qualified", openAIWSLatencyProbeResult{ok: true, ms: 9999, upstreamSessionID: "private-session", upstreamModel: "model"}, nil},
		// 已收到合格首字、随后超时是现有规则允许的成功探测，诊断不能改变它。
		{"qualified", openAIWSLatencyProbeResult{ok: true, ms: 99, upstreamSessionID: "private-session", upstreamModel: "model"}, context.DeadlineExceeded},
		{"first_token_too_slow", openAIWSLatencyProbeResult{ok: true, ms: 10000}, nil},
		{"upstream_session_missing", openAIWSLatencyProbeResult{ok: true, ms: 1}, nil},
		{"upstream_model_mismatch", openAIWSLatencyProbeResult{ok: true, ms: 1, upstreamSessionID: "private-session", upstreamModel: "other"}, nil},
		{"probe_timeout", openAIWSLatencyProbeResult{timedOut: true}, nil},
		{"probe_timeout", openAIWSLatencyProbeResult{}, fmt.Errorf("private-url: %w", context.DeadlineExceeded)},
		{"probe_canceled", openAIWSLatencyProbeResult{}, context.Canceled},
		{"probe_http_error", openAIWSLatencyProbeResult{responseStatus: 529}, errors.New("private-error-body")},
		{"upstream_request_failed", openAIWSLatencyProbeResult{}, errors.New("sk-private-key private-response")},
		// 开启降智后也应保留真实传输失败原因，不能被“答案不完整”覆盖。
		{"probe_timeout", openAIWSLatencyProbeResult{qualityJudgment: &QualityJudgment{Verdict: "unknown", Reason: "answer_incomplete"}}, context.DeadlineExceeded},
		{"probe_canceled", openAIWSLatencyProbeResult{qualityJudgment: &QualityJudgment{Verdict: "unknown", Reason: "answer_incomplete"}}, context.Canceled},
		{"probe_http_error", openAIWSLatencyProbeResult{responseStatus: 503, qualityJudgment: &QualityJudgment{Verdict: "unknown", Reason: "answer_incomplete"}}, errors.New("private-error-body")},
		{"upstream_request_failed", openAIWSLatencyProbeResult{qualityJudgment: &QualityJudgment{Verdict: "unknown", Reason: "answer_incomplete"}}, errors.New("private-response")},
		{"quality_inconclusive", openAIWSLatencyProbeResult{qualityJudgment: &QualityJudgment{Verdict: "unknown", Reason: "judge_no_available_account"}}, nil},
		{"quality_incorrect", openAIWSLatencyProbeResult{qualityJudgment: &QualityJudgment{Verdict: "incorrect", Reason: "答案不同"}}, nil},
		{"no_successful_first_token", openAIWSLatencyProbeResult{}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reason := openAISessionPoolProbeReason(tt.result, tt.err, "model")
			require.Equal(t, tt.name, reason)
			ctx, logs := poolDiagnosticTestContext()
			writeOpenAISessionPoolDiagnostic(ctx, logger.LevelDebug, "openai.session_pool_probe_completed", reason, openAISessionPoolErrorFields(tt.err)...)
			fields := logs.All()[0].ContextMap()
			encoded, err := json.Marshal(fields)
			require.NoError(t, err)
			for _, secret := range []string{"private-url", "private-session", "private-error-body", "sk-private-key", "private-response"} {
				require.NotContains(t, string(encoded), secret)
			}
		})
	}
}

func TestOpenAISessionPoolDiagnosticDisabledHasNoSideEffects(t *testing.T) {
	svc, cache := newOpenAIWSLatencyTestService(t)
	a := preparedTestAccount(11, 1)
	svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: []Account{a}}
	session := preparedHTTPSession(svc, 1, "disabled")
	gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{expiresAt: time.Now().Add(time.Hour).UnixNano()})
	ctx, logs := poolDiagnosticTestContext()
	svc.refillOpenAISessionPool(ctx)
	result := svc.probeOpenAISessionPoolRoute(ctx, openAISessionPoolRoute{account: &a})
	_, _, ok := session.reservePreparedCandidate(ctx, "gpt-5.1")
	require.False(t, result.ok)
	require.False(t, ok)
	require.Empty(t, logs.All())
	require.Empty(t, cache.states)
}

func TestOpenAISessionPoolDiagnosticAbandonMatchesOwnership(t *testing.T) {
	for _, committed := range []bool{false, true} {
		t.Run(fmt.Sprint(committed), func(t *testing.T) {
			svc, _ := newOpenAIWSLatencyTestService(t)
			a := preparedTestAccount(11, 1)
			svc.accountRepo = schedulerTestOpenAIAccountRepo{accounts: []Account{a}}
			seedPreparedPool(t, svc, a, 1)
			ctx, logs := poolDiagnosticTestContext()
			session := preparedHTTPSession(svc, 1, "abandon-test")
			require.True(t, session.assignPreparedCandidate(ctx, "gpt-5.1", 0, true))
			state := session.state(ctx)
			if committed {
				require.True(t, svc.commitOpenAIWSLatencyCandidate(ctx, openAISessionPoolKey, state.CandidateLeaseID, state.TargetResultID, state.TargetID))
				_, updated := svc.updateOpenAIWSLatencyState(ctx, session.key("gpt-5.1"), func(current *openAIWSLatencyState) bool {
					current.CandidateCommitted = true
					return true
				})
				require.True(t, updated)
				state = session.state(ctx)
			}
			session.abandonPreparedCandidate(ctx, state, "target_excluded")
			events := logs.FilterMessage("openai.session_pool_candidate_abandoned").All()
			require.Len(t, events, 1)
			fields := events[0].ContextMap()
			require.Equal(t, "target_excluded", fields["reason"])
			require.Equal(t, committed, fields["candidate_committed"])
			require.Equal(t, state.TargetResultID, fields["target_result_id"])
			require.Zero(t, session.state(ctx).TargetID)
			pool := svc.getOpenAIWSLatencyCandidatePool(ctx, openAISessionPoolKey)
			if committed {
				require.Empty(t, pool.Candidates)
			} else {
				require.Len(t, pool.Candidates, 1)
				require.Empty(t, pool.Candidates[0].LeaseID)
			}
			// 重复清理不产生第二条成功放弃记录。
			session.abandonPreparedCandidate(ctx, state, "target_excluded")
			require.Len(t, logs.FilterMessage("openai.session_pool_candidate_abandoned").All(), 1)
		})
	}
}

package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
)

type probeDeadlineTestStep struct {
	delay time.Duration
	data  string
}

func TestOpenAISessionPoolGlobalFirstTokenThreshold(t *testing.T) {
	for _, seconds := range []int{3, 20} {
		for _, quality := range []bool{false, true} {
			for _, noToken := range []bool{false, true} {
				t.Run(fmt.Sprintf("seconds=%d/quality=%t/timeout=%t", seconds, quality, noToken), func(t *testing.T) {
					synctest.Test(t, func(t *testing.T) {
						s, a, _, judgeCalls := setupConversationQualityTest(t, "correct")
						if !quality {
							delete(a.Extra, OpenAISessionPoolQualityExtraKey)
						}
						s.accountRepo = schedulerTestOpenAIAccountRepo{accounts: []Account{a}}
						settings := *gatewayForwardingCache.Load().(*cachedGatewayForwardingSettings)
						settings.sessionPoolFirstTokenTimeoutSeconds = seconds
						gatewayForwardingCache.Store(&settings)
						first := time.Duration(seconds-2) * time.Second
						u := &probeDeadlineTestUpstream{steps: []probeDeadlineTestStep{
							{first, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"21\"}\n\n"},
							{time.Second, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_custom_threshold\",\"status\":\"completed\",\"model\":\"gpt-5.1\"}}\n\n"},
						}}
						if noToken {
							u.headerDelay = time.Duration(seconds+5) * time.Second
						}
						s.httpUpstream = u
						ctx, logs := poolDiagnosticTestContext()
						started := time.Now()
						s.refillOpenAISessionPool(ctx)
						pool := s.getOpenAIWSLatencyCandidatePool(ctx, openAISessionPoolKey)
						fields := logs.FilterMessage("openai.session_pool_refill_completed").All()[0].ContextMap()
						if noToken {
							require.Equal(t, time.Duration(5*seconds)*time.Second, time.Since(started))
							require.Empty(t, pool.Candidates)
							require.Equal(t, map[string]int{"first_token_too_slow": 5}, fields["probe_outcomes"])
							require.Zero(t, judgeCalls.Load())
						} else {
							require.Equal(t, first+time.Second, time.Since(started))
							require.Len(t, pool.Candidates, 1)
							require.Equal(t, int(first.Milliseconds()), pool.Candidates[0].FirstTokenMS)
							require.Equal(t, map[string]int{"qualified": 1}, fields["probe_outcomes"])
							_, _, claimed := preparedHTTPSession(s, 1, "custom-threshold-user").reservePreparedCandidate(ctx, "gpt-5.1")
							require.True(t, claimed, "取消、入池和领取都必须采用相同门槛")
							if quality {
								require.EqualValues(t, 1, judgeCalls.Load())
							}
						}
					})
				})
			}
		}
	}
}

type probeDeadlineTestUpstream struct {
	HTTPUpstream
	headerDelay time.Duration
	steps       []probeDeadlineTestStep
	requestCtx  context.Context
	closed      bool
}

func (u *probeDeadlineTestUpstream) Do(r *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.requestCtx = r.Context()
	select {
	case <-time.After(u.headerDelay):
	case <-r.Context().Done():
		return nil, r.Context().Err()
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: &probeDeadlineTestBody{upstream: u, ctx: r.Context()}, Request: r}, nil
}

type probeDeadlineTestBody struct {
	upstream *probeDeadlineTestUpstream
	ctx      context.Context
	index    int
	reader   *strings.Reader
}

func (b *probeDeadlineTestBody) Read(p []byte) (int, error) {
	if b.reader != nil && b.reader.Len() > 0 {
		return b.reader.Read(p)
	}
	if b.index == len(b.upstream.steps) {
		return 0, io.EOF
	}
	step := b.upstream.steps[b.index]
	b.index++
	select {
	case <-time.After(step.delay):
	case <-b.ctx.Done():
		return 0, b.ctx.Err()
	}
	b.reader = strings.NewReader(step.data)
	return b.reader.Read(p)
}

func (b *probeDeadlineTestBody) Close() error {
	b.upstream.closed = true
	return nil
}

func TestOpenAIWSLatencyProbeFirstTokenDeadline(t *testing.T) {
	const metadata = "data: {\"type\":\"response.created\"}\n\n"
	const reasoning = "data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"reasoning\"}}\n\n"
	const answer = "data: {\"type\":\"response.output_text.delta\",\"delta\":\"21\"}\n\n"
	const completed = "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_deadline\",\"status\":\"completed\",\"model\":\"gpt-5.1\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n"
	for _, quality := range []bool{false, true} {
		name := "latency"
		if quality {
			name = "quality"
		}
		for _, tc := range []struct {
			name        string
			headerDelay time.Duration
			mode        string
			steps       []probeDeadlineTestStep
			wantOK      bool
			wantMS      int
			elapsed     time.Duration
		}{
			{"waiting_headers", 30 * time.Second, OpenAITTFTModeSemantic, nil, false, -1, 10 * time.Second},
			{"metadata_is_not_first_token", 0, OpenAITTFTModeSemantic, []probeDeadlineTestStep{{0, metadata}, {30 * time.Second, answer + completed}}, false, -1, 10 * time.Second},
			{"reasoning_is_not_visible_output", 0, OpenAITTFTModeVisible, []probeDeadlineTestStep{{time.Second, reasoning}, {30 * time.Second, answer + completed}}, false, -1, 10 * time.Second},
			{"first_token_at_ten_seconds_is_rejected", 0, OpenAITTFTModeVisible, []probeDeadlineTestStep{{10 * time.Second, answer + completed}}, false, -1, 10 * time.Second},
			{"early_first_token_allows_later_completion", 0, OpenAITTFTModeVisible, []probeDeadlineTestStep{{9999 * time.Millisecond, answer}, {3 * time.Second, completed}}, true, 9999, 12999 * time.Millisecond},
			{"semantic_first_token_stops_deadline", 0, OpenAITTFTModeSemantic, []probeDeadlineTestStep{{time.Second, answer}, {11 * time.Second, completed}}, true, 1000, 12 * time.Second},
		} {
			t.Run(name+"/"+tc.name, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					s, a, _, calls := setupConversationQualityTest(t, "correct")
					if !quality {
						delete(a.Extra, OpenAISessionPoolQualityExtraKey)
					}
					settings := *gatewayForwardingCache.Load().(*cachedGatewayForwardingSettings)
					settings.openAITTFTMode = tc.mode
					gatewayForwardingCache.Store(&settings)
					u := &probeDeadlineTestUpstream{headerDelay: tc.headerDelay, steps: tc.steps}
					s.httpUpstream = u
					original := s.conversationQualityJudge.request
					s.conversationQualityJudge.request = func(ctx context.Context, id int64, model, prompt string) (string, error) {
						// 判题开始时必须已解除首字计时，否则会误杀正常答题。
						time.Sleep(5 * time.Second)
						return original(ctx, id, model, prompt)
					}
					ctx := context.WithValue(context.Background(), openAIPreparedProbeContextKey{}, true)
					ctx = context.WithValue(ctx, openAIWSLatencyProbeSessionIDContextKey{}, "deadline-probe")
					releases := 0
					started := time.Now()
					result, err := s.forwardOpenAIWSLatencyProbeWithRelease(ctx, &a, "gpt-5.1", OpenAIUpstreamTransportHTTPSSE, func() { releases++ })
					elapsed := tc.elapsed
					if quality && tc.wantOK {
						elapsed += 5 * time.Second
						require.EqualValues(t, 1, calls.Load())
					} else {
						require.Zero(t, calls.Load(), "首字超时不能继续判题")
					}
					require.Equal(t, elapsed, time.Since(started))
					require.Equal(t, tc.wantOK, result.ok)
					require.Equal(t, tc.wantMS, result.ms)
					require.Equal(t, 1, releases, "取消答题也必须及时释放账号名额")
					if tc.wantOK {
						require.NoError(t, err)
					} else {
						require.ErrorIs(t, err, context.DeadlineExceeded)
						require.True(t, result.timedOut)
						require.NotNil(t, u.requestCtx.Err(), "必须取消实际上游请求")
						require.Equal(t, "first_token_too_slow", openAISessionPoolProbeReason(result, err, "gpt-5.1"))
					}
					if tc.headerDelay == 0 {
						require.True(t, u.closed, "已建立的上游响应必须关闭")
					}
				})
			})
		}
	}
}

func TestOpenAIWSLatencyProbeWebSocketFirstTokenDeadline(t *testing.T) {
	for _, quality := range []bool{false, true} {
		name := "latency"
		if quality {
			name = "quality"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s, a, _, calls := setupConversationQualityTest(t, "correct")
				if !quality {
					delete(a.Extra, OpenAISessionPoolQualityExtraKey)
				}
				s.cfg.Gateway.OpenAIWS.Enabled = true
				s.cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
				s.cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
				s.cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 60
				a.Extra["responses_websockets_v2_enabled"] = true
				upstream := &openAIWSCancelSafeConn{openAIWSCaptureConn: &openAIWSCaptureConn{
					events: [][]byte{
						[]byte(`{"type":"response.created","response":{"id":"resp_slow"}}`),
						[]byte(`{"type":"response.output_text.delta","delta":"21"}`),
						[]byte(`{"type":"response.completed","response":{"id":"resp_slow","status":"completed","model":"gpt-5.1"}}`),
					},
					readDelays: []time.Duration{0, 30 * time.Second, 0},
				}}
				pool := newOpenAIWSConnPool(s.cfg)
				pool.setClientDialerForTest(&openAIWSClientConnCancelDialer{conn: upstream})
				defer pool.Close()
				s.openaiWSPool, s.openaiWSResolver, s.toolCorrector = pool, NewOpenAIWSProtocolResolver(s.cfg), NewCodexToolCorrector()
				ctx := context.WithValue(context.Background(), openAIPreparedProbeContextKey{}, true)
				ctx = context.WithValue(ctx, openAIWSLatencyProbeSessionIDContextKey{}, "deadline-ws-probe")
				releases := 0
				started := time.Now()
				result, err := s.forwardOpenAIWSLatencyProbeWithRelease(ctx, &a, "gpt-5.1", OpenAIUpstreamTransportResponsesWebsocketV2Ingress, func() { releases++ })
				require.Equal(t, 10*time.Second, time.Since(started), "长连接首字超时后不能继续排空响应")
				require.ErrorIs(t, err, errOpenAIWSLatencyFirstTokenTimeout)
				require.False(t, result.ok)
				require.Equal(t, -1, result.ms)
				require.Zero(t, calls.Load())
				require.Equal(t, 1, releases)
				upstream.mu.Lock()
				defer upstream.mu.Unlock()
				require.True(t, upstream.closed, "超时的上游连接必须关闭")
			})
		})
	}
}

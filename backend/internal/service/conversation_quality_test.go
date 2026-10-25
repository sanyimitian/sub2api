package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func conversationQualityTestConfig() *ConversationQualityConfig {
	return &ConversationQualityConfig{Enabled: true, QuestionKind: "candy", Prompt: ConversationQualityCandyPrompt,
		ExpectedAnswer: "21", ReasoningEffort: "high", Judge: &QualityJudgeConfig{GroupID: 9, ModelID: "judge", Prompt: "比较参考答案与实际回答"}}
}

func TestConversationQualityConfigDependencyAndCandidateInvalidation(t *testing.T) {
	a := preparedTestAccount(11, 1)
	previous := openAISessionPoolAccountFingerprint(&a)
	config := conversationQualityTestConfig()
	a.Extra[OpenAISessionPoolQualityExtraKey] = config
	require.NoError(t, ValidateOpenAISessionPoolExtra(a.Platform, a.Type, a.Extra))
	require.NotEqual(t, previous, openAISessionPoolAccountFingerprint(&a))
	merged := mergeOpenAISessionPoolExtra(a.Extra, map[string]any{OpenAISessionPoolEnabledExtraKey: false})
	require.Error(t, ValidateOpenAISessionPoolExtra(a.Platform, a.Type, merged))
	require.True(t, a.OpenAISessionPoolEnabled(), "校验局部更新不能改变原账号")
	config.Enabled = false
	require.Equal(t, previous, openAISessionPoolAccountFingerprint(&a), "关闭质量探测保留旧低延迟候选兼容性")
	config.Enabled = true
	config.QuestionKind = QualityRandomQuestionKind
	config.Prompt, config.ExpectedAnswer = "", ""
	require.NoError(t, ValidateOpenAISessionPoolExtra(a.Platform, a.Type, a.Extra))
	config.Judge = nil
	require.Error(t, ValidateOpenAISessionPoolExtra(a.Platform, a.Type, a.Extra))
	a.Extra[OpenAISessionPoolQualityExtraKey] = map[string]any{"enabled": "true"}
	require.Error(t, ValidateOpenAISessionPoolExtra(a.Platform, a.Type, a.Extra))
}

type conversationJudgeTestGroups struct{ GroupRepository }

func (conversationJudgeTestGroups) GetByID(context.Context, int64) (*Group, error) {
	return &Group{ID: 9, Status: StatusActive}, nil
}

type conversationJudgeTestSlots struct{}

func (conversationJudgeTestSlots) AcquireAccountSlot(context.Context, int64, int) (*AcquireResult, error) {
	return &AcquireResult{Acquired: true, ReleaseFunc: func() {}}, nil
}

type conversationQualityTestUpstream struct {
	HTTPUpstream
	mu       sync.Mutex
	bodies   [][]byte
	sessions []string
	verdict  string
	partial  bool
}

func (u *conversationQualityTestUpstream) Do(r *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	u.mu.Lock()
	u.bodies = append(u.bodies, body)
	u.sessions = append(u.sessions, r.Header.Get("session_id"))
	u.mu.Unlock()
	text := "21"
	if u.verdict != "" {
		text = fmt.Sprintf(`{"verdict":%q,"reason":"答案比对完成"}`, u.verdict)
	}
	delta, _ := json.Marshal(map[string]any{"type": "response.output_text.delta", "delta": text})
	sse := "data: " + string(delta) + "\n\n"
	if !u.partial {
		sse += "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_test\",\"model\":\"gpt-5.1\",\"status\":\"completed\",\"usage\":{\"input_tokens\":30,\"output_tokens\":10}}}\n\n"
	}
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(sse)), Request: r}, nil
}

func setupConversationQualityTest(t *testing.T, verdict string) (*OpenAIGatewayService, Account, *conversationQualityTestUpstream, *atomic.Int32) {
	t.Helper()
	s, _ := newOpenAIWSLatencyTestService(t)
	s.cfg.Gateway.OpenAIWS.Enabled = false
	a := preparedTestAccount(11, 1)
	a.Extra[OpenAISessionPoolQualityExtraKey] = conversationQualityTestConfig()
	judge := preparedTestAccount(22, 9)
	judge.Extra = nil
	repo := schedulerTestOpenAIAccountRepo{accounts: []Account{a, judge}}
	s.accountRepo = repo
	u := &conversationQualityTestUpstream{}
	s.httpUpstream = u
	calls := &atomic.Int32{}
	s.conversationQualityJudge = &ConversationQualityJudgeService{accounts: repo, groups: conversationJudgeTestGroups{}, slots: conversationJudgeTestSlots{},
		request: func(_ context.Context, accountID int64, model, prompt string) (string, error) {
			calls.Add(1)
			if accountID == a.ID || model != "judge" || strings.Contains(prompt, ConversationQualityCandyPrompt) || !strings.Contains(prompt, "reference_answer") || !strings.Contains(prompt, "candidate_answer") {
				return "", errors.New("判题请求不符合参考规则")
			}
			return fmt.Sprintf(`{"verdict":%q,"reason":"答案比对完成"}`, verdict), nil
		}}
	gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{sessionPoolEnabled: true, sessionPoolSize: 1,
		latencySwitch: OpenAIWSLatencySwitchSettings{ProbeCount: 2, ProbeReuseSeconds: 600}, expiresAt: time.Now().Add(time.Hour).UnixNano()})
	return s, a, u, calls
}

func TestConversationQualityPoolRequiresCompleteCorrectAnswerAndRetainsSession(t *testing.T) {
	for _, verdict := range []string{"correct", "incorrect", "unknown", "invalid"} {
		t.Run(verdict, func(t *testing.T) {
			s, a, upstream, calls := setupConversationQualityTest(t, verdict)
			s.refillOpenAISessionPool(context.Background())
			pool := s.getOpenAIWSLatencyCandidatePool(context.Background(), openAISessionPoolKey)
			if verdict != "correct" {
				require.Empty(t, pool.Candidates)
				require.EqualValues(t, openAIWSLatencyMaxProbeRounds, calls.Load(), "答错或未知继续补池，最多五轮")
				require.Equal(t, StatusActive, a.Status)
				require.True(t, a.Schedulable)
				require.Equal(t, []int64{1}, a.GroupIDs)
				return
			}
			require.Len(t, pool.Candidates, 1)
			require.True(t, pool.Candidates[0].QualityPassed)
			require.EqualValues(t, 1, calls.Load())
			require.Len(t, upstream.bodies, 1)
			body := upstream.bodies[0]
			require.Contains(t, string(body), "在一个黑色的袋子里")
			require.NotContains(t, string(body), "Reply with the single word OK")
			require.False(t, gjson.GetBytes(body, "max_output_tokens").Exists())
			require.Equal(t, "high", gjson.GetBytes(body, "reasoning.effort").String())
			require.Equal(t, upstream.sessions[0], pool.Candidates[0].UpstreamSessionID)
			session := preparedHTTPSession(s, 1, "quality-user")
			_, err := session.PrepareHTTP(context.Background(), []byte(`{"input":"hello"}`))
			require.NoError(t, err)
			selection, _, err := session.SelectTarget(context.Background(), map[int64]struct{}{}, OpenAIUpstreamTransportHTTPSSE)
			require.NoError(t, err)
			require.NotNil(t, selection)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			require.NoError(t, session.ApplyUpstreamSessionIdentity(context.Background(), c, selection.Account, "gpt-5.1"))
			require.Equal(t, upstream.sessions[0], OpenAIWSLatencyUpstreamSessionID(c))
		})
	}
}

func TestConversationQualityIncompleteAnswerCannotEnterPool(t *testing.T) {
	s, a, u, calls := setupConversationQualityTest(t, "correct")
	u.partial = true
	ctx := context.WithValue(context.Background(), openAIPreparedProbeContextKey{}, true)
	ctx = context.WithValue(ctx, openAIWSLatencyProbeSessionIDContextKey{}, "quality-incomplete")
	result, _ := s.forwardOpenAIWSLatencyProbe(ctx, &a, "gpt-5.1", OpenAIUpstreamTransportHTTPSSE)
	require.False(t, result.ok)
	require.Equal(t, "unknown", result.qualityJudgment.Verdict)
	require.Zero(t, calls.Load(), "未完整生成的答案不能交给判题器判为通过")
}

func TestConversationQualityConfigurationChangeRejectsOldPool(t *testing.T) {
	s, a, _, _ := setupConversationQualityTest(t, "correct")
	seedPreparedPool(t, s, a, 1) // 相同配置指纹也不能代替真实的质量通过标记。
	session := preparedHTTPSession(s, 1, "no-old-candidate")
	_, _, reserved := session.reservePreparedCandidate(context.Background(), "gpt-5.1")
	require.False(t, reserved)
	s.refillOpenAISessionPool(context.Background())
	pool := s.getOpenAIWSLatencyCandidatePool(context.Background(), openAISessionPoolKey)
	require.Len(t, pool.Candidates, 1)
	require.True(t, pool.Candidates[0].QualityPassed)
	config := conversationQualityTestConfig()
	config.ExpectedAnswer = "custom-answer"
	a.Extra[OpenAISessionPoolQualityExtraKey] = config
	s.accountRepo = schedulerTestOpenAIAccountRepo{accounts: []Account{a}}
	_, _, reserved = session.reservePreparedCandidate(context.Background(), "gpt-5.1")
	require.False(t, reserved, "判分规则变化后旧结果不得继续领取")
}

func TestConversationQualityJudgeRealGatewayTransport(t *testing.T) {
	s, a, _, _ := setupConversationQualityTest(t, "correct")
	u := &conversationQualityTestUpstream{verdict: "correct"}
	s.httpUpstream = u
	repo := schedulerTestOpenAIAccountRepo{accounts: []Account{preparedTestAccount(22, 9)}}
	judge := NewConversationQualityJudgeService(repo, conversationJudgeTestGroups{}, nil, s, nil, nil, nil)
	judge.slots = conversationJudgeTestSlots{}
	result := judge.Judge(context.Background(), a.ID, conversationQualityTestConfig(), "21", "21")
	require.Equal(t, "correct", result.Verdict, result.Reason)
	require.EqualValues(t, 22, result.AccountID)
	require.Len(t, u.bodies, 1)
	require.Contains(t, string(u.bodies[0]), "reference_answer")
	require.NotContains(t, string(u.bodies[0]), ConversationQualityCandyPrompt)
}

func TestConversationQualityCaptureDoesNotGradeThinkingOrTruncatedOutput(t *testing.T) {
	writer := &openAIWSLatencyProbeWriter{header: make(http.Header), capture: true, firstMS: -1, started: time.Now()}
	_, err := writer.Write([]byte("data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"21\"}\n\n"))
	require.NoError(t, err)
	require.Empty(t, writer.output.String())
	_, err = writer.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"21\"}\n\n"))
	require.NoError(t, err)
	require.Equal(t, "21", writer.output.String())
	_, _ = writer.Write([]byte("data: {\"type\":\"response.incomplete\"}\n\n"))
	require.True(t, writer.failed)
	_, _ = writer.Write([]byte("data: " + strings.Repeat("a", 70000) + "\n\n"))
	require.True(t, writer.overflow)
}

func TestConversationQualityRandomProbeUsesGeneratedQuestion(t *testing.T) {
	s, a, u, calls := setupConversationQualityTest(t, "correct")
	cfg := conversationQualityTestConfig()
	cfg.QuestionKind = QualityRandomQuestionKind
	cfg.Prompt, cfg.ExpectedAnswer = "stale question", "stale answer"
	a.Extra[OpenAISessionPoolQualityExtraKey] = cfg
	ctx := context.WithValue(context.Background(), openAIPreparedProbeContextKey{}, true)
	ctx = context.WithValue(ctx, openAIWSLatencyProbeSessionIDContextKey{}, "random-session")
	result, err := s.forwardOpenAIWSLatencyProbe(ctx, &a, "gpt-5.1", OpenAIUpstreamTransportHTTPSSE)
	require.NoError(t, err)
	require.True(t, result.ok)
	require.Equal(t, "random-session", result.upstreamSessionID)
	require.NotNil(t, result.qualityQuestion.Metadata)
	require.True(t, json.Valid([]byte(result.qualityQuestion.ExpectedAnswer)))
	require.Contains(t, gjson.GetBytes(u.bodies[0], "input.0.content").String(), result.qualityQuestion.Prompt)
	require.NotContains(t, string(u.bodies[0]), "stale question")
	require.EqualValues(t, 1, calls.Load())
}

func TestConversationQualityJudgeRejectsAmbiguousOutput(t *testing.T) {
	for _, output := range []string{
		`{"verdict":"correct"}`, `{"verdict":true,"reason":"ok"}`,
		`{"verdict":"correct","verdict":"incorrect","reason":"contradiction"}`,
		`{"verdict":"incorrect","reason":"no"} trailing`, "```json\n{}\n```",
		`{"verdict":"correct","reason":"ok","other":true}`, strings.Repeat("x", 8001),
	} {
		_, err := parseQualityJudgment(output)
		require.Error(t, err)
	}
}

func TestConversationQualityJudgeSkipsSelfAndFallsBackAfterFailure(t *testing.T) {
	s, a, _, _ := setupConversationQualityTest(t, "correct")
	repo := schedulerTestOpenAIAccountRepo{accounts: []Account{a, preparedTestAccount(22, 9), preparedTestAccount(33, 9)}}
	s.conversationQualityJudge.accounts = repo
	var called []int64
	s.conversationQualityJudge.request = func(_ context.Context, id int64, _, _ string) (string, error) {
		called = append(called, id)
		if id == 22 {
			return "", errors.New("unavailable")
		}
		return `{"verdict":"correct","reason":"等价"}`, nil
	}
	result := s.conversationQualityJudge.Judge(context.Background(), a.ID, conversationQualityTestConfig(), "21", "21个")
	require.Equal(t, "correct", result.Verdict)
	require.EqualValues(t, 33, result.AccountID)
	require.Equal(t, []int64{22, 33}, called)
}

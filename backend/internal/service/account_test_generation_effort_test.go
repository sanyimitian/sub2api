//go:build unit

package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestRunGenerationBackgroundExplicitReasoningEffort(t *testing.T) {
	for _, responses := range []bool{true, false} {
		for _, effort := range []string{"", "none", "minimal", "low", "medium", "high", "xhigh"} {
			t.Run(effort+map[bool]string{true: "/responses", false: "/chat"}[responses], func(t *testing.T) {
				account := generationTestAccount(PlatformOpenAI)
				account.Extra = map[string]any{openai_compat.ExtraKeyResponsesSupported: responses}
				stream := "data: {\"choices\":[{\"delta\":{\"content\":\"<svg/>\"},\"finish_reason\":\"stop\"}]}\n\n"
				path := "reasoning_effort"
				if responses {
					stream = "data: {\"type\":\"response.output_text.delta\",\"delta\":\"<svg/>\"}\n\ndata: {\"type\":\"response.completed\"}\n\n"
					path = "reasoning.effort"
				}
				upstream := &generationTestUpstream{response: newJSONResponse(http.StatusOK, stream)}
				result, err := generationService(account, upstream).RunGenerationBackground(context.Background(), account.ID, "gpt-5", "Generate SVG", effort)
				require.NoError(t, err)
				require.Equal(t, "success", result.Status, result.ErrorMessage)
				require.Equal(t, effort, result.ReasoningEffort)
				if effort == "" {
					require.False(t, gjson.GetBytes(upstream.body, path).Exists())
				} else {
					require.Equal(t, effort, gjson.GetBytes(upstream.body, path).String())
				}
			})
		}
	}
}

func TestRunGenerationBackgroundUnsupportedEffortFailsBeforeUpstream(t *testing.T) {
	for _, platform := range []string{PlatformAnthropic, PlatformGemini, PlatformAntigravity} {
		account := generationTestAccount(platform)
		upstream := &generationTestUpstream{}
		result, err := generationService(account, upstream).RunGenerationBackground(context.Background(), account.ID, "text-model", "Generate SVG", "xhigh")
		require.NoError(t, err)
		require.Equal(t, "failed", result.Status)
		require.Contains(t, result.ErrorMessage, "not supported")
		require.Zero(t, upstream.calls)
	}
}

func TestAccountTestGenerationEffortLeavesOrdinaryProbesUntouched(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set(accountTestGenerationEffortKey, "high")
	payload := map[string]any{"max_tokens": 16}
	require.NoError(t, applyAccountTestGenerationPayload(c, payload, APIProtocolChatCompletions, false))
	require.Equal(t, map[string]any{"max_tokens": 16}, payload)
}

func TestAccountTestGenerationEffortOrdinaryRequestHasNoEffortOrGenerationBudget(t *testing.T) {
	for _, responses := range []bool{true, false} {
		account := generationTestAccount(PlatformOpenAI)
		account.Extra = map[string]any{openai_compat.ExtraKeyResponsesSupported: responses}
		stream := "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
		if responses {
			stream = "data: {\"type\":\"response.completed\"}\n\n"
		}
		upstream := &generationTestUpstream{response: newJSONResponse(http.StatusOK, stream)}
		svc := generationService(account, upstream)
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/test", nil)
		require.NoError(t, svc.TestAccountConnection(c, account.ID, "gpt-5", "hi", AccountTestModeDefault))
		require.Equal(t, 1, upstream.calls)
		for _, path := range []string{"reasoning", "reasoning_effort", "max_output_tokens", "max_completion_tokens", "max_tokens"} {
			require.False(t, gjson.GetBytes(upstream.body, path).Exists(), path)
		}
	}
}

type degradationEffortCaptureTester struct {
	effort string
}

func (f *degradationEffortCaptureTester) RunGenerationBackground(_ context.Context, _ int64, _ string, _ string, efforts ...string) (*DegradationTestResult, error) {
	f.effort = efforts[0]
	return &DegradationTestResult{Status: "success"}, nil
}

func TestDegradationTestRunnerPassesAndSnapshotsClaimedEffort(t *testing.T) {
	runner, plans, results := newDegradationRunnerTest(t, nil)
	plans.plan.ReasoningEffort = "xhigh"
	tester := &degradationEffortCaptureTester{}
	runner.accountTestSvc = tester
	result, err := runner.RunNow(context.Background(), 7)
	require.NoError(t, err)
	require.Equal(t, "xhigh", tester.effort)
	require.Equal(t, "xhigh", result.ReasoningEffort)
	require.Equal(t, "xhigh", results.saved.ReasoningEffort)
}

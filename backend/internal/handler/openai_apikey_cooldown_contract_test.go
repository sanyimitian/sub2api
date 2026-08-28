package handler

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenAINativeHandlersWrapEveryForwardWithAPIKeyCooldownAttempt(t *testing.T) {
	tests := []struct {
		name         string
		fileName     string
		functionName string
		forwardToken string
	}{
		{name: "responses", fileName: "openai_gateway_handler.go", functionName: "Responses", forwardToken: "h.gatewayService.Forward("},
		{name: "messages", fileName: "openai_gateway_handler.go", functionName: "Messages", forwardToken: "h.gatewayService.ForwardAsAnthropic("},
		{name: "chat_completions", fileName: "openai_chat_completions.go", functionName: "ChatCompletions", forwardToken: "h.gatewayService.ForwardAsChatCompletions("},
		{name: "images", fileName: "openai_images.go", functionName: "Images", forwardToken: "h.gatewayService.ForwardImages("},
		{name: "embeddings", fileName: "openai_embeddings.go", functionName: "Embeddings", forwardToken: "h.gatewayService.ForwardEmbeddings("},
		{name: "alpha_search", fileName: "openai_alpha_search.go", functionName: "AlphaSearch", forwardToken: "h.gatewayService.ForwardAlphaSearch("},
		{name: "grok_media", fileName: "grok_media.go", functionName: "handleGrokMedia", forwardToken: "h.gatewayService.ForwardGrokMedia("},
		{name: "grok_voice", fileName: "grok_audio.go", functionName: "GrokVoice", forwardToken: "h.gatewayService.ForwardGrokVoice("},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := stripGoComments(goFunctionSource(t, tt.fileName, tt.functionName))
			forwardIndex := strings.Index(source, tt.forwardToken)
			require.NotEqual(t, -1, forwardIndex, "missing upstream forward")

			beforeForward := source[:forwardIndex]
			beginIndex := strings.LastIndex(beforeForward, "beginHandlerAPIKeyCooldownAttempt(")
			markSentIndex := strings.LastIndex(beforeForward, "cooldownAttempt.MarkRequestSent()")
			writerIndex := strings.LastIndex(beforeForward, "installAPIKeyCooldownAttemptWriter(")
			require.NotEqual(t, -1, beginIndex, "missing final shared cooldown guard")
			require.NotEqual(t, -1, markSentIndex, "missing request-sent marker")
			require.NotEqual(t, -1, writerIndex, "missing response-start observer")
			require.Less(t, beginIndex, markSentIndex)
			require.Less(t, markSentIndex, writerIndex)

			afterForward := source[forwardIndex:]
			require.Contains(t, afterForward, "ObserveAPIKeyAttemptError(")
			require.Contains(t, afterForward, "ObserveAPIKeyAttemptSuccess(")
			failoverIndex := strings.Index(afterForward, "var failoverErr")
			require.NotEqual(t, -1, failoverIndex, "missing failover branch")
			require.Less(t, strings.Index(afterForward, "ObserveAPIKeyAttemptError("), failoverIndex,
				"cooldown failure observation must precede failover")
		})
	}
}

func TestRecordOpenAIAPIKeyCooldownGuardVetoIsBoundedAndExcludesAccounts(t *testing.T) {
	failed := make(map[int64]struct{})
	count := 0
	for index := 1; index < maxAPIKeyCooldownGuardVetoAttempts; index++ {
		require.True(t, recordOpenAIAPIKeyCooldownGuardVeto(failed, int64(index), &count))
	}
	require.False(t, recordOpenAIAPIKeyCooldownGuardVeto(failed, 999, &count))
	require.Len(t, failed, maxAPIKeyCooldownGuardVetoAttempts)
}

func TestTokenCountHandlersWrapForwardWithAPIKeyCooldownAttempt(t *testing.T) {
	tests := []struct {
		name         string
		fileName     string
		functionName string
		forwardToken string
	}{
		{name: "responses_input_tokens", fileName: "openai_gateway_count_tokens.go", functionName: "ResponsesInputTokens", forwardToken: "h.gatewayService.ForwardResponsesInputTokens("},
		{name: "openai_count_tokens", fileName: "openai_gateway_count_tokens.go", functionName: "CountTokens", forwardToken: "h.gatewayService.ForwardCountTokensAsAnthropic("},
		{name: "gateway_count_tokens", fileName: "gateway_handler.go", functionName: "CountTokens", forwardToken: "h.gatewayService.ForwardCountTokens("},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := stripGoComments(goFunctionSource(t, tt.fileName, tt.functionName))
			forwardIndex := strings.Index(source, tt.forwardToken)
			require.NotEqual(t, -1, forwardIndex)
			beforeForward := source[:forwardIndex]
			require.Contains(t, beforeForward, "beginHandlerAPIKeyCooldownAttempt(")
			require.Contains(t, beforeForward, "cooldownAttempt.MarkRequestSent()")
			require.Contains(t, beforeForward, "installAPIKeyCooldownAttemptWriter(")
			afterForward := source[forwardIndex:]
			require.Contains(t, afterForward, "ObserveAPIKeyAttemptError(")
			require.Contains(t, afterForward, "ObserveAPIKeyAttemptSuccess(")
		})
	}
}

func TestGrokRealtimeUsesAPIKeyCooldownAttemptAcrossDialAndProxy(t *testing.T) {
	source := stripGoComments(goFunctionSource(t, "grok_audio.go", "GrokRealtime"))
	dialIndex := strings.Index(source, "h.gatewayService.OpenGrokRealtime(")
	require.NotEqual(t, -1, dialIndex)
	beforeDial := source[:dialIndex]
	require.Contains(t, beforeDial, "beginHandlerAPIKeyCooldownAttempt(")
	require.Contains(t, beforeDial, "cooldownAttempt.MarkRequestSent()")
	afterDial := source[dialIndex:]
	require.Contains(t, afterDial, "ObserveAPIKeyAttemptError(")
	proxyIndex := strings.Index(afterDial, "h.gatewayService.ProxyGrokRealtimeConn(")
	require.NotEqual(t, -1, proxyIndex)
	require.Contains(t, afterDial[proxyIndex:], "ObserveAPIKeyAttemptSuccess(")
}

func TestStandaloneSearchAndCodexModelsUseAPIKeyCooldownAttempts(t *testing.T) {
	tests := []struct {
		name         string
		fileName     string
		functionName string
		forwardToken string
	}{
		{name: "grok standalone search", fileName: "gateway_web_search.go", functionName: "WebSearch", forwardToken: "h.doGrokNativeXSearch("},
		{name: "codex models manifest", fileName: "openai_codex_models_handler.go", functionName: "CodexModels", forwardToken: "h.gatewayService.FetchCodexModelsManifest("},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := stripGoComments(goFunctionSource(t, tt.fileName, tt.functionName))
			forwardIndex := strings.Index(source, tt.forwardToken)
			require.NotEqual(t, -1, forwardIndex)
			beforeForward := source[:forwardIndex]
			require.Contains(t, beforeForward, "beginHandlerAPIKeyCooldownAttempt(")
			require.Contains(t, beforeForward, "cooldownAttempt.MarkRequestSent()")
			afterForward := source[forwardIndex:]
			require.Contains(t, afterForward, "ObserveAPIKeyAttemptError(")
			require.Contains(t, afterForward, "ObserveAPIKeyAttemptSuccess(")
		})
	}
}

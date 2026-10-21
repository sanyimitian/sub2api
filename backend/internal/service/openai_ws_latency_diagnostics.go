package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

// 高频诊断保留在应用日志中，不写入运维数据库或冒充切换状态事件。
// 字段仅使用编号、数值和固定分类，禁止记录请求正文、缓存内容或原始错误文本。
func writeOpenAIWSLatencyDiagnostic(ctx context.Context, level logger.Level, reason string, fields ...zap.Field) {
	fields = append(fields, zap.String("component", "openai.latency_diagnostic"),
		zap.Bool(logger.OpsSystemLogSkipField, true), zap.String("reason", reason))
	logger.FromContext(ctx).Log(level, "openai.latency_diagnostic", fields...)
}

func (actor openAIWSLatencyActor) diagnosticFields() []zap.Field {
	return []zap.Field{
		zap.Int64("user_id", actor.userID), zap.Int64("api_key_id", actor.apiKeyID),
		zap.Int64("group_id", actor.groupID), zap.String("scope", actor.sessionID),
	}
}

func (actor *openAIWSLatencyActor) captureRequest(ctx context.Context, groupID *int64) {
	actor.groupID = derefGroupID(groupID)
	if ctx != nil {
		actor.requestID, _ = ctx.Value(ctxkey.RequestID).(string)
		actor.clientRequestID, _ = ctx.Value(ctxkey.ClientRequestID).(string)
	}
}

func logOpenAIWSLatencySessionSkipped(ctx context.Context, groupID *int64, apiKey *APIKey, model string, transport OpenAIUpstreamTransport, reason string) {
	actor := openAIWSLatencyActor{}
	actor.captureRequest(ctx, groupID)
	if apiKey != nil {
		actor.userID, actor.apiKeyID = apiKey.UserID, apiKey.ID
	}
	level := logger.LevelInfo
	if reason == "feature_disabled" {
		level = logger.LevelDebug
	}
	fields := append(actor.diagnosticFields(), zap.String("stage", "session"),
		zap.String("model", model), zap.String("transport", string(transport)))
	writeOpenAIWSLatencyDiagnostic(ctx, level, reason, fields...)
}

func logOpenAIWSLatencyCacheFailure(ctx context.Context, key, operation, reason string, attempt int, err error) {
	// 键只用于关联状态读写，不暴露可能包含会话信息的原始键。
	keyHash, _ := deriveOpenAISessionHashes(key)
	fields := []zap.Field{zap.String("stage", "state"), zap.String("state_key_hash", keyHash),
		zap.String("operation", operation), zap.Int("attempt", attempt)}
	if err != nil {
		fields = append(fields, zap.String("error_type", fmt.Sprintf("%T", err)),
			zap.Bool("timeout", errors.Is(err, context.DeadlineExceeded)),
			zap.Bool("canceled", errors.Is(err, context.Canceled)))
	}
	writeOpenAIWSLatencyDiagnostic(ctx, logger.LevelWarn, reason, fields...)
}

// 仅返回白名单分类；与原先只按输入最后一项判断用户轮次的规则一致。
func openAIWSLatencyRequestKind(payload []byte) string {
	if v := gjson.GetBytes(payload, "generate"); v.Exists() && !v.Bool() {
		return "prewarm"
	}
	input := gjson.GetBytes(payload, "input")
	if input.Type == gjson.String {
		if strings.TrimSpace(input.String()) != "" {
			return "user_message"
		}
		return "empty_input"
	}
	items := input.Array()
	if len(items) == 0 {
		return "empty_input"
	}
	last := items[len(items)-1]
	if last.Get("role").String() == "user" && (last.Get("type").String() == "" || last.Get("type").String() == "message") {
		return "user_message"
	}
	if last.Get("type").String() == "function_call_output" || last.Get("type").String() == "custom_tool_call_output" || last.Get("role").String() == "tool" {
		return "tool_output"
	}
	return "non_user_input"
}

func openAIWSLatencyRemainingMS(until, now int64) int64 {
	if until > now {
		return until - now
	}
	return 0
}

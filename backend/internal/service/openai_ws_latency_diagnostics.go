package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

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

// 路由诊断与原有状态事件分开：应用会话标识不代表已发送请求或接管成功。
func (session *OpenAIWSLatencySession) routeDiagnostic(ctx context.Context, level logger.Level, stage, reason, model string, state openAIWSLatencyState, fields ...zap.Field) {
	session.mu.Lock()
	recovering, confirmed := session.recovering, session.recoveryConfirmed
	session.mu.Unlock()
	base := append(session.actor.diagnosticFields(), zap.String("stage", stage),
		zap.String("model", model), zap.String("transport", string(session.requiredTransport)),
		zap.Int64("from_account_id", state.AccountID), zap.Int64("target_account_id", state.TargetID),
		zap.String("target_result_id", state.TargetResultID),
		zap.Int64("pending_remaining_ms", openAIWSLatencyRemainingMS(state.PendingUntil, time.Now().UnixMilli())),
		zap.Bool("reconnect_requested", state.ReconnectFrom != ""),
		zap.Bool("candidate_lease_present", state.CandidateLeaseID != ""),
		zap.Bool("upstream_session_present", state.UpstreamSessionID != ""),
		zap.Bool("recovering", recovering), zap.Bool("recovery_confirmed", confirmed))
	writeOpenAIWSLatencyDiagnostic(ctx, level, reason, append(base, fields...)...)
}

// 只汇总已读取的池快照，不增加缓存访问；预占、已承诺独占和可领取分别计数。
func openAIWSLatencyPoolDiagnosticFields(pool openAIWSLatencyCandidatePool, eligible map[int64]struct{}, models map[int64]string, now int64) []zap.Field {
	var expired, ineligible, allowed, invalid, leased, committed, available int
	var nextExpiry, nextLease int64
	for _, candidate := range pool.Candidates {
		if candidate.ExpiresAt <= now {
			expired++
			continue
		}
		if !openAIWSLatencyCandidateAllowed(candidate, eligible, models) {
			ineligible++
			continue
		}
		allowed++
		if nextExpiry == 0 || candidate.ExpiresAt < nextExpiry {
			nextExpiry = candidate.ExpiresAt
		}
		switch {
		case candidate.FirstTokenMS < 0 || candidate.FirstTokenMS >= openAIWSLatencyCandidateMaxFirstTokenMS:
			invalid++
		case candidate.Committed:
			committed++
		case candidate.LeaseID != "" && candidate.LeaseUntil > now:
			leased++
			if nextLease == 0 || candidate.LeaseUntil < nextLease {
				nextLease = candidate.LeaseUntil
			}
		default:
			available++
		}
	}
	return []zap.Field{
		zap.Int("candidate_count", len(pool.Candidates)), zap.Int("expired_candidate_count", expired),
		zap.Int("ineligible_candidate_count", ineligible), zap.Int("eligible_candidate_count", allowed),
		zap.Int("invalid_latency_candidate_count", invalid), zap.Int("leased_candidate_count", leased),
		zap.Int("committed_candidate_count", committed), zap.Int("available_candidate_count", available),
		zap.Bool("eligible_candidates_present", allowed > 0), zap.Bool("shared_probe_running", pool.ProbeUntil > now),
		zap.Int64("shared_probe_remaining_ms", openAIWSLatencyRemainingMS(pool.ProbeUntil, now)),
		zap.Int64("next_candidate_expiry_ms", openAIWSLatencyRemainingMS(nextExpiry, now)),
		zap.Int64("next_lease_expiry_ms", openAIWSLatencyRemainingMS(nextLease, now)),
	}
}

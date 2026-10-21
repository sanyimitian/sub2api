package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"go.uber.org/zap"
)

// 后台明细只进应用日志，不写切换记录；原因码来自代码中的固定分类。
func writeOpenAISessionPoolDiagnostic(ctx context.Context, level logger.Level, event, reason string, fields ...zap.Field) {
	fields = append(fields, zap.String("component", "openai.session_pool"),
		zap.Bool(logger.OpsSystemLogSkipField, true), zap.String("reason", reason))
	logger.FromContext(ctx).Log(level, event, fields...)
}

func openAISessionPoolErrorFields(err error) []zap.Field {
	if err == nil {
		return nil
	}
	// 不输出错误正文，避免上游 URL、凭据和响应内容进入日志。
	return []zap.Field{zap.String("error_type", fmt.Sprintf("%T", err)),
		zap.Bool("timeout", errors.Is(err, context.DeadlineExceeded)),
		zap.Bool("canceled", errors.Is(err, context.Canceled))}
}

func (session *OpenAIWSLatencySession) poolDiagnostic(ctx context.Context, level logger.Level, event, reason string, fields ...zap.Field) {
	base := append(session.actor.diagnosticFields(), zap.String("model", session.model),
		zap.String("transport", string(session.requiredTransport)))
	if session.actor.requestID != "" {
		base = append(base, zap.String("request_id", session.actor.requestID))
	}
	writeOpenAISessionPoolDiagnostic(ctx, level, event, reason, append(base, fields...)...)
}

func openAISessionPoolProbeReason(result openAIWSLatencyProbeResult, err error, expectedModel string) string {
	switch {
	case result.ok && result.ms >= 0:
		switch {
		case result.ms >= openAIWSLatencyCandidateMaxFirstTokenMS:
			return "first_token_too_slow"
		case result.upstreamSessionID == "":
			return "upstream_session_missing"
		case result.upstreamModel != expectedModel:
			return "upstream_model_mismatch"
		default:
			return "qualified"
		}
	case result.timedOut || errors.Is(err, context.DeadlineExceeded):
		return "probe_timeout"
	case errors.Is(err, context.Canceled):
		return "probe_canceled"
	case result.responseStatus >= 400:
		return "probe_http_error"
	case err != nil:
		return "upstream_request_failed"
	default:
		return "no_successful_first_token"
	}
}

type openAISessionPoolRefillDiagnostic struct {
	started                                                            time.Time
	id, reason                                                         string
	target, before, available, rounds, probes, published, notPublished int
	retryAfterMS                                                       int64
	outcomes                                                           map[string]int
}

func (d *openAISessionPoolRefillDiagnostic) write(ctx context.Context) {
	level := logger.LevelInfo
	if d.reason == "probe_rounds_exhausted" || d.reason == "cache_update_failed" {
		level = logger.LevelWarn
	}
	writeOpenAISessionPoolDiagnostic(ctx, level, "openai.session_pool_refill_completed", d.reason,
		zap.String("refill_id", d.id), zap.Int("target_size", d.target),
		zap.Int("available_before", d.before), zap.Int("available_last_observed", d.available),
		zap.Int("rounds", d.rounds), zap.Int("probe_count", d.probes),
		zap.Int("published_count", d.published), zap.Int("not_published_count", d.notPublished),
		zap.Any("probe_outcomes", d.outcomes), zap.Int64("retry_after_ms", d.retryAfterMS),
		zap.Int64("duration_ms", time.Since(d.started).Milliseconds()))
}

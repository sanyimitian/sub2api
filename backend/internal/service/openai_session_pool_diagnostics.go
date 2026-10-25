package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"go.uber.org/zap"
)

// 高频后台明细只进应用日志；完成的整组探测另行写入切换记录。
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
	// 质量未知只是未完成判题的结果，不能遮住答题请求本身的错误。
	// 低延迟单测允许已取得首字后超时，仍保留原有成功口径。
	if !result.ok {
		switch {
		case result.timedOut || errors.Is(err, context.DeadlineExceeded):
			return "probe_timeout"
		case errors.Is(err, context.Canceled):
			return "probe_canceled"
		case result.responseStatus >= 400:
			return "probe_http_error"
		case err != nil:
			return "upstream_request_failed"
		}
	}
	if result.qualityJudgment != nil && result.qualityJudgment.Verdict != "correct" {
		if result.qualityJudgment.Verdict == "incorrect" {
			return "quality_incorrect"
		}
		return "quality_inconclusive"
	}
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
	results                                                            []*openAISessionPoolProbeRecord
}

// 仅保存页面需要的探测元数据，绝不包含上游会话标识或凭据。
type openAISessionPoolProbeRecord struct {
	ResultID            string                  `json:"result_id,omitempty"`
	QualityProbeEnabled *bool                   `json:"quality_probe_enabled,omitempty"`
	Round               int                     `json:"round"`
	ProbeNumber         int                     `json:"probe_number"`
	AccountID           int64                   `json:"account_id"`
	AccountName         string                  `json:"account_name"`
	Model               string                  `json:"model"`
	Transport           OpenAIUpstreamTransport `json:"transport"`
	FirstTokenMS        *int                    `json:"first_token_ms"`
	Success             bool                    `json:"success"`
	Qualified           bool                    `json:"qualified"`
	Published           bool                    `json:"published"`
	Reason              string                  `json:"reason"`
	QualityJudgment     *QualityJudgment        `json:"quality_judgment,omitempty"`
}

func (d *openAISessionPoolRefillDiagnostic) write(ctx context.Context) {
	// 入池前复核可能改变最终原因，汇总必须与页面上的逐条结果一致。
	d.outcomes = make(map[string]int)
	for _, record := range d.results {
		d.outcomes[record.Reason]++
	}
	level := logger.LevelInfo
	if d.reason == "probe_rounds_exhausted" || d.reason == "cache_update_failed" {
		level = logger.LevelWarn
	}
	sort.SliceStable(d.results, func(i, j int) bool {
		if d.results[i].Round != d.results[j].Round {
			return d.results[i].Round < d.results[j].Round
		}
		return d.results[i].ProbeNumber < d.results[j].ProbeNumber
	})
	logger.FromContext(ctx).Log(level, "openai.session_pool_refill_completed",
		zap.String("component", "openai.session_pool"), zap.String("platform", PlatformOpenAI), zap.String("reason", d.reason),
		zap.String("refill_id", d.id), zap.Int("target_size", d.target),
		zap.Int("available_before", d.before), zap.Int("available_last_observed", d.available),
		zap.Int("rounds", d.rounds), zap.Int("probe_count", d.probes),
		zap.Int("published_count", d.published), zap.Int("not_published_count", d.notPublished),
		zap.Any("probe_outcomes", d.outcomes), zap.Any("probe_results", d.results), zap.Int64("retry_after_ms", d.retryAfterMS),
		zap.Int64("duration_ms", time.Since(d.started).Milliseconds()))
}

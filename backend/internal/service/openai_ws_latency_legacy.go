package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

// 总开关关闭时完整沿用原先的按需慢切换流程与状态键。
// 与备用池模式只在请求入口选择一次，不将两套触发条件混入同一流程。
type openAILegacyWSLatencySession struct{ *OpenAIWSLatencySession }

func newOpenAILegacyWSLatencySession(s *OpenAIGatewayService, groupID *int64, apiKey *APIKey, scope, sessionHash, model, upstreamModel string, capability OpenAIEndpointCapability, transport OpenAIUpstreamTransport, payload []byte, ctx context.Context) *OpenAIWSLatencySession {
	actor := openAIWSLatencyActor{userID: apiKey.UserID, apiKeyID: apiKey.ID, sessionID: scope}
	actor.captureRequest(ctx, groupID)
	if apiKey.User != nil {
		actor.username = apiKey.User.Username
		actor.userEmail = apiKey.User.Email
	}
	kind := openAIWSLatencyRequestKind(payload)
	session := &OpenAIWSLatencySession{service: s, actor: actor, groupID: groupID, scope: scope, sessionHash: sessionHash, model: model, upstreamModel: upstreamModel, connectionID: uuid.NewString(), requiredCapability: capability, requiredTransport: transport, turns: map[int]openAIWSLatencyTurn{1: {model: model, user: kind == "user_message", kind: kind}}}
	session.legacy = &openAILegacyWSLatencySession{session}
	return session
}
func (session *openAILegacyWSLatencySession) enabled(ctx context.Context) bool {
	return session != nil && session.service.settingService.GetOpenAIWSLatencySwitchSettings(ctx).Enabled
}

func (session *openAILegacyWSLatencySession) key(model string) string {
	return fmt.Sprintf("%d:%s:%s", derefGroupID(session.groupID), session.scope, hashOpenAIWSLatencyModel(model))
}

func (session *openAILegacyWSLatencySession) state(ctx context.Context) openAIWSLatencyState {
	state, _ := session.service.updateOpenAIWSLatencyState(ctx, session.key(session.model), func(*openAIWSLatencyState) bool { return false })
	return state
}

func (session *openAILegacyWSLatencySession) ApplyUpstreamSessionIdentity(ctx context.Context, c *gin.Context, account *Account, model string) error {
	if !session.enabled(ctx) || c == nil || account == nil {
		return nil
	}
	model = strings.TrimSpace(model)
	if model == "" {
		model = session.model
	}
	state, _ := session.service.updateOpenAIWSLatencyState(ctx, session.key(model), func(*openAIWSLatencyState) bool { return false })
	routeSessionID := ""
	if state.TargetID == account.ID && state.UpstreamSessionID != "" && (state.PendingUntil > time.Now().UnixMilli() || state.CandidateCommitted) {
		routeSessionID = state.UpstreamSessionID
	} else if state.AccountID == account.ID {
		routeSessionID = state.UpstreamSessionID
	}
	if routeSessionID == "" {
		if state.TargetID > 0 || state.UpstreamSessionID != "" {
			session.routeDiagnostic(ctx, logger.LevelInfo, "route", "upstream_session_not_applied", model, state, zap.Int64("account_id", account.ID))
		}
		return nil
	}
	c.Set(openAIWSLatencyUpstreamSessionContextKey, routeSessionID)
	level := logger.LevelDebug
	if state.TargetID > 0 {
		level = logger.LevelInfo
	}
	session.routeDiagnostic(ctx, level, "route", "upstream_session_applied", model, state,
		zap.Int64("account_id", account.ID), zap.String("upstream_session_hash", hashOpenAIWSLatencyModel(routeSessionID)))
	return nil
}

func (session *openAILegacyWSLatencySession) Prepare(ctx context.Context, payload []byte) error {
	if !session.enabled(ctx) {
		return nil
	}
	state := session.state(ctx)
	if state.TargetID <= 0 || state.PendingUntil <= time.Now().UnixMilli() {
		return nil
	}
	previous := strings.TrimSpace(gjson.GetBytes(payload, "previous_response_id").String())
	coverage := AnalyzeToolCallOutputContextCoverageBytes(payload)
	if previous != "" || (coverage.HasFunctionCallOutput && !coverage.ContextCoversAllCallIDs) {
		if state.ReconnectFrom != "" {
			logger.FromContext(ctx).Warn("openai.ws_latency_recovery_context_required", append(session.actor.diagnosticFields(),
				zap.Bool("has_previous_response_id", previous != ""), zap.Bool("tool_context_complete", coverage.ContextCoversAllCallIDs),
				zap.Int64("target_account_id", state.TargetID), zap.String("target_result_id", state.TargetResultID))...)
			return NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, "route switch requires complete context without previous_response_id", nil)
		}
		if openAIWSLatencyIsUserRequest(payload) {
			return session.requestReconnect(ctx, session.model)
		}
		return nil
	}
	// Prewarm is allowed to create the new connection with complete input. It is
	// never counted as a user request, and an unresolved tool result is rejected above.
	session.recovering = true
	input := gjson.GetBytes(payload, "input")
	generate := gjson.GetBytes(payload, "generate")
	session.recoveryConfirmed = (!generate.Exists() || generate.Bool()) && ((input.IsArray() && len(input.Array()) > 0) || (input.Type == gjson.String && strings.TrimSpace(input.String()) != ""))
	logger.FromContext(ctx).Info("openai.ws_latency_reconnect_received", append(session.actor.diagnosticFields(),
		zap.Int("input_items", len(input.Array())), zap.Bool("has_previous_response_id", false),
		zap.Bool("has_tool_output", coverage.HasFunctionCallOutput), zap.Bool("tool_context_complete", coverage.ContextCoversAllCallIDs),
		zap.Bool("context_confirmed", session.recoveryConfirmed), zap.String("transport", string(session.requiredTransport)),
		zap.Int64("target_account_id", state.TargetID), zap.String("target_result_id", state.TargetResultID))...)
	session.clearUpstreamAffinity(ctx)
	return nil
}

func (session *openAILegacyWSLatencySession) PrepareHTTP(ctx context.Context, payload []byte) (bool, error) {
	if !session.enabled(ctx) {
		return false, nil
	}
	state := session.state(ctx)
	if state.TargetID <= 0 || state.PendingUntil <= time.Now().UnixMilli() {
		if state.TargetID > 0 {
			session.routeDiagnostic(ctx, logger.LevelInfo, "reconnect", "pending_expired", session.model, state)
		}
		return false, nil
	}
	if !openAIWSLatencyIsUserRequest(payload) {
		session.routeDiagnostic(ctx, logger.LevelInfo, "reconnect", "reconnect_non_user_request", session.model, state,
			zap.String("request_kind", openAIWSLatencyRequestKind(payload)))
		return false, nil
	}
	if state.ReconnectFrom == "" {
		state, changed := session.service.updateOpenAIWSLatencyState(ctx, session.key(session.model), func(state *openAIWSLatencyState) bool {
			if state.TargetID <= 0 || state.PendingUntil <= time.Now().UnixMilli() || state.ReconnectFrom != "" {
				return false
			}
			state.ReconnectFrom = session.connectionID
			return true
		})
		if changed {
			session.clearUpstreamAffinity(ctx)
			session.actor.writeEvent("openai.ws_latency_switch_reconnect_requested", map[string]any{
				"from_account_id":   state.AccountID,
				"target_account_id": state.TargetID,
				"target_result_id":  state.TargetResultID,
				"model":             session.model,
				"transport":         string(OpenAIUpstreamTransportHTTPSSE),
			})
			return true, nil
		}
		if state.ReconnectFrom == "" {
			session.routeDiagnostic(ctx, logger.LevelWarn, "reconnect", "reconnect_state_not_updated", session.model, state)
			return false, nil
		}
	}

	previous := strings.TrimSpace(gjson.GetBytes(payload, "previous_response_id").String())
	coverage := AnalyzeToolCallOutputContextCoverageBytes(payload)
	if previous != "" || (coverage.HasFunctionCallOutput && !coverage.ContextCoversAllCallIDs) {
		session.routeDiagnostic(ctx, logger.LevelInfo, "reconnect", "reconnect_context_rejected", session.model, state,
			zap.Bool("has_previous_response_id", previous != ""), zap.Bool("has_tool_output", coverage.HasFunctionCallOutput),
			zap.Bool("tool_context_complete", coverage.ContextCoversAllCallIDs))
		return false, fmt.Errorf("route switch requires complete context without previous_response_id")
	}
	input := gjson.GetBytes(payload, "input")
	if !((input.IsArray() && len(input.Array()) > 0) || (input.Type == gjson.String && strings.TrimSpace(input.String()) != "")) {
		session.routeDiagnostic(ctx, logger.LevelInfo, "reconnect", "reconnect_input_empty", session.model, state)
		return false, fmt.Errorf("route switch requires complete request input")
	}
	session.recovering = true
	session.recoveryConfirmed = true
	session.clearUpstreamAffinity(ctx)
	logger.FromContext(ctx).Info("openai.ws_latency_reconnect_received", append(session.actor.diagnosticFields(),
		zap.Int("input_items", len(input.Array())), zap.Bool("has_previous_response_id", false),
		zap.Bool("has_tool_output", coverage.HasFunctionCallOutput), zap.Bool("tool_context_complete", coverage.ContextCoversAllCallIDs),
		zap.Bool("context_confirmed", true), zap.String("transport", string(OpenAIUpstreamTransportHTTPSSE)),
		zap.Int64("target_account_id", state.TargetID), zap.String("target_result_id", state.TargetResultID))...)
	return false, nil
}

func (session *openAILegacyWSLatencySession) clearUpstreamAffinity(ctx context.Context) {
	if store := session.service.getOpenAIWSStateStore(); store != nil {
		store.DeleteSessionConn(derefGroupID(session.groupID), session.scope)
		store.DeleteSessionTurnState(derefGroupID(session.groupID), session.scope)
	}
	_ = session.service.deleteStickySessionAccountID(ctx, session.groupID, session.sessionHash)
}

func (session *openAILegacyWSLatencySession) requestReconnect(ctx context.Context, model string) error {
	state, changed := session.service.updateOpenAIWSLatencyState(ctx, session.key(model), func(state *openAIWSLatencyState) bool {
		if state.TargetID <= 0 || state.PendingUntil <= time.Now().UnixMilli() {
			return false
		}
		if state.ReconnectFrom != "" {
			return false
		}
		state.ReconnectFrom = session.connectionID
		return true
	})
	if !changed && state.ReconnectFrom == "" {
		return nil
	}
	session.clearUpstreamAffinity(ctx)
	session.actor.writeEvent("openai.ws_latency_switch_reconnect_requested", map[string]any{
		"from_account_id":   state.AccountID,
		"target_account_id": state.TargetID,
		"target_result_id":  state.TargetResultID,
		"model":             model,
	})
	return NewOpenAIWSClientCloseError(OpenAIWSLatencySwitchCloseCode, "Request terminated", nil)
}

func (session *openAILegacyWSLatencySession) BeforeRequest(ctx context.Context, turn int, account *Account, payload []byte, model string) error {
	if !session.enabled(ctx) {
		return nil
	}
	if model == "" {
		model = session.model
	}
	kind := openAIWSLatencyRequestKind(payload)
	user := kind == "user_message"
	session.mu.Lock()
	if session.recovering && !session.recoveryConfirmed {
		coverage := AnalyzeToolCallOutputContextCoverageBytes(payload)
		if strings.TrimSpace(gjson.GetBytes(payload, "previous_response_id").String()) != "" || (coverage.HasFunctionCallOutput && !coverage.ContextCoversAllCallIDs) {
			session.mu.Unlock()
			return NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, "route switch requires complete context without previous_response_id", nil)
		}
		input := gjson.GetBytes(payload, "input")
		generate := gjson.GetBytes(payload, "generate")
		session.recoveryConfirmed = (!generate.Exists() || generate.Bool()) && ((input.IsArray() && len(input.Array()) > 0) || (input.Type == gjson.String && strings.TrimSpace(input.String()) != ""))
	}
	session.turns[turn] = openAIWSLatencyTurn{model: model, user: user, kind: kind}
	session.mu.Unlock()
	if !user || account == nil {
		return nil
	}
	state, _ := session.service.updateOpenAIWSLatencyState(ctx, session.key(model), func(*openAIWSLatencyState) bool { return false })
	if state.AccountID == account.ID && state.TargetID > 0 && state.PendingUntil > time.Now().UnixMilli() && !session.recovering {
		return session.requestReconnect(ctx, model)
	}
	return nil
}

func (session *openAILegacyWSLatencySession) SelectTarget(ctx context.Context, excluded map[int64]struct{}, requiredTransport OpenAIUpstreamTransport) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
	if !session.enabled(ctx) || !session.recovering {
		return nil, OpenAIAccountScheduleDecision{}, nil
	}
	state := session.state(ctx)
	if state.TargetID <= 0 || state.PendingUntil <= time.Now().UnixMilli() {
		return nil, OpenAIAccountScheduleDecision{}, nil
	}
	if state.AccountID != state.TargetID {
		excluded[state.AccountID] = struct{}{}
	}
	delete(excluded, state.TargetID)
	request := OpenAIAccountScheduleRequest{GroupID: session.groupID, Platform: PlatformOpenAI, SessionHash: session.sessionHash, StickyAccountID: state.TargetID, RequestedModel: session.upstreamModel, RequiredTransport: requiredTransport, RequiredCapability: session.requiredCapability, ExcludedIDs: excluded, DisableStickyEscape: true, PreserveStickyBinding: true, RequirePrivacySet: session.service.openAIGroupRequiresPrivacySet(ctx, session.groupID)}
	scheduler := &defaultOpenAIAccountScheduler{service: session.service}
	selection, _, err := scheduler.selectBySessionHash(ctx, request)
	decision := OpenAIAccountScheduleDecision{Layer: "latency_switch"}
	selectionReason := "target_account_unavailable"
	selectionFields := []zap.Field{zap.Bool("selection_failed", err != nil)}
	switch {
	case err != nil:
		selectionReason = "target_selection_failed"
	case selection != nil && !selection.Acquired:
		selectionReason = "target_slot_not_acquired"
	case selection != nil && selection.Account != nil:
		selectionReason = "target_account_selected"
	}
	if selection != nil && selection.Account != nil {
		selectionFields = append(selectionFields, zap.Int64("selected_account_id", selection.Account.ID),
			zap.Bool("selected_target_matches", selection.Account.ID == state.TargetID))
	}
	session.routeDiagnostic(ctx, logger.LevelInfo, "selection", selectionReason, session.model, state, selectionFields...)
	if err == nil && selection != nil && !selection.Acquired {
		if selection.ReleaseFunc != nil {
			selection.ReleaseFunc()
		}
		excluded[state.TargetID] = struct{}{}
		return nil, decision, nil
	}
	if selection != nil && selection.Account != nil {
		decision.SelectedAccountID = selection.Account.ID
		decision.SelectedAccountType = selection.Account.Type
		_ = session.service.bindOpenAIStickySessionDuringSelection(ctx, session.groupID, session.sessionHash, selection.Account.ID)
	}
	return selection, decision, err
}

func (session *openAILegacyWSLatencySession) Observe(ctx context.Context, turn int, account *Account, result *OpenAIForwardResult, turnErr error) {
	if !session.enabled(ctx) {
		if session != nil {
			fields := append(session.actor.diagnosticFields(), zap.String("stage", "observe"), zap.Int("turn", turn))
			writeOpenAIWSLatencyDiagnostic(ctx, logger.LevelDebug, "feature_disabled", fields...)
		}
		return
	}
	session.mu.Lock()
	classification, exists := session.turns[turn]
	recoveryConfirmed := session.recoveryConfirmed
	delete(session.turns, turn)
	session.mu.Unlock()
	settings := session.service.settingService.GetOpenAIWSLatencySwitchSettings(ctx)
	outputTPS, outputTPSKnown := openAIWSOutputTPS(result)
	reason := "turn_classification_missing"
	level := logger.LevelInfo
	fields := append(session.actor.diagnosticFields(), zap.String("stage", "observe"),
		zap.Int("turn", turn), zap.String("model", classification.model), zap.String("transport", string(session.requiredTransport)),
		zap.Bool("user_request", classification.user), zap.String("request_kind", classification.kind),
		zap.Bool("result_present", result != nil), zap.Bool("request_failed", turnErr != nil),
		zap.Int("slow_threshold_ms", settings.SlowMS), zap.Float64("min_output_tps", settings.MinTPS),
		zap.Int("consecutive_required", settings.Consecutive), zap.Float64("output_tps", outputTPS),
		zap.Bool("output_tps_known", outputTPSKnown), zap.Bool("output_tps_threshold_met", settings.MinTPS <= 0 || (outputTPSKnown && outputTPS < settings.MinTPS)),
		zap.Bool("recovering", session.recovering), zap.Bool("recovery_confirmed", recoveryConfirmed))
	if result != nil {
		fields = append(fields, zap.Int("output_tokens", result.Usage.OutputTokens), zap.Int64("duration_ms", result.Duration.Milliseconds()),
			zap.Bool("request_succeeded", result.SucceededForScheduling()), zap.Bool("first_token_known", result.FirstTokenMs != nil))
		if result.FirstTokenMs != nil {
			fields = append(fields, zap.Int("first_token_ms", *result.FirstTokenMs), zap.Bool("first_token_threshold_met", *result.FirstTokenMs >= settings.SlowMS))
			if turnErr == nil && result.SucceededForScheduling() && *result.FirstTokenMs < settings.SlowMS {
				level = logger.LevelDebug
			}
		}
	}
	completionDecision := "not_evaluated"
	defer func() {
		if session.recovering && level == logger.LevelDebug {
			level = logger.LevelInfo
		}
		fields = append(fields, zap.String("switch_completion_decision", completionDecision))
		writeOpenAIWSLatencyDiagnostic(ctx, level, reason, fields...)
	}()
	if !exists {
		return
	}
	if account == nil {
		reason = "account_missing"
		return
	}
	fields = append(fields, zap.Int64("account_id", account.ID))
	switch {
	case !session.recovering:
		completionDecision = "not_recovering"
	case !recoveryConfirmed:
		completionDecision = "context_unconfirmed"
	case !classification.user:
		completionDecision = "non_user_request"
	default:
		completionDecision = "request_unsuccessful"
	}
	now := time.Now().UnixMilli()
	if session.recovering && recoveryConfirmed && classification.user {
		if turnErr != nil || result == nil || !result.SucceededForScheduling() {
			reason = "recovery_request_unsuccessful"
			return
		}
		fromAccountID := int64(0)
		leaseID := ""
		resultID := ""
		upstreamSessionID := ""
		poolModel := ""
		poolKey := ""
		completionDecision = "state_update_failed"
		var completionState openAIWSLatencyState
		completionStateRead := false
		_, completed := session.service.updateOpenAIWSLatencyState(ctx, session.key(session.model), func(state *openAIWSLatencyState) bool {
			completionState, completionStateRead = *state, true
			completionDecision = "state_update_failed"
			switch {
			case state.TargetID != account.ID:
				completionDecision = "target_account_mismatch"
			case state.UpstreamSessionID == "":
				completionDecision = "upstream_session_missing"
			case state.CandidateLeaseID == "":
				completionDecision = "candidate_lease_missing"
			}
			if state.TargetID != account.ID || state.UpstreamSessionID == "" || state.CandidateLeaseID == "" {
				return false
			}
			fromAccountID = state.AccountID
			leaseID = state.CandidateLeaseID
			resultID = state.TargetResultID
			upstreamSessionID = state.UpstreamSessionID
			poolModel = state.TargetUpstreamModel
			poolKey = state.TargetPoolKey
			*state = openAIWSLatencyState{AccountID: account.ID, UpstreamSessionID: upstreamSessionID, TargetUpstreamModel: poolModel, CooldownUntil: now + openAIWSLatencyCooldown.Milliseconds()}
			return true
		})
		fields = append(fields, zap.Bool("switch_completed", completed))
		if completionStateRead {
			fields = append(fields, zap.Int64("completion_target_account_id", completionState.TargetID),
				zap.String("completion_target_result_id", completionState.TargetResultID))
		}
		if completed {
			completionDecision = "completed"
			if poolKey == "" {
				if poolModel == "" {
					poolModel = session.upstreamModel
				}
				poolKey = openAIWSLatencyCandidatePoolKey(session.groupID, poolModel, session.requiredTransport)
			}
			session.service.releaseOpenAIWSLatencyCandidate(ctx, poolKey, leaseID, account.ID, true)
			session.actor.writeEvent("openai.ws_latency_switch_completed", map[string]any{
				"from_account_id":   fromAccountID,
				"target_account_id": account.ID,
				"account_id":        account.ID,
				"target_result_id":  resultID,
				"model":             session.model,
				"transport":         string(session.requiredTransport),
			})
		}
	}
	if !classification.user {
		reason = "non_user_request"
		if classification.kind == "tool_output" {
			reason = "tool_output_excluded"
		} else if classification.kind == "prewarm" {
			reason = "prewarm_excluded"
			level = logger.LevelDebug
		} else if classification.kind == "empty_input" {
			reason = "empty_input"
		}
		return
	}
	slow := turnErr == nil && result != nil && result.SucceededForScheduling() && result.FirstTokenMs != nil && *result.FirstTokenMs >= settings.SlowMS
	if settings.MinTPS > 0 {
		slow = slow && outputTPSKnown && outputTPS < settings.MinTPS
	}
	evaluationReason := "thresholds_met"
	switch {
	case turnErr != nil:
		evaluationReason = "request_failed"
	case result == nil:
		evaluationReason = "result_missing"
	case !result.SucceededForScheduling():
		evaluationReason = "request_unsuccessful"
	case result.FirstTokenMs == nil:
		evaluationReason = "first_token_unknown"
	case *result.FirstTokenMs < settings.SlowMS:
		evaluationReason = "first_token_below_threshold"
	case settings.MinTPS > 0 && !outputTPSKnown:
		evaluationReason = "output_tps_unknown"
	case settings.MinTPS > 0 && outputTPS >= settings.MinTPS:
		evaluationReason = "output_tps_not_below_minimum"
	}
	fields = append(fields, zap.Bool("slow_request", slow), zap.String("threshold_decision", evaluationReason))
	probeID := uuid.NewString()
	var before openAIWSLatencyState
	blocked := false
	state, changed := session.service.updateOpenAIWSLatencyState(ctx, session.key(classification.model), func(state *openAIWSLatencyState) bool {
		before = *state
		reason = evaluationReason
		if slow {
			reason = "consecutive_not_reached"
		}
		blocked = false
		if state.AccountID != account.ID {
			*state = openAIWSLatencyState{AccountID: account.ID}
		}
		if state.CooldownUntil > now || state.PendingUntil > now || state.ProbeUntil > now {
			blocked = true
			switch {
			case state.PendingUntil > now:
				reason = "switch_pending"
			case state.ProbeUntil > now:
				reason = "probe_in_progress"
			default:
				reason = "cooldown_active"
			}
			return false
		}
		state.TargetID, state.TargetResultID, state.TargetUpstreamModel, state.TargetPoolKey, state.PendingUntil, state.ReconnectFrom = 0, "", "", "", 0, ""
		state.CandidateLeaseID = ""
		state.CandidateCommitted = false
		state.ProbeID, state.ProbeUntil = "", 0
		if !slow {
			state.SlowCount = 0
			return true
		}
		state.SlowCount++
		if state.SlowCount >= settings.Consecutive {
			state.SlowCount = 0
			state.ProbeID, state.ProbeUntil = probeID, now+openAIWSLatencyProbeWindow(settings.ProbeCount).Milliseconds()
			state.CooldownUntil = now + openAIWSLatencyCooldown.Milliseconds()
		}
		return true
	})
	if !changed && !blocked {
		reason = "state_update_failed"
		level = logger.LevelWarn
	}
	fields = append(fields, zap.Bool("state_updated", changed))
	// 写入失败时不能把尝试写入的计数描述为已保存的状态。
	if changed || blocked {
		fields = append(fields, zap.Int("slow_count_before", before.SlowCount), zap.Int("slow_count_after", state.SlowCount),
			zap.Int64("previous_account_id", before.AccountID), zap.Int64("target_account_id", state.TargetID), zap.String("active_probe_id", state.ProbeID),
			zap.Int64("cooldown_remaining_ms", openAIWSLatencyRemainingMS(state.CooldownUntil, now)),
			zap.Int64("pending_remaining_ms", openAIWSLatencyRemainingMS(state.PendingUntil, now)), zap.Int64("probe_remaining_ms", openAIWSLatencyRemainingMS(state.ProbeUntil, now)))
	}
	if changed && state.ProbeID == probeID {
		reason = "probe_triggered"
		fields = append(fields, zap.String("probe_id", probeID))
		slog.Info("openai.ws_latency_probe_started", "scope", session.scope, "account_id", account.ID, "model", classification.model, "consecutive", settings.Consecutive, "probe_count", settings.ProbeCount, "max_probe_rounds", openAIWSLatencyMaxProbeRounds, "transport", string(session.requiredTransport))
		probeCtx := context.WithoutCancel(ctx)
		upstreamModel := session.upstreamModel
		if classification.model != session.model {
			mapping, _ := session.service.ResolveChannelMappingAndRestrict(ctx, session.groupID, classification.model)
			upstreamModel = strings.TrimSpace(mapping.MappedModel)
			if upstreamModel == "" {
				upstreamModel = classification.model
			}
		}
		go session.service.probeOpenAIWSLatencyRoutes(probeCtx, session.key(classification.model), session.groupID, account.ID, classification.model, upstreamModel, session.requiredCapability, session.requiredTransport, probeID, session.actor)
	}
}

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

const (
	openAIWSLatencyStateTTL     = 2 * time.Hour
	openAIWSLatencyCooldown     = 5 * time.Minute
	openAIWSLatencyProbeTimeout = 15 * time.Second
)

// Kept optional so gateway cache doubles do not need to implement this feature.
// Redis CAS protects slow counts, probe ownership, and reconnect state across replicas.
type OpenAIWSLatencyCache interface {
	GetOpenAIWSLatencyState(context.Context, string) ([]byte, error)
	CompareAndSwapOpenAIWSLatencyState(context.Context, string, []byte, []byte, time.Duration) (bool, error)
}

type openAIWSLatencyState struct {
	AccountID     int64  `json:"account_id"`
	SlowCount     int    `json:"slow_count"`
	ProbeID       string `json:"probe_id,omitempty"`
	ProbeUntil    int64  `json:"probe_until,omitempty"`
	TargetID      int64  `json:"target_id,omitempty"`
	PendingUntil  int64  `json:"pending_until,omitempty"`
	ReconnectFrom string `json:"reconnect_from,omitempty"`
	CooldownUntil int64  `json:"cooldown_until,omitempty"`
}

func (s *OpenAIGatewayService) updateOpenAIWSLatencyState(ctx context.Context, key string, mutate func(*openAIWSLatencyState) bool) (openAIWSLatencyState, bool) {
	cache, ok := s.cache.(OpenAIWSLatencyCache)
	if !ok || key == "" {
		return openAIWSLatencyState{}, false
	}
	cacheCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	defer cancel()
	for attempt := 0; attempt < 4; attempt++ {
		raw, err := cache.GetOpenAIWSLatencyState(cacheCtx, key)
		if err != nil {
			return openAIWSLatencyState{}, false
		}
		var state openAIWSLatencyState
		if len(raw) > 0 && json.Unmarshal(raw, &state) != nil {
			return openAIWSLatencyState{}, false
		}
		if !mutate(&state) {
			return state, false
		}
		next, err := json.Marshal(state)
		if err != nil {
			return state, false
		}
		updated, err := cache.CompareAndSwapOpenAIWSLatencyState(cacheCtx, key, raw, next, openAIWSLatencyStateTTL)
		if err != nil {
			return state, false
		}
		if updated {
			return state, true
		}
	}
	return openAIWSLatencyState{}, false
}

// A request ending in a tool result continues the in-flight user turn. Only a
// new user message is a safe boundary at which to request a reconnect.
func openAIWSLatencyIsUserRequest(payload []byte) bool {
	if v := gjson.GetBytes(payload, "generate"); v.Exists() && !v.Bool() {
		return false
	}
	input := gjson.GetBytes(payload, "input")
	if input.Type == gjson.String {
		return strings.TrimSpace(input.String()) != ""
	}
	items := input.Array()
	if len(items) == 0 {
		return false
	}
	last := items[len(items)-1]
	return last.Get("role").String() == "user" && (last.Get("type").String() == "" || last.Get("type").String() == "message")
}

type openAIWSLatencyTurn struct {
	model string
	user  bool
}

// Snapshot the authenticated caller so background probes never retain the API
// key or read request-scoped Gin state after the connection has closed.
type openAIWSLatencyActor struct {
	userID    int64
	apiKeyID  int64
	username  string
	userEmail string
	sessionID string
}

func (actor openAIWSLatencyActor) writeEvent(message string, fields map[string]any) {
	if actor.sessionID != "" {
		fields["conversation_id"] = actor.sessionID
	}
	if actor.userID > 0 {
		fields["user_id"] = actor.userID
	}
	if actor.apiKeyID > 0 {
		fields["api_key_id"] = actor.apiKeyID
	}
	if actor.username != "" {
		fields["username"] = actor.username
	}
	if actor.userEmail != "" {
		fields["user_email"] = actor.userEmail
	}
	logger.WriteSinkEvent("info", "openai.ws_latency_switch", message, fields)
}

// OpenAIWSLatencySession tracks caller identity and request classification.
type OpenAIWSLatencySession struct {
	service            *OpenAIGatewayService
	actor              openAIWSLatencyActor
	groupID            *int64
	scope              string
	sessionHash        string
	model              string
	upstreamModel      string
	connectionID       string
	requiredCapability OpenAIEndpointCapability
	requiredTransport  OpenAIUpstreamTransport
	recovering         bool
	recoveryConfirmed  bool
	mu                 sync.Mutex
	turns              map[int]openAIWSLatencyTurn
}

func (s *OpenAIGatewayService) NewOpenAIWSLatencySession(ctx context.Context, c *gin.Context, payload []byte, groupID *int64, apiKey *APIKey, sessionHash, model, upstreamModel string, requiredCapability OpenAIEndpointCapability) *OpenAIWSLatencySession {
	if s == nil || apiKey == nil || s.settingService == nil || !s.settingService.GetOpenAIWSLatencySwitchSettings(ctx).Enabled {
		return nil
	}
	if _, ok := s.cache.(OpenAIWSLatencyCache); !ok {
		return nil
	}
	metadata := openAIWSExecutionTurnMetadata(c, payload)
	if resolveOpenAIWSExecutionLane(c, payload) != "" || (metadata.RequestKind != "" && metadata.RequestKind != openAIWSRequestKindTurn && metadata.RequestKind != openAIWSRequestKindPrewarm) {
		return nil
	}
	scope, _ := resolveOpenAIWSExecutionScope(c, payload, apiKey.ID)
	if scope == "" {
		return nil
	}
	return newOpenAIWSLatencySession(s, groupID, apiKey, scope, sessionHash, model, upstreamModel, requiredCapability, OpenAIUpstreamTransportResponsesWebsocketV2Ingress, payload)
}

// NewOpenAIHTTPResponsesLatencySession tracks HTTP SSE requests by their stable
// session affinity key. Each HTTP request is a new connection, so the reconnect
// marker persists the forced retry across requests.
func (s *OpenAIGatewayService) NewOpenAIHTTPResponsesLatencySession(ctx context.Context, groupID *int64, apiKey *APIKey, sessionHash, model, upstreamModel string, requiredCapability OpenAIEndpointCapability, payload []byte) *OpenAIWSLatencySession {
	if s == nil || apiKey == nil || apiKey.ID <= 0 || strings.TrimSpace(sessionHash) == "" || s.settingService == nil || !s.settingService.GetOpenAIWSLatencySwitchSettings(ctx).Enabled {
		return nil
	}
	if _, ok := s.cache.(OpenAIWSLatencyCache); !ok {
		return nil
	}
	scope, _ := deriveOpenAISessionHashes(fmt.Sprintf("openai_http_latency:%d:%s", apiKey.ID, strings.TrimSpace(sessionHash)))
	return newOpenAIWSLatencySession(s, groupID, apiKey, scope, sessionHash, model, upstreamModel, requiredCapability, OpenAIUpstreamTransportHTTPSSE, payload)
}

func newOpenAIWSLatencySession(s *OpenAIGatewayService, groupID *int64, apiKey *APIKey, scope, sessionHash, model, upstreamModel string, requiredCapability OpenAIEndpointCapability, requiredTransport OpenAIUpstreamTransport, payload []byte) *OpenAIWSLatencySession {
	actor := openAIWSLatencyActor{userID: apiKey.UserID, apiKeyID: apiKey.ID, sessionID: scope}
	if apiKey.User != nil {
		actor.username = apiKey.User.Username
		actor.userEmail = apiKey.User.Email
	}
	return &OpenAIWSLatencySession{service: s, actor: actor, groupID: groupID, scope: scope, sessionHash: sessionHash, model: model, upstreamModel: upstreamModel, connectionID: uuid.NewString(), requiredCapability: requiredCapability, requiredTransport: requiredTransport, turns: map[int]openAIWSLatencyTurn{1: {model: model, user: openAIWSLatencyIsUserRequest(payload)}}}
}

func (session *OpenAIWSLatencySession) enabled(ctx context.Context) bool {
	return session != nil && session.service.settingService.GetOpenAIWSLatencySwitchSettings(ctx).Enabled
}

func (session *OpenAIWSLatencySession) key(model string) string {
	return fmt.Sprintf("%d:%s:%s", derefGroupID(session.groupID), session.scope, hashOpenAIWSLatencyModel(model))
}

func hashOpenAIWSLatencyModel(model string) string {
	hash, _ := deriveOpenAISessionHashes(strings.TrimSpace(model))
	return hash
}

func (session *OpenAIWSLatencySession) state(ctx context.Context) openAIWSLatencyState {
	state, _ := session.service.updateOpenAIWSLatencyState(ctx, session.key(session.model), func(*openAIWSLatencyState) bool { return false })
	return state
}

// Prepare checks the unmodified client frame, before gateway affinity recovery
// can strip previous_response_id. A delta-only reconnect must never reach B.
func (session *OpenAIWSLatencySession) Prepare(ctx context.Context, payload []byte) error {
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
			slog.Warn("openai.ws_latency_recovery_context_required", "scope", session.scope, "has_previous_response_id", previous != "", "tool_context_complete", coverage.ContextCoversAllCallIDs)
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
	slog.Info("openai.ws_latency_reconnect_received", "scope", session.scope, "input_items", len(input.Array()), "has_previous_response_id", false, "has_tool_output", coverage.HasFunctionCallOutput, "tool_context_complete", coverage.ContextCoversAllCallIDs, "context_confirmed", session.recoveryConfirmed)
	session.clearUpstreamAffinity(ctx)
	return nil
}

// PrepareHTTP requests one client retry before the pending route switch. The
// retried request must carry complete input so it can be replayed on another
// account without relying on response state held by the old account.
func (session *OpenAIWSLatencySession) PrepareHTTP(ctx context.Context, payload []byte) (bool, error) {
	if !session.enabled(ctx) {
		return false, nil
	}
	state := session.state(ctx)
	if state.TargetID <= 0 || state.PendingUntil <= time.Now().UnixMilli() || !openAIWSLatencyIsUserRequest(payload) {
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
				"model":             session.model,
				"transport":         string(OpenAIUpstreamTransportHTTPSSE),
			})
			return true, nil
		}
		if state.ReconnectFrom == "" {
			return false, nil
		}
	}

	previous := strings.TrimSpace(gjson.GetBytes(payload, "previous_response_id").String())
	coverage := AnalyzeToolCallOutputContextCoverageBytes(payload)
	if previous != "" || (coverage.HasFunctionCallOutput && !coverage.ContextCoversAllCallIDs) {
		return false, fmt.Errorf("route switch requires complete context without previous_response_id")
	}
	input := gjson.GetBytes(payload, "input")
	if !((input.IsArray() && len(input.Array()) > 0) || (input.Type == gjson.String && strings.TrimSpace(input.String()) != "")) {
		return false, fmt.Errorf("route switch requires complete request input")
	}
	session.recovering = true
	session.recoveryConfirmed = true
	session.clearUpstreamAffinity(ctx)
	slog.Info("openai.ws_latency_reconnect_received", "scope", session.scope, "input_items", len(input.Array()), "has_previous_response_id", false, "has_tool_output", coverage.HasFunctionCallOutput, "tool_context_complete", coverage.ContextCoversAllCallIDs, "context_confirmed", true, "transport", string(OpenAIUpstreamTransportHTTPSSE))
	return false, nil
}

func (session *OpenAIWSLatencySession) clearUpstreamAffinity(ctx context.Context) {
	if store := session.service.getOpenAIWSStateStore(); store != nil {
		store.DeleteSessionConn(derefGroupID(session.groupID), session.scope)
		store.DeleteSessionTurnState(derefGroupID(session.groupID), session.scope)
	}
	_ = session.service.deleteStickySessionAccountID(ctx, session.groupID, session.sessionHash)
}

func (session *OpenAIWSLatencySession) requestReconnect(ctx context.Context, model string) error {
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
		"model":             model,
	})
	return NewOpenAIWSClientCloseError(coderws.StatusTryAgainLater, "faster upstream available; reconnect with complete context", nil)
}

func (session *OpenAIWSLatencySession) BeforeRequest(ctx context.Context, turn int, account *Account, payload []byte, model string) error {
	if !session.enabled(ctx) {
		return nil
	}
	if model == "" {
		model = session.model
	}
	user := openAIWSLatencyIsUserRequest(payload)
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
	session.turns[turn] = openAIWSLatencyTurn{model: model, user: user}
	session.mu.Unlock()
	if !user || account == nil {
		return nil
	}
	state, _ := session.service.updateOpenAIWSLatencyState(ctx, session.key(model), func(*openAIWSLatencyState) bool { return false })
	if state.AccountID == account.ID && state.TargetID > 0 && state.PendingUntil > time.Now().UnixMilli() {
		return session.requestReconnect(ctx, model)
	}
	return nil
}

// SelectTarget reuses normal model, group, privacy, profit, transport, quota and
// concurrency checks. Excluding A also applies to normal failover if B became busy.
func (session *OpenAIWSLatencySession) SelectTarget(ctx context.Context, excluded map[int64]struct{}, requiredTransport OpenAIUpstreamTransport) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
	if !session.enabled(ctx) || !session.recovering {
		return nil, OpenAIAccountScheduleDecision{}, nil
	}
	state := session.state(ctx)
	if state.TargetID <= 0 || state.PendingUntil <= time.Now().UnixMilli() {
		return nil, OpenAIAccountScheduleDecision{}, nil
	}
	excluded[state.AccountID] = struct{}{}
	request := OpenAIAccountScheduleRequest{GroupID: session.groupID, Platform: PlatformOpenAI, SessionHash: session.sessionHash, StickyAccountID: state.TargetID, RequestedModel: session.upstreamModel, RequiredTransport: requiredTransport, RequiredCapability: session.requiredCapability, ExcludedIDs: excluded, DisableStickyEscape: true, PreserveStickyBinding: true, RequirePrivacySet: session.service.openAIGroupRequiresPrivacySet(ctx, session.groupID)}
	scheduler := &defaultOpenAIAccountScheduler{service: session.service}
	selection, _, err := scheduler.selectBySessionHash(ctx, request)
	decision := OpenAIAccountScheduleDecision{Layer: "latency_switch"}
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

func (session *OpenAIWSLatencySession) Observe(ctx context.Context, turn int, account *Account, result *OpenAIForwardResult, turnErr error) {
	if !session.enabled(ctx) {
		return
	}
	session.mu.Lock()
	classification, exists := session.turns[turn]
	recoveryConfirmed := session.recoveryConfirmed
	delete(session.turns, turn)
	session.mu.Unlock()
	if !exists || account == nil {
		return
	}
	now := time.Now().UnixMilli()
	if session.recovering && recoveryConfirmed && classification.user && turnErr == nil && result != nil && result.SucceededForScheduling() {
		fromAccountID := int64(0)
		_, completed := session.service.updateOpenAIWSLatencyState(ctx, session.key(session.model), func(state *openAIWSLatencyState) bool {
			if state.TargetID == 0 || state.AccountID == account.ID {
				return false
			}
			fromAccountID = state.AccountID
			*state = openAIWSLatencyState{AccountID: account.ID, CooldownUntil: now + openAIWSLatencyCooldown.Milliseconds()}
			return true
		})
		if completed {
			session.actor.writeEvent("openai.ws_latency_switch_completed", map[string]any{
				"from_account_id":   fromAccountID,
				"target_account_id": account.ID,
				"account_id":        account.ID,
				"model":             session.model,
				"transport":         string(session.requiredTransport),
			})
		}
	}
	if !classification.user {
		return
	}
	settings := session.service.settingService.GetOpenAIWSLatencySwitchSettings(ctx)
	slow := turnErr == nil && result != nil && result.SucceededForScheduling() && result.FirstTokenMs != nil && *result.FirstTokenMs >= settings.SlowMS
	probeID := uuid.NewString()
	state, changed := session.service.updateOpenAIWSLatencyState(ctx, session.key(classification.model), func(state *openAIWSLatencyState) bool {
		if state.AccountID != account.ID {
			*state = openAIWSLatencyState{AccountID: account.ID}
		}
		if state.CooldownUntil > now || state.PendingUntil > now || state.ProbeUntil > now {
			return false
		}
		state.TargetID, state.PendingUntil, state.ReconnectFrom = 0, 0, ""
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
	if changed && state.ProbeID == probeID {
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
		go session.service.probeOpenAIWSLatencyRoutes(probeCtx, session.key(classification.model), session.groupID, account.ID, upstreamModel, session.requiredCapability, session.requiredTransport, probeID, session.actor)
	}
}

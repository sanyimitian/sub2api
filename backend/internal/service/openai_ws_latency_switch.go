package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

const (
	openAIWSLatencyStateTTL                           = 2 * time.Hour
	openAIWSLatencyCooldown                           = 5 * time.Minute
	openAIWSLatencyProbeTimeout                       = 15 * time.Second
	OpenAIWSLatencySwitchCloseCode coderws.StatusCode = 4001
	OpenAIWSLatencySwitchErrorCode                    = "sub2api_latency"
)

// ErrOpenAIPreparedSessionUnavailable 表示无法可靠确定会话归属，客户端可稍后重试。
var ErrOpenAIPreparedSessionUnavailable = errors.New("prepared session state is unavailable")

// Kept optional so gateway cache doubles do not need to implement this feature.
// Redis CAS protects slow counts, probe ownership, and reconnect state across replicas.
type OpenAIWSLatencyCache interface {
	GetOpenAIWSLatencyState(context.Context, string) ([]byte, error)
	CompareAndSwapOpenAIWSLatencyState(context.Context, string, []byte, []byte, time.Duration) (bool, error)
}

// 首字慢自动切换的业务目标（经用户澄清）：
// 本系统账号是调用上游的账号条目／接口密钥，不等同于上游内部的 OpenAI 账号。
// 用户确认：沿用探测时的上游会话标识，后续请求会延续该会话原有的账号绑定。
// 因此切换对象是「探测合格的上游会话」，本系统 AccountID 是否变化不是切换成功的判据。
//
// 用户侧原对话保持不变，由网关维护「原用户对话 -> 目标账号 + 快速上游会话」的映射。
// 接管时要把原对话的完整有效上下文送到目标会话，包括系统指令、工具调用及对应结果；
// 探测用的测试问答不得混入用户上下文。接管成功后，后续请求持续使用目标会话，
// 不再使用旧慢会话；在途旧请求也不应覆盖已经确认的新映射。
//
// 会话路由与上下文恢复是两个独立条件：换会话标识不会自动搬运历史。
// 仅有增量内容或旧 previous_response_id（上次响应引用）时，必须先确认完整上下文可恢复，
// 不得直接丢弃引用后转发残缺内容，也不能把返回 529 或关闭连接等同于客户端已重放历史。
// 工具结果回传的速度统计与安全接管时机应分别判断，不能仅因包含工具结果就遗漏慢请求。
//
// 上述内容是后续维护必须遵守的目标与边界，不表示仅凭状态字段或重试信号就已验证
// 所有客户端的上下文恢复行为；具体接管路径仍须校验上下文并确认目标请求成功。
type openAIWSLatencyState struct {
	// 旧状态只允许一个线程认领；新键保存别名，避免跨键复制产生两个可写绑定。
	MigrationScope string `json:"migration_scope,omitempty"`
	StateAliasKey  string `json:"state_alias_key,omitempty"`
	AccountID      int64  `json:"account_id"`
	SlowCount      int    `json:"slow_count"`
	ProbeID        string `json:"probe_id,omitempty"`
	ProbeUntil     int64  `json:"probe_until,omitempty"`
	TargetID       int64  `json:"target_id,omitempty"`
	TargetResultID string `json:"target_result_id,omitempty"`
	// 候选的实际探测模型，用于校验探测配置与追踪来源；备用池接管时不用于覆盖用户请求模型。
	TargetUpstreamModel     string `json:"target_upstream_model,omitempty"`
	TargetPoolKey           string `json:"target_pool_key,omitempty"`
	TargetFingerprint       string `json:"target_fingerprint,omitempty"`
	UpstreamSessionID       string `json:"upstream_session_id,omitempty"`
	ActiveUpstreamSessionID string `json:"active_upstream_session_id,omitempty"`
	InitialAssignment       bool   `json:"initial_assignment,omitempty"`
	InitialDecisionMade     bool   `json:"initial_decision_made,omitempty"`
	PendingUntil            int64  `json:"pending_until,omitempty"`
	ReconnectFrom           string `json:"reconnect_from,omitempty"`
	CooldownUntil           int64  `json:"cooldown_until,omitempty"`
	CandidateLeaseID        string `json:"candidate_lease_id,omitempty"`
	CandidateCommitted      bool   `json:"candidate_committed,omitempty"`
}

// openAIWSLatencyReusableCandidate 表示一条可被独占领取的快速探测会话。
// ResultID 是本地候选编号；UpstreamSessionID 才是接管时需要延续的上游会话标识。
// 同一 AccountID 可以有多个独立候选，不能按账号合并，否则会丢失已探测的快速会话。
//
// 共享对象只限于尚未承载真实用户内容的探测会话，不共享用户对话历史：
//   - 领取必须原子且独占；临时占用不等于切换成功，也不应被视为池已耗尽而重复探测。
//   - 尚未发送用户内容时，失败可以释放；一旦准备发送用户内容，必须先锁定独占归属，
//     防止请求在途时租约到期，被另一用户领取。Committed 表示这一独占承诺，不表示成功。
//   - 已发送用户内容后即使超时／失败，也不能假定上游未记录；只能保留给原对话或作废，
//     不得重新共享。只有接管请求确认成功才记为「切换成功／成功消费」，失败作废应另行区分。
type openAIWSLatencyReusableCandidate struct {
	ResultID           string                  `json:"result_id,omitempty"`
	AccountID          int64                   `json:"account_id"`
	UpstreamModel      string                  `json:"upstream_model,omitempty"`
	ProbeModel         string                  `json:"probe_model,omitempty"`
	UpstreamSessionID  string                  `json:"upstream_session_id,omitempty"`
	FirstTokenMS       int                     `json:"first_token_ms"`
	ExpiresAt          int64                   `json:"expires_at"`
	LeaseID            string                  `json:"lease_id,omitempty"`
	LeaseUntil         int64                   `json:"lease_until,omitempty"`
	Committed          bool                    `json:"committed,omitempty"`
	AccountFingerprint string                  `json:"account_fingerprint,omitempty"`
	Transport          OpenAIUpstreamTransport `json:"transport,omitempty"`
	QualityPassed      bool                    `json:"quality_passed,omitempty"`
}

const openAIPreparedSessionContextKey = "openai_prepared_session_enabled"

const openAIWSLatencyUpstreamSessionContextKey = "openai_ws_latency_upstream_session_id"

type openAIWSLatencyCandidatePool struct {
	ProbeID       string                                       `json:"probe_id,omitempty"`
	ProbeUntil    int64                                        `json:"probe_until,omitempty"`
	Candidates    []openAIWSLatencyReusableCandidate           `json:"candidates,omitempty"`
	RetryAfter    int64                                        `json:"retry_after,omitempty"`
	AccountProbes map[int64]openAISessionPoolAccountProbeState `json:"account_probes,omitempty"`
}

type openAISessionPoolAccountProbeState struct {
	Rounds     int   `json:"rounds,omitempty"`
	RetryAfter int64 `json:"retry_after,omitempty"`
}

func (s *OpenAIGatewayService) updateOpenAIWSLatencyState(ctx context.Context, key string, mutate func(*openAIWSLatencyState) bool) (openAIWSLatencyState, bool) {
	cache, ok := s.cache.(OpenAIWSLatencyCache)
	if !ok || key == "" {
		logOpenAIWSLatencyCacheFailure(ctx, key, "access", "state_cache_unavailable", 0, nil)
		return openAIWSLatencyState{}, false
	}
	cacheCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	defer cancel()
	type stateAlias struct {
		key string
		raw []byte
	}
	var aliases []stateAlias
	for attempt := 0; attempt < 4; attempt++ {
		raw, err := cache.GetOpenAIWSLatencyState(cacheCtx, key)
		if err != nil {
			logOpenAIWSLatencyCacheFailure(ctx, key, "read", "state_read_failed", attempt+1, err)
			return openAIWSLatencyState{}, false
		}
		var state openAIWSLatencyState
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &state); err != nil {
				logOpenAIWSLatencyCacheFailure(ctx, key, "decode", "state_decode_failed", attempt+1, err)
				return openAIWSLatencyState{}, false
			}
		}
		if state.StateAliasKey != "" {
			aliases = append(aliases, stateAlias{key: key, raw: raw})
			key = state.StateAliasKey
			continue
		}
		migrationScope := state.MigrationScope
		if !mutate(&state) {
			return state, false
		}
		// 业务状态整体替换时也不能释放旧会话的迁移归属。
		if migrationScope != "" {
			state.MigrationScope = migrationScope
		}
		next, err := json.Marshal(state)
		if err != nil {
			logOpenAIWSLatencyCacheFailure(ctx, key, "encode", "state_encode_failed", attempt+1, err)
			return state, false
		}
		updated, err := cache.CompareAndSwapOpenAIWSLatencyState(cacheCtx, key, raw, next, openAIWSLatencyStateTTL)
		if err != nil {
			logOpenAIWSLatencyCacheFailure(ctx, key, "compare_and_swap", "state_write_failed", attempt+1, err)
			return state, false
		}
		if updated {
			// 先保存实际绑定，再由内向外续期入口，保证入口不会先于绑定过期。
			// 使用原值比较，不能复活已过期或覆盖已被其他请求修改的入口。
			for i := len(aliases) - 1; i >= 0; i-- {
				alias := aliases[i]
				refreshed, err := cache.CompareAndSwapOpenAIWSLatencyState(cacheCtx, alias.key, alias.raw, alias.raw, openAIWSLatencyStateTTL)
				if err != nil || !refreshed {
					logOpenAIWSLatencyCacheFailure(ctx, alias.key, "compare_and_swap", "state_alias_refresh_failed", attempt+1, err)
					return state, false
				}
			}
			return state, true
		}
	}
	logOpenAIWSLatencyCacheFailure(ctx, key, "compare_and_swap", "state_conflict_exhausted", 4, nil)
	return openAIWSLatencyState{}, false
}

// 共享池不按来源分组隔离：分组 1 和分组 2 都有权调用账号 1 时，
// 分组 2 可以领取分组 1 在账号 1 上创建的未过期、未使用的合格探测会话。
// 池键仅用于组织候选，不赋予调用权限；领取及实际使用仍须按当前分组检查账号权限、
// 实际上游模型和传输协议等兼容条件，计费归当前调用方，不继承原探测者的权限或身份。
func openAIWSLatencyCandidatePoolKey(_ *int64, model string, transport OpenAIUpstreamTransport) string {
	seed := fmt.Sprintf("openai_ws_latency_pool:%s:%s", strings.TrimSpace(model), transport)
	key, _ := deriveOpenAISessionHashes(seed)
	return "candidate_pool:" + key
}

func (s *OpenAIGatewayService) updateOpenAIWSLatencyCandidatePool(ctx context.Context, key string, mutate func(*openAIWSLatencyCandidatePool) bool) (openAIWSLatencyCandidatePool, bool) {
	cache, ok := s.cache.(OpenAIWSLatencyCache)
	if !ok || key == "" {
		logOpenAIWSLatencyCacheFailure(ctx, key, "access", "state_cache_unavailable", 0, nil)
		return openAIWSLatencyCandidatePool{}, false
	}
	cacheCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	defer cancel()
	for attempt := 0; attempt < 8; attempt++ {
		raw, err := cache.GetOpenAIWSLatencyState(cacheCtx, key)
		if err != nil {
			logOpenAIWSLatencyCacheFailure(ctx, key, "read", "state_read_failed", attempt+1, err)
			return openAIWSLatencyCandidatePool{}, false
		}
		var pool openAIWSLatencyCandidatePool
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &pool); err != nil {
				logOpenAIWSLatencyCacheFailure(ctx, key, "decode", "state_decode_failed", attempt+1, err)
				return openAIWSLatencyCandidatePool{}, false
			}
		}
		if !mutate(&pool) {
			return pool, false
		}
		next, err := json.Marshal(pool)
		if err != nil {
			logOpenAIWSLatencyCacheFailure(ctx, key, "encode", "state_encode_failed", attempt+1, err)
			return pool, false
		}
		// 可配置等待可能超过原两小时缓存期限；状态必须保存到最长等待结束以后。
		ttl := openAIWSLatencyStateTTL
		now := time.Now().UnixMilli()
		for _, state := range pool.AccountProbes {
			if remaining := time.Duration(state.RetryAfter-now) * time.Millisecond; remaining > ttl {
				ttl = remaining + openAIWSLatencyStateTTL
			}
		}
		updated, err := cache.CompareAndSwapOpenAIWSLatencyState(cacheCtx, key, raw, next, ttl)
		if err != nil {
			logOpenAIWSLatencyCacheFailure(ctx, key, "compare_and_swap", "state_write_failed", attempt+1, err)
			return pool, false
		}
		if updated {
			return pool, true
		}
	}
	logOpenAIWSLatencyCacheFailure(ctx, key, "compare_and_swap", "state_conflict_exhausted", 8, nil)
	return openAIWSLatencyCandidatePool{}, false
}

func (s *OpenAIGatewayService) getOpenAIWSLatencyCandidatePool(ctx context.Context, key string) openAIWSLatencyCandidatePool {
	cache, ok := s.cache.(OpenAIWSLatencyCache)
	if !ok || key == "" {
		logOpenAIWSLatencyCacheFailure(ctx, key, "access", "state_cache_unavailable", 0, nil)
		return openAIWSLatencyCandidatePool{}
	}
	cacheCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	defer cancel()
	raw, err := cache.GetOpenAIWSLatencyState(cacheCtx, key)
	if err != nil {
		logOpenAIWSLatencyCacheFailure(ctx, key, "read", "state_read_failed", 1, err)
		return openAIWSLatencyCandidatePool{}
	}
	if len(raw) == 0 {
		return openAIWSLatencyCandidatePool{}
	}
	var pool openAIWSLatencyCandidatePool
	if err := json.Unmarshal(raw, &pool); err != nil {
		logOpenAIWSLatencyCacheFailure(ctx, key, "decode", "state_decode_failed", 1, err)
		return openAIWSLatencyCandidatePool{}
	}
	return pool
}

func pruneOpenAIWSLatencyCandidates(pool *openAIWSLatencyCandidatePool, now int64) {
	kept := pool.Candidates[:0]
	for _, candidate := range pool.Candidates {
		if candidate.ExpiresAt > now {
			kept = append(kept, candidate)
		}
	}
	pool.Candidates = kept
}

func openAIWSLatencyCandidateAllowed(candidate openAIWSLatencyReusableCandidate, eligible map[int64]struct{}, expectedUpstreamModels map[int64]string) bool {
	if candidate.UpstreamSessionID == "" || strings.TrimSpace(candidate.UpstreamModel) == "" {
		return false
	}
	if eligible != nil {
		if _, ok := eligible[candidate.AccountID]; !ok {
			return false
		}
	}
	if expected := strings.TrimSpace(expectedUpstreamModels[candidate.AccountID]); expected != "" && strings.TrimSpace(candidate.UpstreamModel) != expected {
		return false
	}
	return true
}

func (s *OpenAIGatewayService) releaseOpenAIWSLatencyCandidate(ctx context.Context, poolKey, leaseID string, accountID int64, consume bool) {
	if leaseID == "" || accountID <= 0 {
		return
	}
	_, _ = s.updateOpenAIWSLatencyCandidatePool(ctx, poolKey, func(pool *openAIWSLatencyCandidatePool) bool {
		for i := range pool.Candidates {
			candidate := &pool.Candidates[i]
			if candidate.AccountID != accountID || candidate.LeaseID != leaseID {
				continue
			}
			if consume {
				pool.Candidates = append(pool.Candidates[:i], pool.Candidates[i+1:]...)
			} else if !candidate.Committed {
				candidate.LeaseID = ""
				candidate.LeaseUntil = 0
			}
			return true
		}
		return false
	})
}

// 发送真实用户内容前将租约转为不可再共享的独占归属。
// 请求失败或客户端断开也不能撤销它，因为上游可能已经保存了用户内容。
func (s *OpenAIGatewayService) commitOpenAIWSLatencyCandidate(ctx context.Context, poolKey, leaseID, resultID string, accountID int64) bool {
	if poolKey == "" || leaseID == "" || resultID == "" || accountID <= 0 {
		return false
	}
	_, committed := s.updateOpenAIWSLatencyCandidatePool(ctx, poolKey, func(pool *openAIWSLatencyCandidatePool) bool {
		now := time.Now().UnixMilli()
		for i := range pool.Candidates {
			candidate := &pool.Candidates[i]
			if candidate.ResultID != resultID || candidate.LeaseID != leaseID || candidate.AccountID != accountID {
				continue
			}
			if !candidate.Committed && (candidate.ExpiresAt <= now || candidate.LeaseUntil <= now) {
				return false
			}
			candidate.Committed = true
			return true
		}
		return false
	})
	return committed
}

// A request ending in a tool result continues the in-flight user turn. Only a
// new user message is a safe boundary at which to request a reconnect.
func openAIWSLatencyIsUserRequest(payload []byte) bool {
	return openAIWSLatencyRequestKind(payload) == "user_message"
}

type openAIWSLatencyTurn struct {
	model string
	user  bool
	kind  string
	route *openAIWSLatencyTurnRoute
}

// 记录这一轮真正发出的线路，不能拿晚到结果确认另一个同账号的候选。
type openAIWSLatencyTurnRoute struct {
	accountID int64
	sessionID string
	resultID  string
	leaseID   string
}

// Snapshot the authenticated caller so background probes never retain the API
// key or read request-scoped Gin state after the connection has closed.
type openAIWSLatencyActor struct {
	userID          int64
	apiKeyID        int64
	username        string
	userEmail       string
	sessionID       string
	groupID         int64
	requestID       string
	clientRequestID string
}

func (actor openAIWSLatencyActor) writeEvent(message string, fields map[string]any) {
	fields["group_id"] = actor.groupID
	if actor.requestID != "" {
		fields["request_id"] = actor.requestID
	}
	if actor.clientRequestID != "" {
		fields["client_request_id"] = actor.clientRequestID
	}
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
	legacy              *openAILegacyWSLatencySession
	service             *OpenAIGatewayService
	actor               openAIWSLatencyActor
	groupID             *int64
	scope               string
	sessionHash         string
	model               string
	upstreamModel       string
	connectionID        string
	requiredCapability  OpenAIEndpointCapability
	requiredTransport   OpenAIUpstreamTransport
	existingBindingOnly bool
	initializationErr   error
	requireCompact      bool
	recovering          bool
	recoveryConfirmed   bool
	mu                  sync.Mutex
	turns               map[int]openAIWSLatencyTurn
}

func (s *OpenAIGatewayService) NewOpenAIWSLatencySession(ctx context.Context, c *gin.Context, payload []byte, groupID *int64, apiKey *APIKey, sessionHash, model, upstreamModel string, requiredCapability OpenAIEndpointCapability) *OpenAIWSLatencySession {
	skip := func(reason string) {
		logOpenAIWSLatencySessionSkipped(ctx, groupID, apiKey, model, OpenAIUpstreamTransportResponsesWebsocketV2Ingress, reason)
	}
	if s == nil || apiKey == nil || s.settingService == nil {
		skip("session_dependency_missing")
		return nil
	}
	if !s.settingService.IsOpenAISessionPoolEnabled(ctx) {
		if !s.settingService.GetOpenAIWSLatencySwitchSettings(ctx).Enabled {
			skip("feature_disabled")
			return nil
		}
		metadata := openAIWSExecutionTurnMetadata(c, payload)
		if resolveOpenAIWSExecutionLane(c, payload) != "" || (metadata.RequestKind != "" && metadata.RequestKind != openAIWSRequestKindTurn && metadata.RequestKind != openAIWSRequestKindPrewarm) {
			skip("execution_lane_or_kind_excluded")
			return nil
		}
		if _, ok := s.cache.(OpenAIWSLatencyCache); !ok {
			skip("state_cache_unavailable")
			return nil
		}
		scope, _ := resolveOpenAIWSExecutionScope(c, payload, apiKey.ID)
		if scope == "" {
			skip("session_scope_missing")
			return nil
		}
		return newOpenAILegacyWSLatencySession(s, groupID, apiKey, scope, sessionHash, model, upstreamModel, requiredCapability, OpenAIUpstreamTransportResponsesWebsocketV2Ingress, payload, ctx)
	}
	if _, ok := s.cache.(OpenAIWSLatencyCache); !ok {
		skip("state_cache_unavailable")
		return nil
	}
	metadata := openAIWSExecutionTurnMetadata(c, payload)
	if resolveOpenAIWSExecutionLane(c, payload) != "" || (metadata.RequestKind != "" && metadata.RequestKind != openAIWSRequestKindTurn && metadata.RequestKind != openAIWSRequestKindPrewarm && metadata.RequestKind != openAIWSRequestKindCompaction) {
		skip("execution_lane_or_kind_excluded")
		return nil
	}
	scope, _ := resolveOpenAIWSExecutionScope(c, payload, apiKey.ID)
	if scope == "" {
		skip("session_scope_missing")
		return nil
	}
	session := newOpenAIWSLatencySession(ctx, s, groupID, apiKey, scope, sessionHash, model, upstreamModel, requiredCapability, OpenAIUpstreamTransportResponsesWebsocketV2Ingress, payload, openAIHTTPResponsesLatencyScope(apiKey.ID, sessionHash))
	if metadata.RequestKind == openAIWSRequestKindCompaction {
		session.PrepareExistingBinding(false)
	}
	return session
}

// NewOpenAIHTTPResponsesLatencySession tracks HTTP SSE requests by their stable
// session affinity key. Each HTTP request is a new connection, so the reconnect
// marker persists the forced retry across requests.
func (s *OpenAIGatewayService) NewOpenAIHTTPResponsesLatencySession(ctx context.Context, groupID *int64, apiKey *APIKey, sessionHash, model, upstreamModel string, requiredCapability OpenAIEndpointCapability, payload []byte, requestContexts ...*gin.Context) *OpenAIWSLatencySession {
	skip := func(reason string) {
		logOpenAIWSLatencySessionSkipped(ctx, groupID, apiKey, model, OpenAIUpstreamTransportHTTPSSE, reason)
	}
	if s == nil || apiKey == nil || apiKey.ID <= 0 || s.settingService == nil {
		skip("session_dependency_missing")
		return nil
	}
	if strings.TrimSpace(sessionHash) == "" {
		skip("session_hash_missing")
		return nil
	}
	if !s.settingService.IsOpenAISessionPoolEnabled(ctx) {
		if !s.settingService.GetOpenAIWSLatencySwitchSettings(ctx).Enabled {
			skip("feature_disabled")
			return nil
		}
		if _, ok := s.cache.(OpenAIWSLatencyCache); !ok {
			skip("state_cache_unavailable")
			return nil
		}
		return newOpenAILegacyWSLatencySession(s, groupID, apiKey, openAIHTTPResponsesLatencyScope(apiKey.ID, sessionHash), sessionHash, model, upstreamModel, requiredCapability, OpenAIUpstreamTransportHTTPSSE, payload, ctx)
	}
	if _, ok := s.cache.(OpenAIWSLatencyCache); !ok {
		skip("state_cache_unavailable")
		return nil
	}
	legacyScope := openAIHTTPResponsesLatencyScope(apiKey.ID, sessionHash)
	scope := legacyScope
	if len(requestContexts) > 0 && requestContexts[0] != nil {
		sharedScope, _ := resolveOpenAIWSExecutionScope(requestContexts[0], payload, apiKey.ID)
		if sharedScope == "" {
			skip("session_scope_missing")
			return nil
		}
		scope = sharedScope
	}
	return newOpenAIWSLatencySession(ctx, s, groupID, apiKey, scope, sessionHash, model, upstreamModel, requiredCapability, OpenAIUpstreamTransportHTTPSSE, payload, legacyScope)
}

func openAIHTTPResponsesLatencyScope(apiKeyID int64, sessionHash string) string {
	scope, _ := deriveOpenAISessionHashes(fmt.Sprintf("openai_http_latency:%d:%s", apiKeyID, strings.TrimSpace(sessionHash)))
	return scope
}

func newOpenAIWSLatencySession(ctx context.Context, s *OpenAIGatewayService, groupID *int64, apiKey *APIKey, scope, sessionHash, model, upstreamModel string, requiredCapability OpenAIEndpointCapability, requiredTransport OpenAIUpstreamTransport, payload []byte, legacyScopes ...string) *OpenAIWSLatencySession {
	actor := openAIWSLatencyActor{userID: apiKey.UserID, apiKeyID: apiKey.ID, sessionID: scope}
	actor.captureRequest(ctx, groupID)
	if apiKey.User != nil {
		actor.username = apiKey.User.Username
		actor.userEmail = apiKey.User.Email
	}
	kind := openAIWSLatencyRequestKind(payload)
	session := &OpenAIWSLatencySession{service: s, actor: actor, groupID: groupID, scope: scope, sessionHash: sessionHash, model: model, upstreamModel: upstreamModel, connectionID: uuid.NewString(), requiredCapability: requiredCapability, requiredTransport: requiredTransport, turns: map[int]openAIWSLatencyTurn{1: {model: model, user: kind == "user_message", kind: kind}}}
	// 升级时延续当前模型已有的会话状态，避免丢失已接管的上游绑定。
	current, err := s.readOpenAIPreparedState(ctx, session.key(model))
	if err != nil {
		session.initializationErr = err
		return session
	}
	if current == (openAIWSLatencyState{}) {
		keys := []string{fmt.Sprintf("%d:%s:%s", derefGroupID(groupID), scope, hashOpenAIWSLatencyModel(model))}
		for _, oldScope := range legacyScopes {
			if oldScope != "" && oldScope != scope {
				keys = append(keys, fmt.Sprintf("conversation:v2:%d:%s", derefGroupID(groupID), oldScope), fmt.Sprintf("%d:%s:%s", derefGroupID(groupID), oldScope, hashOpenAIWSLatencyModel(model)))
			}
		}
		for _, legacyKey := range keys {
			legacy, err := s.readOpenAIPreparedState(ctx, legacyKey)
			if err != nil {
				session.initializationErr = err
				return session
			}
			if legacy == (openAIWSLatencyState{}) {
				continue
			}
			// 在旧键原子认领；失败后允许同线程重试，其他线程不能继承私有会话。
			claimedState, claimed := s.updateOpenAIWSLatencyState(ctx, legacyKey, func(state *openAIWSLatencyState) bool {
				if *state == (openAIWSLatencyState{}) || (state.MigrationScope != "" && state.MigrationScope != scope) {
					return false
				}
				state.MigrationScope = scope
				return true
			})
			if !claimed {
				if claimedState.MigrationScope != "" && claimedState.MigrationScope != scope {
					// 新版旧键已经有归属，不能再从更早版本的残留状态继承同一会话。
					break
				}
				session.initializationErr = ErrOpenAIPreparedSessionUnavailable
				return session
			}
			alreadyExists := false
			_, migrated := s.updateOpenAIWSLatencyState(ctx, session.key(model), func(state *openAIWSLatencyState) bool {
				alreadyExists = *state != (openAIWSLatencyState{})
				if alreadyExists {
					return false
				}
				*state = openAIWSLatencyState{StateAliasKey: legacyKey}
				return true
			})
			if !migrated && !alreadyExists {
				session.initializationErr = ErrOpenAIPreparedSessionUnavailable
			}
			break
		}
	}
	return session
}

func (session *OpenAIWSLatencySession) recoveryState() (bool, bool) {
	session.mu.Lock()
	defer session.mu.Unlock()
	return session.recovering, session.recoveryConfirmed
}

func (session *OpenAIWSLatencySession) setRecovery(recovering, confirmed bool) {
	session.mu.Lock()
	session.recovering, session.recoveryConfirmed = recovering, confirmed
	session.mu.Unlock()
}

func (session *OpenAIWSLatencySession) enabled(ctx context.Context) bool {
	if session != nil && session.legacy != nil {
		return session.legacy.enabled(ctx)
	}
	return session != nil && session.service != nil && session.service.settingService != nil && session.service.settingService.IsOpenAISessionPoolEnabled(ctx)
}

// 上游绑定属于对话，不能随请求模型变化而重新领取或丢失。
func (session *OpenAIWSLatencySession) key(model string) string {
	if session != nil && session.legacy != nil {
		return session.legacy.key(model)
	}
	return fmt.Sprintf("conversation:v2:%d:%s", derefGroupID(session.groupID), session.scope)
}

func hashOpenAIWSLatencyModel(model string) string {
	hash, _ := deriveOpenAISessionHashes(strings.TrimSpace(model))
	return hash
}

// 只读回调是否执行代表读取成功；缓存异常不能当作新对话。
func (s *OpenAIGatewayService) readOpenAIPreparedState(ctx context.Context, key string) (openAIWSLatencyState, error) {
	read := false
	state, _ := s.updateOpenAIWSLatencyState(ctx, key, func(*openAIWSLatencyState) bool {
		read = true
		return false
	})
	if !read {
		return openAIWSLatencyState{}, ErrOpenAIPreparedSessionUnavailable
	}
	return state, nil
}

func (session *OpenAIWSLatencySession) preparedState(ctx context.Context) (openAIWSLatencyState, error) {
	if session.initializationErr != nil {
		return openAIWSLatencyState{}, session.initializationErr
	}
	state, err := session.service.readOpenAIPreparedState(ctx, session.key(session.model))
	if err != nil {
		return state, err
	}
	if state.TargetID > 0 && !state.CandidateCommitted && state.PendingUntil <= time.Now().UnixMilli() {
		session.routeDiagnostic(ctx, logger.LevelInfo, "reconnect", "pending_expired", session.model, state)
		session.abandonPreparedCandidate(ctx, state, "pending_expired")
		return session.service.readOpenAIPreparedState(ctx, session.key(session.model))
	}
	return state, nil
}

func (session *OpenAIWSLatencySession) state(ctx context.Context) openAIWSLatencyState {
	if session != nil && session.legacy != nil {
		return session.legacy.state(ctx)
	}
	state, _ := session.preparedState(ctx)
	return state
}

// 并发请求可能已消费池条目；只接受同一已确认线路或同一已独占租约。
func openAIPreparedRouteOwned(current, expected openAIWSLatencyState) bool {
	if expected.UpstreamSessionID == "" || expected.TargetID <= 0 {
		return false
	}
	if current.TargetID == 0 {
		return current.AccountID == expected.TargetID && current.ActiveUpstreamSessionID == expected.UpstreamSessionID
	}
	return current.CandidateCommitted && current.TargetID == expected.TargetID &&
		current.UpstreamSessionID == expected.UpstreamSessionID && current.TargetResultID == expected.TargetResultID &&
		current.CandidateLeaseID == expected.CandidateLeaseID
}

// ApplyUpstreamSessionIdentity 将目标探测会话绑定到用户原对话的出站请求。
// 内部标识只由上游请求构造器读取，不从客户端请求头直接采信；探测、首次接管、
// 后续请求最终发给上游的会话标识必须一致，不能在头部覆盖或身份隔离处理中被重新生成。
// 本方法处理路由身份，不负责自动恢复完整历史；上下文完整性须由接管流程另行保证。
func (session *OpenAIWSLatencySession) ApplyUpstreamSessionIdentity(ctx context.Context, c *gin.Context, account *Account, model string, turns ...int) error {
	if session != nil && session.legacy != nil {
		return session.legacy.ApplyUpstreamSessionIdentity(ctx, c, account, model)
	}
	if c != nil {
		c.Set(openAIWSLatencyUpstreamSessionContextKey, "")
		c.Set(openAIPreparedSessionContextKey, false)
	}
	if !session.enabled(ctx) || c == nil || account == nil {
		return nil
	}
	model = strings.TrimSpace(model)
	if model == "" {
		model = session.model
	}
	c.Set(openAIPreparedSessionContextKey, true)
	if session.initializationErr != nil {
		return session.initializationErr
	}
	state, err := session.service.readOpenAIPreparedState(ctx, session.key(model))
	if err != nil {
		return err
	}
	if session.existingBindingOnly {
		state = openAIExistingPreparedBinding(state)
	}
	turn := 1
	if len(turns) > 0 {
		turn = turns[0]
	}
	recovering, _ := session.recoveryState()
	routeSessionID := ""
	route := openAIWSLatencyTurnRoute{accountID: account.ID}
	defer func() {
		route.sessionID = routeSessionID
		session.mu.Lock()
		classification := session.turns[turn]
		classification.route = &route
		session.turns[turn] = classification
		session.mu.Unlock()
	}()
	if (recovering || state.CandidateCommitted) && state.TargetID == account.ID && state.UpstreamSessionID != "" && (state.PendingUntil > time.Now().UnixMilli() || state.CandidateCommitted) {
		if !state.CandidateCommitted {
			requestedModel := session.upstreamModel
			if model != session.model {
				mapping, _ := session.service.ResolveChannelMappingAndRestrict(ctx, session.groupID, model)
				requestedModel = strings.TrimSpace(mapping.MappedModel)
				if requestedModel == "" {
					requestedModel = model
				}
			}
			if state.TargetPoolKey == openAISessionPoolKey && !session.preparedCandidateMatchesRequest(account, state, requestedModel) {
				session.abandonPreparedCandidate(ctx, state, "candidate_no_longer_eligible")
				return fmt.Errorf("prepared session is no longer eligible")
			}
			if !session.service.commitOpenAIWSLatencyCandidate(ctx, state.TargetPoolKey, state.CandidateLeaseID, state.TargetResultID, account.ID) {
				current, readErr := session.service.readOpenAIPreparedState(ctx, session.key(model))
				if readErr != nil {
					return readErr
				}
				if openAIPreparedRouteOwned(current, state) {
					routeSessionID = state.UpstreamSessionID
					if current.CandidateCommitted {
						route.resultID, route.leaseID = current.TargetResultID, current.CandidateLeaseID
					}
					c.Set(openAIWSLatencyUpstreamSessionContextKey, routeSessionID)
					return nil
				}
				session.poolDiagnostic(ctx, logger.LevelWarn, "openai.session_pool_commit_failed", "candidate_commit_failed",
					zap.Int64("target_account_id", account.ID), zap.String("target_result_id", state.TargetResultID))
				// 选中后过期时尚未发送用户内容，新对话允许按普通线路继续。
				if state.InitialAssignment && session.abandonPreparedCandidate(ctx, state, "candidate_commit_failed") {
					return nil
				}
				return fmt.Errorf("latency candidate ownership is unavailable")
			}
			_, committed := session.service.updateOpenAIWSLatencyState(ctx, session.key(model), func(current *openAIWSLatencyState) bool {
				if current.TargetID != account.ID || current.TargetResultID != state.TargetResultID || current.CandidateLeaseID != state.CandidateLeaseID {
					return false
				}
				current.CandidateCommitted = true
				return true
			})
			if !committed {
				// 更新失败可能返回尝试写入的值，必须重新读取持久化归属。
				currentState, readErr := session.service.readOpenAIPreparedState(ctx, session.key(model))
				if readErr == nil && openAIPreparedRouteOwned(currentState, state) {
					if currentState.CandidateCommitted {
						route.resultID, route.leaseID = currentState.TargetResultID, currentState.CandidateLeaseID
					}
					routeSessionID = state.UpstreamSessionID
					c.Set(openAIWSLatencyUpstreamSessionContextKey, routeSessionID)
					return nil
				}
				session.service.releaseOpenAIWSLatencyCandidate(ctx, state.TargetPoolKey, state.CandidateLeaseID, account.ID, true)
				session.poolDiagnostic(ctx, logger.LevelWarn, "openai.session_pool_commit_failed", "ownership_state_not_updated",
					zap.Int64("target_account_id", account.ID), zap.String("target_result_id", state.TargetResultID))
				return fmt.Errorf("latency candidate ownership could not be recorded")
			}
		}
		routeSessionID = state.UpstreamSessionID
		route.resultID, route.leaseID = state.TargetResultID, state.CandidateLeaseID
	} else if state.AccountID == account.ID {
		routeSessionID = state.ActiveUpstreamSessionID
		if state.TargetID == 0 && routeSessionID == "" {
			routeSessionID = state.UpstreamSessionID
		}
	}
	if routeSessionID == "" {
		if session.existingBindingOnly {
			return nil
		}
		// 调度已经选定普通故障转移线路；发送前更新归属，阻止旧结果覆盖。
		_, saved := session.service.updateOpenAIWSLatencyState(ctx, session.key(model), func(current *openAIWSLatencyState) bool {
			if current.TargetID > 0 || current.AccountID == account.ID || current.AccountID != state.AccountID || current.ActiveUpstreamSessionID != state.ActiveUpstreamSessionID {
				return false
			}
			*current = openAIWSLatencyState{AccountID: account.ID, InitialDecisionMade: true}
			return true
		})
		if !saved {
			// 写入失败时返回值可能是未保存的尝试值；重新读取才能确认线路。
			current, readErr := session.service.readOpenAIPreparedState(ctx, session.key(model))
			if readErr != nil {
				return readErr
			}
			// 未提交的候选只预留待切换会话，原普通线路仍可继续；
			// 候选已提交或活跃绑定已改变时，不能再发送旧线路。
			if current.AccountID != account.ID || current.ActiveUpstreamSessionID != "" || current.CandidateCommitted || (current.TargetID == 0 && current.UpstreamSessionID != "") {
				session.routeDiagnostic(ctx, logger.LevelWarn, "route", "ordinary_route_not_recorded", model, current, zap.Int64("account_id", account.ID))
				return ErrOpenAIPreparedSessionUnavailable
			}
		}
		if state.TargetID > 0 || state.UpstreamSessionID != "" {
			session.routeDiagnostic(ctx, logger.LevelInfo, "route", "upstream_session_not_applied", model, state, zap.Int64("account_id", account.ID))
		}
		return nil
	}
	// 活跃对话持续刷新路由寿命，不让关闭慢切换或长会话导致绑定在两小时后丢失。
	_, refreshed := session.service.updateOpenAIWSLatencyState(ctx, session.key(model), func(current *openAIWSLatencyState) bool {
		if current.CandidateCommitted && current.TargetID == account.ID && current.UpstreamSessionID == routeSessionID {
			return true
		}
		activeSessionID := current.ActiveUpstreamSessionID
		if current.TargetID == 0 && activeSessionID == "" {
			activeSessionID = current.UpstreamSessionID
		}
		return current.AccountID == account.ID && activeSessionID == routeSessionID
	})
	if !refreshed {
		session.routeDiagnostic(ctx, logger.LevelWarn, "route", "active_route_refresh_failed", model, state, zap.Int64("account_id", account.ID))
		return ErrOpenAIPreparedSessionUnavailable
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

func OpenAIWSLatencyUpstreamSessionID(c *gin.Context) string {
	if c == nil {
		return ""
	}
	value, _ := c.Get(openAIWSLatencyUpstreamSessionContextKey)
	sessionID, _ := value.(string)
	return strings.TrimSpace(sessionID)
}

// 只读取备用池路径生成的身份；旧模式不启用新增头部和帧改写。
func OpenAIPreparedSessionID(c *gin.Context) string {
	if c == nil || !c.GetBool(openAIPreparedSessionContextKey) {
		return ""
	}
	return OpenAIWSLatencyUpstreamSessionID(c)
}

// 在所有账号头覆写之后调用，确保探测与接管使用同一条上游粘性线路。
func applyOpenAIPreparedSessionHeaders(c *gin.Context, headers http.Header) {
	if id := OpenAIWSLatencyUpstreamSessionID(c); id != "" {
		names := []string{"session_id", "conversation_id"}
		if OpenAIPreparedSessionID(c) != "" {
			names = append(names, "session-id", "thread-id")
		}
		for _, name := range names {
			headers.Set(name, id)
		}
	}
}

// Prepare checks the unmodified client frame, before gateway affinity recovery
// can strip previous_response_id. A delta-only reconnect must never reach B.
func (session *OpenAIWSLatencySession) Prepare(ctx context.Context, payload []byte) error {
	if session != nil && session.legacy != nil {
		return session.legacy.Prepare(ctx, payload)
	}
	if session != nil && session.existingBindingOnly {
		return nil
	}
	if prepared, err := session.prepareInitialSession(ctx, payload); err != nil || prepared {
		return err
	}
	if !session.enabled(ctx) {
		return nil
	}
	state, err := session.preparedState(ctx)
	if err != nil {
		return err
	}
	if !session.service.settingService.GetOpenAIWSLatencySwitchSettings(ctx).Enabled && !state.InitialAssignment {
		return nil
	}
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
	input := gjson.GetBytes(payload, "input")
	generate := gjson.GetBytes(payload, "generate")
	confirmed := (!generate.Exists() || generate.Bool()) && ((input.IsArray() && len(input.Array()) > 0) || (input.Type == gjson.String && strings.TrimSpace(input.String()) != ""))
	session.setRecovery(true, confirmed)
	logger.FromContext(ctx).Info("openai.ws_latency_reconnect_received", append(session.actor.diagnosticFields(),
		zap.Int("input_items", len(input.Array())), zap.Bool("has_previous_response_id", false),
		zap.Bool("has_tool_output", coverage.HasFunctionCallOutput), zap.Bool("tool_context_complete", coverage.ContextCoversAllCallIDs),
		zap.Bool("context_confirmed", confirmed), zap.String("transport", string(session.requiredTransport)),
		zap.Int64("target_account_id", state.TargetID), zap.String("target_result_id", state.TargetResultID))...)
	session.clearUpstreamAffinity(ctx)
	return nil
}

// PrepareHTTP requests one client retry before the pending route switch. The
// retried request must carry complete input so it can be replayed on another
// account without relying on response state held by the old account.
func (session *OpenAIWSLatencySession) PrepareHTTP(ctx context.Context, payload []byte) (bool, error) {
	if session != nil && session.legacy != nil {
		return session.legacy.PrepareHTTP(ctx, payload)
	}
	if session != nil && session.existingBindingOnly {
		return false, nil
	}
	if prepared, err := session.prepareInitialSession(ctx, payload); err != nil || prepared {
		return false, err
	}
	if !session.enabled(ctx) {
		return false, nil
	}
	state, err := session.preparedState(ctx)
	if err != nil {
		return false, err
	}
	if !session.service.settingService.GetOpenAIWSLatencySwitchSettings(ctx).Enabled && !state.InitialAssignment {
		return false, nil
	}
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
	session.setRecovery(true, true)
	session.clearUpstreamAffinity(ctx)
	logger.FromContext(ctx).Info("openai.ws_latency_reconnect_received", append(session.actor.diagnosticFields(),
		zap.Int("input_items", len(input.Array())), zap.Bool("has_previous_response_id", false),
		zap.Bool("has_tool_output", coverage.HasFunctionCallOutput), zap.Bool("tool_context_complete", coverage.ContextCoversAllCallIDs),
		zap.Bool("context_confirmed", true), zap.String("transport", string(OpenAIUpstreamTransportHTTPSSE)),
		zap.Int64("target_account_id", state.TargetID), zap.String("target_result_id", state.TargetResultID))...)
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
		"target_result_id":  state.TargetResultID,
		"model":             model,
	})
	return NewOpenAIWSClientCloseError(OpenAIWSLatencySwitchCloseCode, "Request terminated", nil)
}

func (session *OpenAIWSLatencySession) BeforeRequest(ctx context.Context, turn int, account *Account, payload []byte, model string) error {
	if session != nil && session.legacy != nil {
		return session.legacy.BeforeRequest(ctx, turn, account, payload, model)
	}
	if !session.enabled(ctx) || session.existingBindingOnly {
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
	recovering := session.recovering
	session.mu.Unlock()
	if !user || account == nil {
		return nil
	}
	state, err := session.service.readOpenAIPreparedState(ctx, session.key(model))
	if err != nil {
		return err
	}
	if session.service.settingService.GetOpenAIWSLatencySwitchSettings(ctx).Enabled && state.AccountID == account.ID && state.TargetID > 0 && state.PendingUntil > time.Now().UnixMilli() && !recovering {
		return session.requestReconnect(ctx, model)
	}
	return nil
}

// SelectTarget 在当前分组的正常模型、隐私、收益、协议、额度和并发检查下选择接管账号。
// 目标账号可以与原账号相同：同一密钥下的另一个快速上游会话也是有效切换目标。
// 必须连同候选的上游会话标识接管，不能仅因选中了账号，就认为复用了探测到的快速线路。
func (session *OpenAIWSLatencySession) SelectTarget(ctx context.Context, excluded map[int64]struct{}, requiredTransport OpenAIUpstreamTransport) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
	if session != nil && session.legacy != nil {
		return session.legacy.SelectTarget(ctx, excluded, requiredTransport)
	}
	if !session.enabled(ctx) {
		return nil, OpenAIAccountScheduleDecision{}, nil
	}
	state, err := session.preparedState(ctx)
	if err != nil {
		return nil, OpenAIAccountScheduleDecision{}, err
	}
	if session.existingBindingOnly {
		state = openAIExistingPreparedBinding(state)
		if state.UpstreamSessionID == "" {
			return nil, OpenAIAccountScheduleDecision{}, nil
		}
		return session.selectPreparedBoundAccount(ctx, state, excluded, requiredTransport)
	}
	recovering, _ := session.recoveryState()
	if state.InitialAssignment || state.CandidateCommitted || (recovering && state.TargetPoolKey == openAISessionPoolKey) || (state.TargetID == 0 && state.UpstreamSessionID != "") {
		return session.selectPreparedBoundAccount(ctx, state, excluded, requiredTransport)
	}
	if !recovering {
		return nil, OpenAIAccountScheduleDecision{}, nil
	}
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

func openAIWSOutputTPS(result *OpenAIForwardResult) (float64, bool) {
	if result == nil || result.Usage.OutputTokens <= 0 || result.Duration.Milliseconds() <= 0 || result.ImageCount > 0 || result.Usage.ImageOutputTokens > 0 {
		return 0, false
	}
	return float64(result.Usage.OutputTokens) * 1000 / float64(result.Duration.Milliseconds()), true
}

func (session *OpenAIWSLatencySession) Observe(ctx context.Context, turn int, account *Account, result *OpenAIForwardResult, turnErr error) {
	if session != nil && session.legacy != nil {
		session.legacy.Observe(ctx, turn, account, result, turnErr)
		return
	}
	if session != nil && session.existingBindingOnly {
		return
	}
	if !session.enabled(ctx) {
		if session != nil {
			fields := append(session.actor.diagnosticFields(), zap.String("stage", "observe"), zap.Int("turn", turn))
			writeOpenAIWSLatencyDiagnostic(ctx, logger.LevelDebug, "feature_disabled", fields...)
		}
		return
	}
	session.mu.Lock()
	classification, exists := session.turns[turn]
	recovering, recoveryConfirmed := session.recovering, session.recoveryConfirmed
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
		zap.Bool("recovering", recovering), zap.Bool("recovery_confirmed", recoveryConfirmed))
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
		if recovering && level == logger.LevelDebug {
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
	case !recovering:
		completionDecision = "not_recovering"
	case !recoveryConfirmed:
		completionDecision = "context_unconfirmed"
	case !classification.user:
		completionDecision = "non_user_request"
	default:
		completionDecision = "request_unsuccessful"
	}
	now := time.Now().UnixMilli()
	if recovering && recoveryConfirmed && classification.user {
		if turnErr != nil || result == nil || !result.SucceededForScheduling() {
			reason = "recovery_request_unsuccessful"
			fields = append(fields, openAISessionPoolErrorFields(turnErr)...)
			if classification.route != nil {
				fields = append(fields, zap.String("target_result_id", classification.route.resultID))
			}
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
		_, completed := session.service.updateOpenAIWSLatencyState(ctx, session.key(classification.model), func(state *openAIWSLatencyState) bool {
			completionState, completionStateRead = *state, true
			completionDecision = "state_update_failed"
			switch {
			case state.TargetID != account.ID:
				completionDecision = "target_account_mismatch"
			case state.UpstreamSessionID == "":
				completionDecision = "upstream_session_missing"
			case state.CandidateLeaseID == "":
				completionDecision = "candidate_lease_missing"
			case !state.CandidateCommitted:
				completionDecision = "candidate_not_committed"
			case classification.route == nil || classification.route.accountID != account.ID || classification.route.sessionID != state.UpstreamSessionID || classification.route.resultID != state.TargetResultID || classification.route.leaseID != state.CandidateLeaseID:
				completionDecision = "sent_route_mismatch"
			}
			if completionDecision != "state_update_failed" {
				return false
			}
			fromAccountID = state.AccountID
			leaseID = state.CandidateLeaseID
			resultID = state.TargetResultID
			upstreamSessionID = state.UpstreamSessionID
			poolModel = state.TargetUpstreamModel
			poolKey = state.TargetPoolKey
			*state = openAIWSLatencyState{AccountID: account.ID, InitialDecisionMade: true, UpstreamSessionID: upstreamSessionID, ActiveUpstreamSessionID: upstreamSessionID, TargetUpstreamModel: poolModel, CooldownUntil: now + openAIWSLatencyCooldown.Milliseconds()}
			return true
		})
		fields = append(fields, zap.Bool("switch_completed", completed))
		if completionStateRead {
			fields = append(fields, zap.Int64("completion_target_account_id", completionState.TargetID),
				zap.String("completion_target_result_id", completionState.TargetResultID))
		}
		if completed {
			completionDecision = "completed"
			level = logger.LevelInfo
			if poolKey == "" {
				if poolModel == "" {
					poolModel = session.upstreamModel
				}
				poolKey = openAIWSLatencyCandidatePoolKey(session.groupID, poolModel, session.requiredTransport)
			}
			session.service.releaseOpenAIWSLatencyCandidate(ctx, poolKey, leaseID, account.ID, true)
			session.setRecovery(false, false)
			session.service.wakeOpenAISessionPool()
			event := "openai.ws_latency_switch_completed"
			if completionState.InitialAssignment {
				event = "openai.session_pool_assignment_completed"
			}
			session.actor.writeEvent(event, map[string]any{
				"from_account_id":   fromAccountID,
				"target_account_id": account.ID,
				"account_id":        account.ID,
				"target_result_id":  resultID,
				"model":             session.model,
				"transport":         string(session.requiredTransport),
			})
		}
	}
	if !settings.Enabled || account.Platform != PlatformOpenAI {
		reason = "switch_disabled_or_account_ineligible"
		// 首次分配独立于慢切换开关；仍记录对话已使用账号，防止下一轮被当作新对话。
		session.service.updateOpenAIWSLatencyState(ctx, session.key(classification.model), func(state *openAIWSLatencyState) bool {
			if state.AccountID > 0 || state.TargetID > 0 || state.ActiveUpstreamSessionID != "" {
				return false
			}
			*state = openAIWSLatencyState{AccountID: account.ID}
			return true
		})
		return
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
		if classification.route != nil && (state.AccountID != account.ID || state.ActiveUpstreamSessionID != classification.route.sessionID) {
			blocked, reason = true, "stale_route_result"
			return false
		}
		if state.AccountID != account.ID {
			// 旧请求晚于新线路完成时返回，不能覆盖已经建立的上游会话映射。
			if state.AccountID > 0 || state.TargetID > 0 {
				blocked = true
				reason = "stale_route_result"
				return false
			}
			*state = openAIWSLatencyState{AccountID: account.ID}
		}
		if state.CooldownUntil > now || state.PendingUntil > now || state.ProbeUntil > now || state.CandidateCommitted {
			blocked = true
			switch {
			case state.PendingUntil > now || state.CandidateCommitted:
				reason = "switch_pending"
			case state.ProbeUntil > now:
				reason = "candidate_reservation_in_progress"
			default:
				reason = "cooldown_active"
			}
			return false
		}
		clearOpenAISessionPoolPending(state)
		state.ProbeID, state.ProbeUntil = "", 0
		if !slow {
			state.SlowCount = 0
			return true
		}
		state.SlowCount++
		if state.SlowCount >= settings.Consecutive {
			state.SlowCount = 0
			state.ProbeID, state.ProbeUntil = probeID, now+(5*time.Second).Milliseconds()
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
		reason = "shared_pool_empty_or_ineligible"
		if session.assignPreparedCandidate(ctx, classification.model, account.ID, false) {
			reason = "shared_candidate_reserved"
		}
		session.service.updateOpenAIWSLatencyState(ctx, session.key(classification.model), func(current *openAIWSLatencyState) bool {
			if current.ProbeID != probeID {
				return false
			}
			current.ProbeID, current.ProbeUntil = "", 0
			return true
		})
	}
}

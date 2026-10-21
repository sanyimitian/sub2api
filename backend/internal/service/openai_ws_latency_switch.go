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
	AccountID           int64  `json:"account_id"`
	SlowCount           int    `json:"slow_count"`
	ProbeID             string `json:"probe_id,omitempty"`
	ProbeUntil          int64  `json:"probe_until,omitempty"`
	TargetID            int64  `json:"target_id,omitempty"`
	TargetResultID      string `json:"target_result_id,omitempty"`
	TargetUpstreamModel string `json:"target_upstream_model,omitempty"`
	TargetPoolKey       string `json:"target_pool_key,omitempty"`
	UpstreamSessionID   string `json:"upstream_session_id,omitempty"`
	PendingUntil        int64  `json:"pending_until,omitempty"`
	ReconnectFrom       string `json:"reconnect_from,omitempty"`
	CooldownUntil       int64  `json:"cooldown_until,omitempty"`
	CandidateLeaseID    string `json:"candidate_lease_id,omitempty"`
	CandidateCommitted  bool   `json:"candidate_committed,omitempty"`
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
	ResultID          string `json:"result_id,omitempty"`
	AccountID         int64  `json:"account_id"`
	UpstreamModel     string `json:"upstream_model,omitempty"`
	UpstreamSessionID string `json:"upstream_session_id,omitempty"`
	FirstTokenMS      int    `json:"first_token_ms"`
	ExpiresAt         int64  `json:"expires_at"`
	LeaseID           string `json:"lease_id,omitempty"`
	LeaseUntil        int64  `json:"lease_until,omitempty"`
	Committed         bool   `json:"committed,omitempty"`
}

const openAIWSLatencyUpstreamSessionContextKey = "openai_ws_latency_upstream_session_id"

type openAIWSLatencyCandidatePool struct {
	ProbeID    string                             `json:"probe_id,omitempty"`
	ProbeUntil int64                              `json:"probe_until,omitempty"`
	Candidates []openAIWSLatencyReusableCandidate `json:"candidates,omitempty"`
}

func (s *OpenAIGatewayService) updateOpenAIWSLatencyState(ctx context.Context, key string, mutate func(*openAIWSLatencyState) bool) (openAIWSLatencyState, bool) {
	cache, ok := s.cache.(OpenAIWSLatencyCache)
	if !ok || key == "" {
		logOpenAIWSLatencyCacheFailure(ctx, key, "access", "state_cache_unavailable", 0, nil)
		return openAIWSLatencyState{}, false
	}
	cacheCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	defer cancel()
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
		if !mutate(&state) {
			return state, false
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
		updated, err := cache.CompareAndSwapOpenAIWSLatencyState(cacheCtx, key, raw, next, openAIWSLatencyStateTTL)
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

func (s *OpenAIGatewayService) reserveOpenAIWSLatencyCandidate(ctx context.Context, poolKey string, _ int64, leaseID string, leaseSeconds int, eligible map[int64]struct{}, expectedUpstreamModels map[int64]string) (openAIWSLatencyReusableCandidate, bool) {
	var selected openAIWSLatencyReusableCandidate
	_, updated := s.updateOpenAIWSLatencyCandidatePool(ctx, poolKey, func(pool *openAIWSLatencyCandidatePool) bool {
		now := time.Now().UnixMilli()
		pruneOpenAIWSLatencyCandidates(pool, now)
		selectedIndex := -1
		for i, candidate := range pool.Candidates {
			if candidate.FirstTokenMS < 0 || candidate.FirstTokenMS >= openAIWSLatencyCandidateMaxFirstTokenMS || candidate.Committed || (candidate.LeaseID != "" && candidate.LeaseUntil > now) || !openAIWSLatencyCandidateAllowed(candidate, eligible, expectedUpstreamModels) {
				continue
			}
			if selectedIndex < 0 || candidate.FirstTokenMS < pool.Candidates[selectedIndex].FirstTokenMS {
				selectedIndex = i
			}
		}
		if selectedIndex < 0 {
			return len(pool.Candidates) > 0
		}
		selected = pool.Candidates[selectedIndex]
		selected.LeaseID = leaseID
		selected.LeaseUntil = now + (time.Duration(leaseSeconds) * time.Second).Milliseconds()
		pool.Candidates[selectedIndex] = selected
		return true
	})
	return selected, updated && selected.AccountID > 0
}

func (s *OpenAIGatewayService) addOpenAIWSLatencyCandidates(ctx context.Context, poolKey string, results []openAIWSLatencyProbeResult, reuseSeconds int, leaseAccountID int64, leaseID string, leaseSeconds int, preferredResultID ...string) (openAIWSLatencyReusableCandidate, bool) {
	var leased openAIWSLatencyReusableCandidate
	preferred := ""
	if len(preferredResultID) > 0 {
		preferred = preferredResultID[0]
	}
	_, updated := s.updateOpenAIWSLatencyCandidatePool(ctx, poolKey, func(pool *openAIWSLatencyCandidatePool) bool {
		now := time.Now().UnixMilli()
		pruneOpenAIWSLatencyCandidates(pool, now)
		for _, result := range results {
			if !result.ok || result.accountID <= 0 || result.ms < 0 || result.ms >= openAIWSLatencyCandidateMaxFirstTokenMS || result.upstreamSessionID == "" || strings.TrimSpace(result.upstreamModel) == "" {
				continue
			}
			resultID := result.resultID
			if resultID == "" {
				resultID = uuid.NewString()
			}
			candidate := openAIWSLatencyReusableCandidate{ResultID: resultID, AccountID: result.accountID, UpstreamModel: result.upstreamModel, UpstreamSessionID: result.upstreamSessionID, FirstTokenMS: result.ms, ExpiresAt: now + (time.Duration(reuseSeconds) * time.Second).Milliseconds()}
			foundIndex := -1
			for i := range pool.Candidates {
				if pool.Candidates[i].ResultID != candidate.ResultID {
					continue
				}
				foundIndex = i
				break
			}
			if foundIndex < 0 {
				pool.Candidates = append(pool.Candidates, candidate)
				continue
			}
			existing := pool.Candidates[foundIndex]
			if existing.LeaseID != "" && existing.LeaseUntil > now {
				continue
			}
			pool.Candidates[foundIndex] = candidate
		}
		for i := range pool.Candidates {
			if pool.Candidates[i].AccountID != leaseAccountID || leaseAccountID <= 0 || (pool.Candidates[i].LeaseID != "" && pool.Candidates[i].LeaseUntil > now) || pool.Candidates[i].Committed {
				continue
			}
			if preferred != "" && pool.Candidates[i].ResultID != preferred {
				continue
			}
			pool.Candidates[i].LeaseID = leaseID
			pool.Candidates[i].LeaseUntil = now + (time.Duration(leaseSeconds) * time.Second).Milliseconds()
			leased = pool.Candidates[i]
			break
		}
		return true
	})
	return leased, updated && leased.AccountID > 0
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

func (s *OpenAIGatewayService) claimOpenAIWSLatencyProbe(ctx context.Context, poolKey string, _ int64, probeID string, probeWindow time.Duration, eligible map[int64]struct{}, expectedUpstreamModels map[int64]string) bool {
	claimed := false
	_, updated := s.updateOpenAIWSLatencyCandidatePool(ctx, poolKey, func(pool *openAIWSLatencyCandidatePool) bool {
		now := time.Now().UnixMilli()
		pruneOpenAIWSLatencyCandidates(pool, now)
		if pool.ProbeUntil > now {
			return false
		}
		for _, candidate := range pool.Candidates {
			if candidate.ExpiresAt > now && openAIWSLatencyCandidateAllowed(candidate, eligible, expectedUpstreamModels) {
				return false
			}
		}
		pool.ProbeID = probeID
		pool.ProbeUntil = now + probeWindow.Milliseconds()
		claimed = true
		return true
	})
	return claimed && updated
}

func (s *OpenAIGatewayService) releaseOpenAIWSLatencyProbe(ctx context.Context, poolKey, probeID string) {
	_, _ = s.updateOpenAIWSLatencyCandidatePool(ctx, poolKey, func(pool *openAIWSLatencyCandidatePool) bool {
		if pool.ProbeID != probeID {
			return false
		}
		pool.ProbeID, pool.ProbeUntil = "", 0
		return true
	})
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
	skip := func(reason string) {
		logOpenAIWSLatencySessionSkipped(ctx, groupID, apiKey, model, OpenAIUpstreamTransportResponsesWebsocketV2Ingress, reason)
	}
	if s == nil || apiKey == nil || s.settingService == nil {
		skip("session_dependency_missing")
		return nil
	}
	if !s.settingService.GetOpenAIWSLatencySwitchSettings(ctx).Enabled {
		skip("feature_disabled")
		return nil
	}
	if _, ok := s.cache.(OpenAIWSLatencyCache); !ok {
		skip("state_cache_unavailable")
		return nil
	}
	metadata := openAIWSExecutionTurnMetadata(c, payload)
	if resolveOpenAIWSExecutionLane(c, payload) != "" || (metadata.RequestKind != "" && metadata.RequestKind != openAIWSRequestKindTurn && metadata.RequestKind != openAIWSRequestKindPrewarm) {
		skip("execution_lane_or_kind_excluded")
		return nil
	}
	scope, _ := resolveOpenAIWSExecutionScope(c, payload, apiKey.ID)
	if scope == "" {
		skip("session_scope_missing")
		return nil
	}
	return newOpenAIWSLatencySession(ctx, s, groupID, apiKey, scope, sessionHash, model, upstreamModel, requiredCapability, OpenAIUpstreamTransportResponsesWebsocketV2Ingress, payload)
}

// NewOpenAIHTTPResponsesLatencySession tracks HTTP SSE requests by their stable
// session affinity key. Each HTTP request is a new connection, so the reconnect
// marker persists the forced retry across requests.
func (s *OpenAIGatewayService) NewOpenAIHTTPResponsesLatencySession(ctx context.Context, groupID *int64, apiKey *APIKey, sessionHash, model, upstreamModel string, requiredCapability OpenAIEndpointCapability, payload []byte) *OpenAIWSLatencySession {
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
	if !s.settingService.GetOpenAIWSLatencySwitchSettings(ctx).Enabled {
		skip("feature_disabled")
		return nil
	}
	if _, ok := s.cache.(OpenAIWSLatencyCache); !ok {
		skip("state_cache_unavailable")
		return nil
	}
	scope, _ := deriveOpenAISessionHashes(fmt.Sprintf("openai_http_latency:%d:%s", apiKey.ID, strings.TrimSpace(sessionHash)))
	return newOpenAIWSLatencySession(ctx, s, groupID, apiKey, scope, sessionHash, model, upstreamModel, requiredCapability, OpenAIUpstreamTransportHTTPSSE, payload)
}

func newOpenAIWSLatencySession(ctx context.Context, s *OpenAIGatewayService, groupID *int64, apiKey *APIKey, scope, sessionHash, model, upstreamModel string, requiredCapability OpenAIEndpointCapability, requiredTransport OpenAIUpstreamTransport, payload []byte) *OpenAIWSLatencySession {
	actor := openAIWSLatencyActor{userID: apiKey.UserID, apiKeyID: apiKey.ID, sessionID: scope}
	actor.captureRequest(ctx, groupID)
	if apiKey.User != nil {
		actor.username = apiKey.User.Username
		actor.userEmail = apiKey.User.Email
	}
	kind := openAIWSLatencyRequestKind(payload)
	return &OpenAIWSLatencySession{service: s, actor: actor, groupID: groupID, scope: scope, sessionHash: sessionHash, model: model, upstreamModel: upstreamModel, connectionID: uuid.NewString(), requiredCapability: requiredCapability, requiredTransport: requiredTransport, turns: map[int]openAIWSLatencyTurn{1: {model: model, user: kind == "user_message", kind: kind}}}
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

// ApplyUpstreamSessionIdentity 将目标探测会话绑定到用户原对话的出站请求。
// 内部标识只由上游请求构造器读取，不从客户端请求头直接采信；探测、首次接管、
// 后续请求最终发给上游的会话标识必须一致，不能在头部覆盖或身份隔离处理中被重新生成。
// 本方法处理路由身份，不负责自动恢复完整历史；上下文完整性须由接管流程另行保证。
func (session *OpenAIWSLatencySession) ApplyUpstreamSessionIdentity(ctx context.Context, c *gin.Context, account *Account, model string) error {
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
		return nil
	}
	c.Set(openAIWSLatencyUpstreamSessionContextKey, routeSessionID)
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
	return NewOpenAIWSClientCloseError(OpenAIWSLatencySwitchCloseCode, "Request terminated", nil)
}

func (session *OpenAIWSLatencySession) BeforeRequest(ctx context.Context, turn int, account *Account, payload []byte, model string) error {
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

// SelectTarget 在当前分组的正常模型、隐私、收益、协议、额度和并发检查下选择接管账号。
// 目标账号可以与原账号相同：同一密钥下的另一个快速上游会话也是有效切换目标。
// 必须连同候选的上游会话标识接管，不能仅因选中了账号，就认为复用了探测到的快速线路。
func (session *OpenAIWSLatencySession) SelectTarget(ctx context.Context, excluded map[int64]struct{}, requiredTransport OpenAIUpstreamTransport) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
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
	defer func() { writeOpenAIWSLatencyDiagnostic(ctx, level, reason, fields...) }()
	if !exists {
		return
	}
	if account == nil {
		reason = "account_missing"
		return
	}
	fields = append(fields, zap.Int64("account_id", account.ID))
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
		_, completed := session.service.updateOpenAIWSLatencyState(ctx, session.key(session.model), func(state *openAIWSLatencyState) bool {
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
		if completed {
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

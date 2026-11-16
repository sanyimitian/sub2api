package service

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
)

const OpsQQWebhookPath = "/api/v1/ops/qq-bot/webhook"
const OpsQQWebhookBodyLimit = 128 * 1024

var (
	ErrOpsQQWebhookUnavailable  = errors.New("save QQ official AppID and AppSecret before configuring the callback")
	ErrOpsQQWebhookUnauthorized = errors.New("invalid QQ callback authentication")
	ErrOpsQQWebhookPayload      = errors.New("invalid QQ callback payload")
)

type OpsQQDiscoveredGroup struct {
	GroupOpenID string    `json:"group_openid"`
	LastSeenAt  time.Time `json:"last_seen_at"`
	EventType   string    `json:"event_type"`
}

type OpsQQWebhookStatus struct {
	AppID        string                 `json:"app_id"`
	CallbackPath string                 `json:"callback_path"`
	Ready        bool                   `json:"ready"`
	LastEventAt  *time.Time             `json:"last_event_at,omitempty"`
	Groups       []OpsQQDiscoveredGroup `json:"groups"`
}

type opsQQWebhookState struct {
	LastEventAt *time.Time             `json:"last_event_at,omitempty"`
	Groups      []OpsQQDiscoveredGroup `json:"groups"`
}

func opsQQWebhookKey(cfg *OpsQQNotificationConfig) string {
	key := sha256.Sum256([]byte(cfg.AppID + "\x00" + cfg.AppSecret))
	return "ops_qq_webhook_" + hex.EncodeToString(key[:16])
}

func opsQQWebhookReady(cfg *OpsQQNotificationConfig) bool {
	return cfg.provider() == opsQQProviderOfficial && cfg.AppID != "" && cfg.AppSecret != ""
}

func (s *OpsService) readQQWebhookState(ctx context.Context, cfg *OpsQQNotificationConfig) (*opsQQWebhookState, error) {
	state := &opsQQWebhookState{Groups: []OpsQQDiscoveredGroup{}}
	raw, err := s.settingRepo.GetValue(ctx, opsQQWebhookKey(cfg))
	if errors.Is(err, ErrSettingNotFound) {
		return state, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(raw), state); err != nil {
		return nil, errors.New("invalid QQ callback state")
	}
	if state.Groups == nil {
		state.Groups = []OpsQQDiscoveredGroup{}
	}
	return state, nil
}

func (s *OpsService) GetQQWebhookStatus(ctx context.Context) (*OpsQQWebhookStatus, error) {
	status := &OpsQQWebhookStatus{CallbackPath: OpsQQWebhookPath, Groups: []OpsQQDiscoveredGroup{}}
	cfg, err := s.getQQNotificationConfig(ctx)
	if err != nil {
		return nil, err
	}
	status.Ready = opsQQWebhookReady(cfg)
	status.AppID = cfg.AppID
	if !status.Ready {
		return status, nil
	}
	s.qqWebhookMu.Lock()
	defer s.qqWebhookMu.Unlock()
	state, err := s.readQQWebhookState(ctx, cfg)
	if err != nil {
		return nil, err
	}
	status.Groups, status.LastEventAt = state.Groups, state.LastEventAt
	return status, nil
}

// QQ derives its Ed25519 seed by repeating Bot Secret and taking 32 bytes.
// https://bot.q.qq.com/wiki/develop/api-v2/dev-prepare/interface-framework/sign.html
func opsQQWebhookPrivateKey(secret string) ed25519.PrivateKey {
	if secret == "" {
		return nil
	}
	seed := []byte(secret)
	for len(seed) < ed25519.SeedSize {
		seed = append(seed, seed...)
	}
	return ed25519.NewKeyFromSeed(seed[:ed25519.SeedSize])
}

func verifyOpsQQWebhook(key ed25519.PrivateKey, body []byte, timestamp, signature string, now time.Time) bool {
	if len(timestamp) == 0 || len(timestamp) > 20 || strings.Trim(timestamp, "0123456789") != "" {
		return false
	}
	seconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil || seconds < now.Add(-10*time.Minute).Unix() || seconds > now.Add(time.Minute).Unix() {
		return false
	}
	if len(signature) != ed25519.SignatureSize*2 {
		return false
	}
	sig, err := hex.DecodeString(signature)
	if err != nil {
		return false
	}
	message := append([]byte(timestamp), body...)
	return ed25519.Verify(key.Public().(ed25519.PublicKey), message, sig)
}

// Acceptance is independent of notification Enabled, so groups can be discovered
// before enabling alerts. Chat contents are neither stored nor sent.
func (s *OpsService) HandleQQWebhook(ctx context.Context, appID, timestamp, signature string, body []byte) (map[string]any, error) {
	if len(body) == 0 || len(body) > OpsQQWebhookBodyLimit {
		return nil, ErrOpsQQWebhookPayload
	}
	cfg, err := s.getQQNotificationConfig(ctx)
	if err != nil {
		return nil, err
	}
	if !opsQQWebhookReady(cfg) {
		return nil, ErrOpsQQWebhookUnavailable
	}
	if appID != cfg.AppID {
		return nil, ErrOpsQQWebhookUnauthorized
	}
	var payload struct {
		Op   *int            `json:"op"`
		Type string          `json:"t"`
		Data json.RawMessage `json:"d"`
	}
	if json.Unmarshal(body, &payload) != nil || payload.Op == nil {
		return nil, ErrOpsQQWebhookPayload
	}
	key := opsQQWebhookPrivateKey(cfg.AppSecret)
	now := time.Now().UTC()
	// The initial op=13 challenge has X-Bot-Appid but no signature headers.
	// It signs event_ts + plain_token and never changes discovery state.
	if *payload.Op == 13 {
		if (timestamp != "" || signature != "") && !verifyOpsQQWebhook(key, body, timestamp, signature, now) {
			return nil, ErrOpsQQWebhookUnauthorized
		}
		var challenge struct {
			PlainToken string `json:"plain_token"`
			EventTS    string `json:"event_ts"`
		}
		// Restrict the challenge to token characters: never sign arbitrary JSON that
		// could be replayed as an authenticated event using this same key.
		if json.Unmarshal(payload.Data, &challenge) != nil || challenge.PlainToken == "" || len(challenge.PlainToken) > 1024 || strings.Trim(challenge.PlainToken, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-") != "" || len(challenge.EventTS) > 20 {
			return nil, ErrOpsQQWebhookPayload
		}
		seconds, err := strconv.ParseInt(challenge.EventTS, 10, 64)
		if err != nil || seconds <= 0 || strings.Trim(challenge.EventTS, "0123456789") != "" {
			return nil, ErrOpsQQWebhookPayload
		}
		sig := ed25519.Sign(key, []byte(challenge.EventTS+challenge.PlainToken))
		return map[string]any{"plain_token": challenge.PlainToken, "signature": hex.EncodeToString(sig)}, nil
	}
	if !verifyOpsQQWebhook(key, body, timestamp, signature, now) {
		return nil, ErrOpsQQWebhookUnauthorized
	}
	if *payload.Op != 0 {
		return nil, ErrOpsQQWebhookPayload
	}
	ack := map[string]any{"op": 12, "d": 0}
	switch payload.Type {
	case "GROUP_AT_MESSAGE_CREATE", "GROUP_ADD_ROBOT", "GROUP_DEL_ROBOT":
	default:
		return ack, nil
	}
	var event struct {
		GroupOpenID string `json:"group_openid"`
	}
	if json.Unmarshal(payload.Data, &event) != nil || event.GroupOpenID == "" || len(event.GroupOpenID) > 128 || strings.Trim(event.GroupOpenID, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-") != "" {
		return nil, ErrOpsQQWebhookPayload
	}
	s.qqWebhookMu.Lock()
	defer s.qqWebhookMu.Unlock()
	state, err := s.readQQWebhookState(ctx, cfg)
	if err != nil {
		return nil, err
	}
	groups := make([]OpsQQDiscoveredGroup, 0, len(state.Groups)+1)
	if payload.Type != "GROUP_DEL_ROBOT" {
		groups = append(groups, OpsQQDiscoveredGroup{GroupOpenID: event.GroupOpenID, LastSeenAt: now, EventType: payload.Type})
	}
	for _, group := range state.Groups {
		if group.GroupOpenID != event.GroupOpenID && len(groups) < 50 {
			groups = append(groups, group)
		}
	}
	state.Groups, state.LastEventAt = groups, &now
	raw, err := json.Marshal(state)
	if err != nil {
		return nil, err
	}
	if err := s.settingRepo.Set(ctx, opsQQWebhookKey(cfg), string(raw)); err != nil {
		return nil, err
	}
	return ack, nil
}

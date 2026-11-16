package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Official credentials are only sent to Tencent's fixed HTTPS endpoint.
const opsQQOfficialAPIURL = "https://api.bot.qq.com"

type opsQQOfficialSender struct {
	client      *http.Client
	mu          sync.Mutex
	refreshing  chan struct{}
	credentials [32]byte
	token       string
	refreshAt   time.Time
}

func validateOpsQQOfficialConfig(cfg *OpsQQNotificationConfig) error {
	if cfg.AppID != "" || cfg.Enabled {
		if len(cfg.AppID) == 0 || len(cfg.AppID) > 32 || strings.Trim(cfg.AppID, "0123456789") != "" {
			return errors.New("QQ official AppID must contain only digits")
		}
	}
	if cfg.GroupOpenID != "" || cfg.Enabled {
		if len(cfg.GroupOpenID) == 0 || len(cfg.GroupOpenID) > 128 || strings.Trim(cfg.GroupOpenID, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-") != "" {
			return errors.New("invalid QQ group OpenID; use the group_openid from a bot event")
		}
	}
	if cfg.Enabled && cfg.AppSecret == "" {
		return errors.New("QQ official AppSecret is required")
	}
	return nil
}

func (s *opsQQOfficialSender) httpClient() *http.Client {
	client := &http.Client{Timeout: 8 * time.Second}
	if s.client != nil {
		*client = *s.client
	}
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	return client
}

// The cache is scoped to this service and keyed by both credentials, so rotating
// an AppSecret cannot reuse an old token. Concurrent senders share one refresh;
// waiting callers can still honor their own cancellation/deadline.
func (s *opsQQOfficialSender) accessToken(ctx context.Context, cfg *OpsQQNotificationConfig) (string, error) {
	key := sha256.Sum256([]byte(cfg.AppID + "\x00" + cfg.AppSecret))
	for {
		if err := ctx.Err(); err != nil {
			return "", errors.New("QQ official token request canceled or timed out")
		}
		s.mu.Lock()
		if s.credentials == key && s.token != "" && time.Now().Before(s.refreshAt) {
			token := s.token
			s.mu.Unlock()
			return token, nil
		}
		if pending := s.refreshing; pending != nil {
			s.mu.Unlock()
			select {
			case <-ctx.Done():
				return "", errors.New("QQ official token request canceled or timed out")
			case <-pending:
				continue
			}
		}
		s.refreshing = make(chan struct{})
		s.mu.Unlock()

		started := time.Now()
		token, lifetime, err := s.fetchToken(ctx, cfg)
		s.mu.Lock()
		if err == nil {
			// Refresh within Tencent's 60-second overlap window. Short-lived
			// tokens retain half their lifetime instead of expiring immediately.
			margin := min(30*time.Second, lifetime/2)
			s.credentials, s.token = key, token
			s.refreshAt = started.Add(lifetime - margin)
		}
		close(s.refreshing)
		s.refreshing = nil
		s.mu.Unlock()
		return token, err
	}
}

func (s *opsQQOfficialSender) invalidateToken(cfg *OpsQQNotificationConfig, token string) {
	key := sha256.Sum256([]byte(cfg.AppID + "\x00" + cfg.AppSecret))
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.credentials == key && s.token == token {
		s.token = ""
		s.refreshAt = time.Time{}
	}
}

func (s *opsQQOfficialSender) fetchToken(ctx context.Context, cfg *OpsQQNotificationConfig) (string, time.Duration, error) {
	body, err := json.Marshal(map[string]string{"appId": cfg.AppID, "clientSecret": cfg.AppSecret})
	if err != nil {
		return "", 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, opsQQOfficialAPIURL+"/app/getAppAccessToken", bytes.NewReader(body))
	if err != nil {
		return "", 0, errors.New("invalid QQ official token request")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.httpClient().Do(req)
	if err != nil {
		return "", 0, errors.New("QQ official token connection failed or timed out")
	}
	defer resp.Body.Close()
	var result struct {
		AccessToken string      `json:"access_token"`
		ExpiresIn   json.Number `json:"expires_in"`
		Code        int         `json:"code"`
		ErrCode     int         `json:"errcode"`
	}
	decodeErr := json.NewDecoder(io.LimitReader(resp.Body, 16*1024)).Decode(&result)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || result.Code != 0 || result.ErrCode != 0 {
		return "", 0, fmt.Errorf("QQ official token request failed (HTTP %d, code %d); check AppID, AppSecret and IP allowlist", resp.StatusCode, firstNonzeroQQCode(result.Code, result.ErrCode))
	}
	lifetime, lifetimeErr := result.ExpiresIn.Int64()
	if decodeErr != nil || lifetimeErr != nil || lifetime <= 0 || lifetime > 86400 || result.AccessToken == "" || len(result.AccessToken) > 4096 || strings.ContainsAny(result.AccessToken, "\r\n") {
		return "", 0, errors.New("invalid QQ official token response")
	}
	return result.AccessToken, time.Duration(lifetime) * time.Second, nil
}

func firstNonzeroQQCode(code, fallback int) int {
	if code != 0 {
		return code
	}
	return fallback
}

func (s *opsQQOfficialSender) send(ctx context.Context, cfg *OpsQQNotificationConfig, message string) error {
	if err := validateOpsQQConfig(cfg); err != nil {
		return err
	}
	if cfg.AppID == "" || cfg.AppSecret == "" || cfg.GroupOpenID == "" {
		return errors.New("QQ official notification requires AppID, AppSecret and group OpenID")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if len(message) > 12000 {
		message = truncateString(message, 12000)
	}
	body, err := json.Marshal(map[string]any{"msg_type": 0, "content": message})
	if err != nil {
		return err
	}
	for attempt := 0; attempt < 2; attempt++ {
		token, err := s.accessToken(ctx, cfg)
		if err != nil {
			return err
		}
		status, err := s.postMessage(ctx, cfg, token, body)
		if status == http.StatusUnauthorized {
			s.invalidateToken(cfg, token)
			if attempt == 0 {
				continue
			}
		}
		return err
	}
	return errors.New("QQ official authentication failed")
}

func (s *opsQQOfficialSender) postMessage(ctx context.Context, cfg *OpsQQNotificationConfig, token string, body []byte) (int, error) {
	// No msg_id/event_id: alerts are proactive messages, not replies to users.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, opsQQOfficialAPIURL+"/v2/groups/"+cfg.GroupOpenID+"/messages", bytes.NewReader(body))
	if err != nil {
		return 0, errors.New("invalid QQ official message request")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "QQBot "+token)
	resp, err := s.httpClient().Do(req)
	if err != nil {
		return 0, errors.New("QQ official bot connection failed or timed out")
	}
	defer resp.Body.Close()
	var result struct {
		ID   string `json:"id"`
		Code int    `json:"code"`
	}
	decodeErr := json.NewDecoder(io.LimitReader(resp.Body, 16*1024)).Decode(&result)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || result.Code != 0 {
		return resp.StatusCode, opsQQOfficialMessageError(resp.StatusCode, result.Code)
	}
	if decodeErr != nil || result.ID == "" {
		return resp.StatusCode, errors.New("invalid QQ official bot response")
	}
	return resp.StatusCode, nil
}

// Only report known hints and numeric codes, never response text which may
// contain message content or credentials. The alert log persists these errors.
func opsQQOfficialMessageError(status, code int) error {
	hint := "check bot settings and permissions"
	switch code {
	case 40034105:
		hint = "allow proactive messages for this bot in the target group"
	case 40034100:
		hint = "proactive message rate limit reached; retry later"
	case 40034101, 40054003:
		hint = "add the bot to the target group and verify group OpenID"
	case 40054002:
		hint = "the bot is muted in the target group"
	case 40054010:
		hint = "message URLs are not allowed; check the bot URL allowlist"
	}
	return fmt.Errorf("QQ official bot returned HTTP %d (code %d): %s", status, code, hint)
}

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const opsQQConfigKey = "ops_qq_notification_config"

type OpsQQNotificationConfig struct {
	Enabled               bool   `json:"enabled"`
	APIURL                string `json:"api_url"`
	GroupID               string `json:"group_id"`
	AccessToken           string `json:"access_token,omitempty"`
	AccessTokenConfigured bool   `json:"access_token_configured"`
}

type OpsQQNotificationUpdate struct {
	Enabled bool   `json:"enabled"`
	APIURL  string `json:"api_url"`
	GroupID string `json:"group_id"`
	// Omitted/empty preserves the existing token. Clearing is explicit.
	AccessToken      string `json:"access_token"`
	ClearAccessToken bool   `json:"clear_access_token"`
}

func (s *OpsService) getQQNotificationConfig(ctx context.Context) (*OpsQQNotificationConfig, error) {
	cfg := &OpsQQNotificationConfig{}
	if s == nil || s.settingRepo == nil {
		return cfg, nil
	}
	raw, err := s.settingRepo.GetValue(ctx, opsQQConfigKey)
	if errors.Is(err, ErrSettingNotFound) {
		return cfg, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(raw), cfg); err != nil {
		return nil, errors.New("invalid QQ notification settings")
	}
	return cfg, nil
}

func redactOpsQQConfig(cfg *OpsQQNotificationConfig) *OpsQQNotificationConfig {
	copy := *cfg
	copy.AccessTokenConfigured = copy.AccessToken != ""
	copy.AccessToken = ""
	return &copy
}

func (s *OpsService) GetQQNotificationConfig(ctx context.Context) (*OpsQQNotificationConfig, error) {
	cfg, err := s.getQQNotificationConfig(ctx)
	if err != nil {
		return nil, err
	}
	return redactOpsQQConfig(cfg), nil
}

func validateOpsQQConfig(cfg *OpsQQNotificationConfig) error {
	if cfg.APIURL != "" || cfg.Enabled {
		u, err := url.Parse(cfg.APIURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return errors.New("QQ API URL must be an HTTP(S) base URL without credentials, query or fragment")
		}
	}
	if cfg.GroupID != "" || cfg.Enabled {
		id, err := strconv.ParseInt(cfg.GroupID, 10, 64)
		if err != nil || id <= 0 {
			return errors.New("QQ group ID must be a positive integer")
		}
	}
	if len(cfg.AccessToken) > 4096 || strings.ContainsAny(cfg.AccessToken, "\r\n") {
		return errors.New("invalid QQ access token")
	}
	return nil
}

func (s *OpsService) UpdateQQNotificationConfig(ctx context.Context, req *OpsQQNotificationUpdate) (*OpsQQNotificationConfig, error) {
	if s == nil || s.settingRepo == nil || req == nil {
		return nil, errors.New("settings unavailable")
	}
	cfg, err := s.getQQNotificationConfig(ctx)
	if err != nil {
		return nil, err
	}
	cfg.Enabled = req.Enabled
	cfg.APIURL = strings.TrimRight(strings.TrimSpace(req.APIURL), "/")
	cfg.GroupID = strings.TrimSpace(req.GroupID)
	if req.ClearAccessToken {
		cfg.AccessToken = ""
	}
	if req.AccessToken != "" {
		cfg.AccessToken = strings.TrimSpace(req.AccessToken)
	}
	if err := validateOpsQQConfig(cfg); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	if err := s.settingRepo.Set(ctx, opsQQConfigKey, string(raw)); err != nil {
		return nil, err
	}
	return redactOpsQQConfig(cfg), nil
}

// OneBot v11 HTTP API. Private addresses are intentional: administrators commonly
// run the bot on the same server. Redirects are forbidden to keep the token at
// the configured endpoint; responses are bounded and never echoed into logs.
func sendOpsQQMessage(ctx context.Context, cfg *OpsQQNotificationConfig, message string) error {
	if err := validateOpsQQConfig(cfg); err != nil {
		return err
	}
	if cfg.APIURL == "" || cfg.GroupID == "" {
		return errors.New("QQ notification is not configured")
	}
	groupID, _ := strconv.ParseInt(cfg.GroupID, 10, 64)
	if len(message) > 12000 {
		message = truncateString(message, 12000)
	}
	body, err := json.Marshal(map[string]any{
		"group_id":    groupID,
		"message":     []map[string]any{{"type": "text", "data": map[string]string{"text": message}}},
		"auto_escape": true,
	})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.APIURL+"/send_group_msg", bytes.NewReader(body))
	if err != nil {
		return errors.New("invalid QQ request")
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.AccessToken != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.AccessToken)
	}
	client := &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return errors.New("QQ bot connection failed or timed out")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("QQ bot returned HTTP %d", resp.StatusCode)
	}
	var result struct {
		Status  string `json:"status"`
		RetCode *int   `json:"retcode"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16*1024)).Decode(&result); err != nil {
		return errors.New("invalid QQ bot response")
	}
	if result.Status != "ok" || result.RetCode == nil || *result.RetCode != 0 {
		return errors.New("QQ bot rejected the message")
	}
	return nil
}

func (s *OpsService) TestQQNotification(ctx context.Context) error {
	cfg, err := s.getQQNotificationConfig(ctx)
	if err != nil {
		return err
	}
	return sendOpsQQMessage(ctx, cfg, "【Sub2API 测试通知】QQ群告警连接成功。")
}

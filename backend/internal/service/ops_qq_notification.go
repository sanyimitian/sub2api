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

const (
	opsQQConfigKey        = "ops_qq_notification_config"
	opsQQProviderOneBot   = "onebot"
	opsQQProviderOfficial = "qq_official"
)

type OpsQQNotificationConfig struct {
	Enabled               bool   `json:"enabled"`
	Provider              string `json:"provider"`
	APIURL                string `json:"api_url"`
	GroupID               string `json:"group_id"`
	AccessToken           string `json:"access_token,omitempty"`
	AccessTokenConfigured bool   `json:"access_token_configured"`
	AppID                 string `json:"app_id"`
	AppSecret             string `json:"app_secret,omitempty"`
	AppSecretConfigured   bool   `json:"app_secret_configured"`
	GroupOpenID           string `json:"group_openid"`
}

type OpsQQNotificationUpdate struct {
	Enabled  bool   `json:"enabled"`
	Provider string `json:"provider"`
	APIURL   string `json:"api_url"`
	GroupID  string `json:"group_id"`
	// Omitted/empty preserves the existing token. Clearing is explicit.
	AccessToken      string `json:"access_token"`
	ClearAccessToken bool   `json:"clear_access_token"`
	AppID            string `json:"app_id"`
	AppSecret        string `json:"app_secret"`
	ClearAppSecret   bool   `json:"clear_app_secret"`
	GroupOpenID      string `json:"group_openid"`
}

func (cfg *OpsQQNotificationConfig) provider() string {
	if cfg.Provider == "" {
		return opsQQProviderOneBot
	}
	return cfg.Provider
}

func (cfg *OpsQQNotificationConfig) recipient() string {
	if cfg.provider() == opsQQProviderOfficial {
		return "qq_official:" + cfg.AppID + ":" + cfg.GroupOpenID
	}
	return cfg.GroupID
}

func (s *OpsService) getQQNotificationConfig(ctx context.Context) (*OpsQQNotificationConfig, error) {
	cfg := &OpsQQNotificationConfig{Provider: opsQQProviderOneBot}
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
	cfg.Provider = cfg.provider()
	return cfg, nil
}

func redactOpsQQConfig(cfg *OpsQQNotificationConfig) *OpsQQNotificationConfig {
	copy := *cfg
	copy.AccessTokenConfigured = copy.AccessToken != ""
	copy.AccessToken = ""
	copy.AppSecretConfigured = copy.AppSecret != ""
	copy.AppSecret = ""
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
	if cfg == nil {
		return errors.New("QQ notification is not configured")
	}
	if len(cfg.AccessToken) > 4096 || strings.ContainsAny(cfg.AccessToken, "\r\n") {
		return errors.New("invalid QQ access token")
	}
	if len(cfg.AppSecret) > 4096 || strings.ContainsAny(cfg.AppSecret, "\r\n") {
		return errors.New("invalid QQ AppSecret")
	}
	switch cfg.provider() {
	case opsQQProviderOfficial:
		return validateOpsQQOfficialConfig(cfg)
	case opsQQProviderOneBot:
	default:
		return errors.New("unsupported QQ notification provider")
	}
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
	if req.Provider != "" {
		cfg.Provider = strings.TrimSpace(req.Provider)
	}
	cfg.APIURL = strings.TrimRight(strings.TrimSpace(req.APIURL), "/")
	cfg.GroupID = strings.TrimSpace(req.GroupID)
	cfg.AppID = strings.TrimSpace(req.AppID)
	cfg.GroupOpenID = strings.TrimSpace(req.GroupOpenID)
	if req.ClearAccessToken {
		cfg.AccessToken = ""
	}
	if token := strings.TrimSpace(req.AccessToken); token != "" {
		cfg.AccessToken = token
	}
	if req.ClearAppSecret {
		cfg.AppSecret = ""
	}
	if secret := strings.TrimSpace(req.AppSecret); secret != "" {
		cfg.AppSecret = secret
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

func (s *OpsService) sendQQNotification(ctx context.Context, cfg *OpsQQNotificationConfig, message string) error {
	if err := validateOpsQQConfig(cfg); err != nil {
		return err
	}
	if cfg.provider() == opsQQProviderOfficial {
		return s.qqOfficialSender.send(ctx, cfg, message)
	}
	return sendOpsQQMessage(ctx, cfg, message)
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
	return s.sendQQNotification(ctx, cfg, "【Sub2API 测试通知】QQ群告警连接成功。")
}

package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const OpenAISessionPoolEnabledExtraKey = "openai_session_pool_enabled"
const OpenAISessionPoolModelsExtraKey = "openai_session_pool_models"
const defaultOpenAISessionPoolModel = "gpt-6-sol"

func (a *Account) OpenAISessionPoolModels() []string {
	if a == nil {
		return nil
	}
	raw, exists := a.Extra[OpenAISessionPoolModelsExtraKey]
	if !exists {
		return []string{defaultOpenAISessionPoolModel}
	}
	var values []string
	switch v := raw.(type) {
	case []string:
		values = v
	case []any:
		for _, item := range v {
			if model, ok := item.(string); ok {
				values = append(values, model)
			}
		}
	}
	seen := make(map[string]bool)
	models := make([]string, 0, len(values))
	for _, model := range values {
		model = strings.TrimSpace(model)
		if model != "" && !seen[model] {
			models = append(models, model)
			seen[model] = true
		}
	}
	return models
}

// OpenAISessionPoolEnabled 只允许显式开启的 OpenAI 接口密钥账号提供备用会话。
// 是否属于调用分组、模型可用性和并发额度仍由正常调度检查决定。
func (a *Account) OpenAISessionPoolEnabled() bool {
	if a == nil || a.Platform != PlatformOpenAI || a.Type != AccountTypeAPIKey || a.IsCredentialShadow() {
		return false
	}
	enabled, _ := a.Extra[OpenAISessionPoolEnabledExtraKey].(bool)
	return enabled
}

func ValidateOpenAISessionPoolExtra(platform, accountType string, extra map[string]any) error {
	if raw, exists := extra[OpenAISessionPoolModelsExtraKey]; exists {
		var values []string
		switch v := raw.(type) {
		case []string:
			values = v
		case []any:
			for _, item := range v {
				model, ok := item.(string)
				if !ok {
					return infraerrors.BadRequest("OPENAI_SESSION_POOL_MODELS_INVALID", "探测模型列表必须为字符串数组")
				}
				values = append(values, model)
			}
		default:
			return infraerrors.BadRequest("OPENAI_SESSION_POOL_MODELS_INVALID", "探测模型列表必须为字符串数组")
		}
		if len(values) < 1 || len(values) > 20 {
			return infraerrors.BadRequest("OPENAI_SESSION_POOL_MODELS_INVALID", "请配置 1 至 20 个探测模型")
		}
		for _, model := range values {
			if strings.TrimSpace(model) == "" || len(model) > 256 {
				return infraerrors.BadRequest("OPENAI_SESSION_POOL_MODELS_INVALID", "探测模型名称不能为空且不能超过 256 字节")
			}
		}
	}
	raw, exists := extra[OpenAISessionPoolEnabledExtraKey]
	if !exists {
		return nil
	}
	enabled, ok := raw.(bool)
	if !ok {
		return infraerrors.BadRequest("OPENAI_SESSION_POOL_INVALID", "低延迟备用会话开关必须为布尔值")
	}
	if enabled && (platform != PlatformOpenAI || accountType != AccountTypeAPIKey) {
		return infraerrors.BadRequest("OPENAI_SESSION_POOL_ACCOUNT_INVALID", "仅 OpenAI 接口密钥账号可以开启低延迟备用会话")
	}
	return nil
}

// 不保存或输出凭据原文。上游身份或会话头覆盖变化后，旧探测结果不能继续使用。
func openAISessionPoolAccountFingerprint(account *Account) string {
	if account == nil {
		return ""
	}
	raw, err := json.Marshal(struct {
		ID          int64
		Platform    string
		Type        string
		ProxyID     *int64
		Credentials map[string]any
	}{account.ID, account.Platform, account.Type, account.ProxyID, account.Credentials})
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

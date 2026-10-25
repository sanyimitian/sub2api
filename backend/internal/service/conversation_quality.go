package service

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const OpenAISessionPoolQualityExtraKey = "openai_session_pool_quality"

// 与参考项目普通质量规则一致：答题模型与判题模型独立配置，只有明确答对才入池。
// 这里管理单次候选会话，不执行参考项目中的账号隔离、移组或调度开关操作。
type ConversationQualityConfig struct {
	Enabled         bool                `json:"enabled"`
	QuestionKind    string              `json:"question_kind"`
	Prompt          string              `json:"prompt"`
	ExpectedAnswer  string              `json:"expected_answer"`
	ReasoningEffort string              `json:"reasoning_effort"`
	Judge           *QualityJudgeConfig `json:"judge"`
}

// 普通质量规则的内置糖果题；用户也可以编辑题目与参考答案。
const ConversationQualityCandyPrompt = `在一个黑色的袋子里放有三种口味的糖果，每种糖果有两种不同的形状（圆形和五角星形，不同的形状靠手感可以分辨）。现已知不同口味的糖和不同形状的数量统计如下表。参赛者需要在活动前决定摸出的糖果数目，那么，最少取出多少个糖果才能保证手中同时拥有不同形状的苹果味和桃子味的糖？（同时手中有圆形苹果味匹配五角星桃子味糖果，或者有圆形桃子味匹配五角星苹果味糖果都满足要求）
苹果味 桃子味 西瓜味
圆形 7 9 8
五角星形 7 6 4`

const openAISessionQualityProbeTimeout = 10 * time.Minute

func (a *Account) OpenAISessionQualityConfig() (*ConversationQualityConfig, error) {
	if a == nil || a.Extra[OpenAISessionPoolQualityExtraKey] == nil {
		return nil, nil
	}
	raw, err := json.Marshal(a.Extra[OpenAISessionPoolQualityExtraKey])
	if err != nil {
		return nil, fmt.Errorf("降智探测配置无效")
	}
	var cfg ConversationQualityConfig
	if err := json.Unmarshal(raw, &cfg); err != nil || string(raw) == "null" {
		return nil, fmt.Errorf("降智探测配置必须为对象，开关必须为布尔值")
	}
	return &cfg, nil
}

func validateConversationQualityConfig(cfg *ConversationQualityConfig) error {
	if cfg == nil || !cfg.Enabled {
		return nil
	}
	if cfg.QuestionKind != "candy" && cfg.QuestionKind != QualityRandomQuestionKind {
		return fmt.Errorf("请选择固定题目或随机推理题")
	}
	if len(cfg.Prompt) > 16000 || len(cfg.ExpectedAnswer) > 4000 {
		return fmt.Errorf("题目不能超过 16000 字节，参考答案不能超过 4000 字节")
	}
	if cfg.QuestionKind == "candy" && (strings.TrimSpace(cfg.Prompt) == "" || strings.TrimSpace(cfg.ExpectedAnswer) == "") {
		return fmt.Errorf("请填写测试题目和参考答案")
	}
	switch cfg.ReasoningEffort {
	case "", "none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra":
	default:
		return fmt.Errorf("推理强度无效")
	}
	if cfg.Judge == nil || cfg.Judge.GroupID <= 0 || strings.TrimSpace(cfg.Judge.ModelID) == "" || len(cfg.Judge.ModelID) > 100 || strings.TrimSpace(cfg.Judge.Prompt) == "" || len(cfg.Judge.Prompt) > 16000 {
		return fmt.Errorf("请配置判题分组、判题模型和判题提示词（模型最多 100 字节，提示词最多 16000 字节）")
	}
	return nil
}

func openAISessionProbeTimeout(account *Account, firstTokenMaxMS int) time.Duration {
	if cfg, err := account.OpenAISessionQualityConfig(); err == nil && cfg != nil && cfg.Enabled {
		return openAISessionQualityProbeTimeout
	}
	// 较大的首字门槛不能被旧十五秒总期限提前截断，首字之后留五秒收尾。
	if timeout := time.Duration(firstTokenMaxMS)*time.Millisecond + 5*time.Second; timeout > openAIWSLatencyProbeTimeout {
		return timeout
	}
	return openAIWSLatencyProbeTimeout
}

func openAISessionPoolQualityCandidateAllowed(account *Account, candidate openAIWSLatencyReusableCandidate) bool {
	cfg, err := account.OpenAISessionQualityConfig()
	return err == nil && (cfg == nil || !cfg.Enabled || candidate.QualityPassed)
}

// 按合并后的真实配置校验，避免局部更新绕过“降智探测必须同时开启低延迟探测”。
func mergeOpenAISessionPoolExtra(current, updates map[string]any) map[string]any {
	merged := make(map[string]any, len(current)+len(updates))
	for k, v := range current {
		merged[k] = v
	}
	for k, v := range updates {
		merged[k] = v
	}
	return merged
}

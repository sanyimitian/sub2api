package service

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// 公开结果视图绝不携带地址或密钥。
type ModelIdentificationGroup struct {
	DisplayGroupID *int64               `json:"display_group_id"`
	GroupID        int64                `json:"group_id"`
	Name           string               `json:"name"`
	Platform       string               `json:"platform"`
	Plan           *DegradationTestPlan `json:"plan"`
	LatestResultID int64                `json:"latest_result_id"`
	ResultCount    int                  `json:"result_count"`
}

// 独立检测目标；DisplayGroupID 只关联展示名称，不参与请求路由。
type ModelIdentificationTarget struct {
	DisplayGroupID  *int64 `json:"-"`
	ID              int64  `json:"-"`
	Name            string `json:"-"`
	Endpoint        string `json:"-"`
	EncryptedAPIKey string `json:"-"`
	Protocol        string `json:"-"`
}
type ModelIdentificationAdminConfig struct {
	DisplayGroupID *int64 `json:"display_group_id"`
	Name           string `json:"name"`
	Endpoint       string `json:"endpoint"`
	APIKey         string `json:"api_key"`
	Protocol       string `json:"protocol"`
}
type ModelIdentificationRepository interface {
	ListGroups(context.Context) ([]*ModelIdentificationGroup, error)
	GetTarget(context.Context, int64) (*ModelIdentificationTarget, error)
	Save(context.Context, *ModelIdentificationTarget, *DegradationTestPlan) (*DegradationTestPlan, error)
	Delete(context.Context, int64) error
}
type ModelIdentificationInput struct {
	DisplayGroupID  int64  `json:"display_group_id"`
	Endpoint        string `json:"endpoint"`
	APIKey          string `json:"api_key"`
	Protocol        string `json:"protocol"`
	ModelID         string `json:"model_id"`
	IntervalMinutes int    `json:"interval_minutes"`
	TimeoutSeconds  int    `json:"timeout_seconds"`
	ReasoningEffort string `json:"reasoning_effort"`
	Enabled         bool   `json:"enabled"`
}
type ModelIdentificationService struct {
	plans     *DegradationTestService
	repo      ModelIdentificationRepository
	encryptor SecretEncryptor
}

func NewModelIdentificationService(plans *DegradationTestService, repo ModelIdentificationRepository, encryptor SecretEncryptor) *ModelIdentificationService {
	return &ModelIdentificationService{plans: plans, repo: repo, encryptor: encryptor}
}
func (s *ModelIdentificationService) ListGroups(ctx context.Context) ([]*ModelIdentificationGroup, error) {
	return s.repo.ListGroups(ctx)
}

// 共享接口不能读取或操作原有私有账号测试。
func (s *ModelIdentificationService) GetPlan(ctx context.Context, id int64) (*DegradationTestPlan, error) {
	plan, err := s.plans.GetPlan(ctx, id)
	if err != nil {
		return nil, err
	}
	if plan.GroupID == nil {
		return nil, ErrGroupNotFound
	}
	return plan, nil
}

// 仅由管理员路由调用。
func (s *ModelIdentificationService) AdminConfig(ctx context.Context, id int64) (*ModelIdentificationAdminConfig, error) {
	plan, err := s.GetPlan(ctx, id)
	if err != nil {
		return nil, err
	}
	target, err := s.repo.GetTarget(ctx, *plan.GroupID)
	if err != nil {
		return nil, err
	}
	key := ""
	if target.EncryptedAPIKey != "" {
		key, err = s.encryptor.Decrypt(target.EncryptedAPIKey)
		if err != nil {
			return nil, errors.New("无法读取检测密钥")
		}
	}
	return &ModelIdentificationAdminConfig{DisplayGroupID: target.DisplayGroupID, Name: target.Name, Endpoint: target.Endpoint, APIKey: key, Protocol: target.Protocol}, nil
}
func validateIdentificationProtocol(protocol, effort string) error {
	switch protocol {
	case APIProtocolResponses, APIProtocolChatCompletions:
	case APIProtocolAnthropic, "gemini":
		if effort != "" {
			return errors.New("当前接口使用模型默认推理强度；显式推理强度仅支持 Responses 和 Chat Completions 接口")
		}
	default:
		return errors.New("请选择有效的接口类型")
	}
	return validateDegradationTestReasoningEffort(effort)
}
func (s *ModelIdentificationService) Save(ctx context.Context, id int64, input ModelIdentificationInput) (*DegradationTestPlan, error) {
	if input.DisplayGroupID <= 0 {
		return nil, errors.New("请选择站内已有分组用于展示")
	}
	if input.IntervalMinutes < 1 || input.IntervalMinutes > 43200 {
		return nil, errors.New("检测间隔须为 1–43200 分钟")
	}
	if err := validateIdentificationProtocol(input.Protocol, input.ReasoningEffort); err != nil {
		return nil, err
	}
	endpoint := normalizeEndpoint(input.Endpoint)
	u, err := url.Parse(endpoint)
	if err != nil || u.User != nil || len(endpoint) > 2048 {
		return nil, errors.New("接口基础地址无效，不允许包含用户名或密码")
	}
	if err = validateEndpoint(endpoint); err != nil {
		return nil, err
	}
	key := strings.TrimSpace(input.APIKey)
	if len(key) > 8192 || strings.ContainsAny(key, "\r\n") {
		return nil, errors.New("接口密钥格式无效")
	}
	target := &ModelIdentificationTarget{DisplayGroupID: &input.DisplayGroupID, Endpoint: endpoint, Protocol: input.Protocol}
	if id != 0 {
		existing, err := s.GetPlan(ctx, id)
		if err != nil {
			return nil, err
		}
		old, err := s.repo.GetTarget(ctx, *existing.GroupID)
		if err != nil {
			return nil, err
		}
		target.ID = old.ID
		if key == "" {
			if endpoint != old.Endpoint {
				return nil, errors.New("修改接口地址时请重新填写密钥")
			}
			target.EncryptedAPIKey = old.EncryptedAPIKey
		}
	}
	// 目标 ID 由仓库在事务中生成，这里仅验证待保存的公共字段。
	plan := &DegradationTestPlan{
		ID: id, ModelID: strings.TrimSpace(input.ModelID), Prompt: DefaultDegradationTestPrompt,
		CronExpression: fmt.Sprintf("@every %dm", input.IntervalMinutes), TimeoutSeconds: input.TimeoutSeconds,
		ReasoningEffort: input.ReasoningEffort, Enabled: input.Enabled, MaxResults: DegradationTestMaxResults,
	}
	if plan.TimeoutSeconds == 0 {
		plan.TimeoutSeconds = DegradationTestDefaultTimeoutSeconds
	}
	if err = validateDegradationTestFields(plan); err != nil {
		return nil, err
	}
	next, err := computeDegradationNextRun(plan.CronExpression, time.Now())
	if err != nil {
		return nil, err
	}
	plan.NextRunAt = &next
	if key != "" {
		target.EncryptedAPIKey, err = s.encryptor.Encrypt(key)
		if err != nil {
			return nil, errors.New("无法加密检测密钥")
		}
	}
	if target.EncryptedAPIKey == "" {
		return nil, errors.New("请填写接口密钥")
	}
	return s.repo.Save(ctx, target, plan)
}
func (s *ModelIdentificationService) Delete(ctx context.Context, id int64) error {
	if _, err := s.GetPlan(ctx, id); err != nil {
		return err
	}
	return s.repo.Delete(ctx, id)
}
func (s *ModelIdentificationService) Results(ctx context.Context, id int64, admin bool) ([]*DegradationTestResult, error) {
	if _, err := s.GetPlan(ctx, id); err != nil {
		return nil, err
	}
	results, err := s.plans.ListResults(ctx, id, DegradationTestMaxResults)
	if err != nil {
		return nil, err
	}
	out := make([]*DegradationTestResult, 0, len(results))
	for _, result := range results {
		copy := *result
		if !admin && copy.Status != "success" {
			copy.ErrorMessage = "本次检测失败，请等待下次检测或联系管理员"
			copy.ResponseText = ""
		}
		out = append(out, &copy)
	}
	return out, nil
}

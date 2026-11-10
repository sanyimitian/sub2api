package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type identificationRepoStub struct {
	ModelIdentificationRepository
	target ModelIdentificationTarget
	saved  *DegradationTestPlan
}

func (r *identificationRepoStub) GetTarget(context.Context, int64) (*ModelIdentificationTarget, error) {
	copy := r.target
	return &copy, nil
}
func (r *identificationRepoStub) Save(_ context.Context, target *ModelIdentificationTarget, plan *DegradationTestPlan) (*DegradationTestPlan, error) {
	if target.ID == 0 {
		target.ID = 4
	}
	r.target = *target
	plan.GroupID = &r.target.ID
	r.saved = plan
	return plan, nil
}

type identificationEncryptor struct{}

func (identificationEncryptor) Encrypt(value string) (string, error) {
	return "encrypted:" + value, nil
}
func (identificationEncryptor) Decrypt(value string) (string, error) {
	if !strings.HasPrefix(value, "encrypted:") {
		return "", errors.New("invalid cipher")
	}
	return strings.TrimPrefix(value, "encrypted:"), nil
}

type identificationResultsStub struct {
	DegradationTestResultRepository
	rows []*DegradationTestResult
}

func (r *identificationResultsStub) ListByPlanID(_ context.Context, _ int64, limit int) ([]*DegradationTestResult, error) {
	if limit != 24 {
		return nil, errors.New("expected 24 results")
	}
	return r.rows, nil
}
func identificationInput() ModelIdentificationInput {
	return ModelIdentificationInput{DisplayGroupID: 42, Endpoint: "https://8.8.8.8/prefix/v1", APIKey: "test-secret", Protocol: APIProtocolResponses, ModelID: " alias-model ", IntervalMinutes: 90, Enabled: true}
}
func TestModelIdentificationSaveUsesCredentialsAndFixedPrompt(t *testing.T) {
	repo := &identificationRepoStub{}
	svc := NewModelIdentificationService(nil, repo, identificationEncryptor{})
	input := identificationInput()
	plan, err := svc.Save(context.Background(), 0, input)
	require.NoError(t, err)
	require.EqualValues(t, 42, *repo.target.DisplayGroupID)
	require.Zero(t, plan.AccountID)
	require.EqualValues(t, 4, *plan.GroupID)
	require.Equal(t, DefaultDegradationTestPrompt, plan.Prompt)
	require.Equal(t, "alias-model", plan.ModelID)
	require.Equal(t, "@every 90m", plan.CronExpression)
	require.Equal(t, 24, plan.MaxResults)
	require.Equal(t, 600, plan.TimeoutSeconds)
	require.WithinDuration(t, time.Now().Add(90*time.Minute), *plan.NextRunAt, time.Second)
	require.NotEqual(t, input.APIKey, repo.target.EncryptedAPIKey)
	key, err := svc.encryptor.Decrypt(repo.target.EncryptedAPIKey)
	require.NoError(t, err)
	require.Equal(t, input.APIKey, key)
	encoded, err := json.Marshal(&ModelIdentificationGroup{GroupID: 4, Name: repo.target.Name, Platform: repo.target.Protocol, Plan: plan})
	require.NoError(t, err)
	require.NotContains(t, string(encoded), input.APIKey)
	require.NotContains(t, string(encoded), input.Endpoint)
	require.NotContains(t, string(encoded), "encrypted_api_key")
	for _, interval := range []int{0, -1, 43201} {
		input.IntervalMinutes = interval
		_, err = svc.Save(context.Background(), 0, input)
		require.Error(t, err)
	}
}
func TestModelIdentificationCredentialUpdatesAndAdminRead(t *testing.T) {
	id := int64(4)
	plans := &degradationPlanRepoStub{plan: DegradationTestPlan{ID: 9, GroupID: &id}}
	repo := &identificationRepoStub{target: ModelIdentificationTarget{ID: id, Endpoint: "https://8.8.8.8/prefix/v1", EncryptedAPIKey: "encrypted:old-secret", Protocol: APIProtocolResponses}}
	svc := NewModelIdentificationService(NewDegradationTestService(plans, nil), repo, identificationEncryptor{})
	input := identificationInput()
	input.APIKey = ""
	_, err := svc.Save(context.Background(), 9, input)
	require.NoError(t, err)
	require.Equal(t, "encrypted:old-secret", repo.target.EncryptedAPIKey)
	config, err := svc.AdminConfig(context.Background(), 9)
	require.NoError(t, err)
	require.Equal(t, "old-secret", config.APIKey)
	require.EqualValues(t, 42, *config.DisplayGroupID)
	input.Endpoint = "https://1.1.1.1"
	_, err = svc.Save(context.Background(), 9, input)
	require.ErrorContains(t, err, "重新填写密钥")
	input.APIKey = "new-secret"
	_, err = svc.Save(context.Background(), 9, input)
	require.NoError(t, err)
	require.Equal(t, "encrypted:new-secret", repo.target.EncryptedAPIKey)
	plans.plan = DegradationTestPlan{ID: 9, AccountID: 7}
	_, err = svc.GetPlan(context.Background(), 9)
	require.ErrorIs(t, err, ErrGroupNotFound)
	_, err = svc.AdminConfig(context.Background(), 9)
	require.ErrorIs(t, err, ErrGroupNotFound)
	_, err = svc.Results(context.Background(), 9, false)
	require.ErrorIs(t, err, ErrGroupNotFound)
}
func TestModelIdentificationRejectsInvalidCredentialsAndProtocols(t *testing.T) {
	svc := NewModelIdentificationService(nil, &identificationRepoStub{}, identificationEncryptor{})
	for _, endpoint := range []string{"http://8.8.8.8", "https://user:password@8.8.8.8", "https://127.0.0.1", "https://10.0.0.1", "https://8.8.8.8?api_key=secret", "https://8.8.8.8#secret"} {
		input := identificationInput()
		input.Endpoint = endpoint
		_, err := svc.Save(context.Background(), 0, input)
		require.Error(t, err, endpoint)
	}
	input := identificationInput()
	input.APIKey = ""
	_, err := svc.Save(context.Background(), 0, input)
	require.ErrorContains(t, err, "填写接口密钥")
	for _, protocol := range []string{APIProtocolAnthropic, "gemini"} {
		input = identificationInput()
		input.Protocol = protocol
		input.ReasoningEffort = "high"
		_, err = svc.Save(context.Background(), 0, input)
		require.ErrorContains(t, err, "默认推理强度")
		input.ReasoningEffort = ""
		_, err = svc.Save(context.Background(), 0, input)
		require.NoError(t, err)
	}
	input = identificationInput()
	input.Protocol = "unknown"
	_, err = svc.Save(context.Background(), 0, input)
	require.Error(t, err)
}
func TestModelIdentificationRedactsFailuresWithoutMutatingStoredResults(t *testing.T) {
	id := int64(4)
	plans := &degradationPlanRepoStub{plan: DegradationTestPlan{ID: 1, GroupID: &id}}
	results := &identificationResultsStub{rows: []*DegradationTestResult{
		{ID: 2, Status: "failed", ResponseText: "private partial", ErrorMessage: "private failure"},
		{ID: 1, Status: "success", ResponseText: "<svg></svg>", CreatedAt: time.Now().Add(-8 * 24 * time.Hour)},
	}}
	svc := NewModelIdentificationService(NewDegradationTestService(plans, results), nil, nil)
	public, err := svc.Results(context.Background(), 1, false)
	require.NoError(t, err)
	require.Len(t, public, 2)
	require.Empty(t, public[0].ResponseText)
	require.NotContains(t, public[0].ErrorMessage, "private")
	require.Equal(t, "<svg></svg>", public[1].ResponseText)
	admin, err := svc.Results(context.Background(), 1, true)
	require.NoError(t, err)
	require.Equal(t, "private partial", admin[0].ResponseText)
}

type identificationGroupTesterStub struct {
	group                 int64
	model, prompt, effort string
}

func (s *identificationGroupTesterStub) RunGroupGeneration(_ context.Context, target *ModelIdentificationTarget, model, prompt, effort string) (*DegradationTestResult, error) {
	s.group, s.model, s.prompt, s.effort = target.ID, model, prompt, effort
	return &DegradationTestResult{Status: "success", ResponseText: "<svg></svg>"}, nil
}
func TestModelIdentificationRunnerDispatchesGroupPlan(t *testing.T) {
	id := int64(4)
	group := &identificationGroupTesterStub{}
	runner := NewDegradationTestRunnerService(nil, nil, nil, nil)
	t.Cleanup(runner.Stop)
	runner.groupTestSvc = group
	result, err := runner.invokeTest(context.Background(), &DegradationTestPlan{GroupID: &id, IdentificationTarget: &ModelIdentificationTarget{ID: id}, ModelID: "group-model", Prompt: DefaultDegradationTestPrompt, ReasoningEffort: "high"})
	require.NoError(t, err)
	require.Equal(t, "success", result.Status)
	require.Equal(t, id, group.group)
	require.Equal(t, "group-model", group.model)
	require.Equal(t, "high", group.effort)
	runner.groupTestSvc = nil
	_, err = runner.invokeTest(context.Background(), &DegradationTestPlan{GroupID: &id})
	require.Error(t, err)
}
func TestModelIdentificationPlanRequiresExactlyOneTarget(t *testing.T) {
	id := int64(4)
	plan := &DegradationTestPlan{GroupID: &id, ModelID: "model", Prompt: DefaultDegradationTestPrompt}
	require.NoError(t, validateDegradationTestPlan(plan))
	plan.AccountID = 2
	require.Error(t, validateDegradationTestPlan(plan))
	plan.AccountID = 0
	id = 0
	require.Error(t, validateDegradationTestPlan(plan))
}

func TestModelIdentificationRequiresDisplayGroup(t *testing.T) {
	repo := &identificationRepoStub{}
	svc := NewModelIdentificationService(nil, repo, identificationEncryptor{})
	for _, id := range []int64{0, -1} {
		input := identificationInput()
		input.DisplayGroupID = id
		_, err := svc.Save(context.Background(), 0, input)
		require.ErrorContains(t, err, "请选择站内已有分组")
		require.Nil(t, repo.saved)
	}
}

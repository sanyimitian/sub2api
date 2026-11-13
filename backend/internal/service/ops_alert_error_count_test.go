package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type errorCountAlertRepo struct {
	opsRepoMock
	rule     *OpsAlertRule
	counts   map[int]int64
	countErr error
	filter   *OpsErrorLogFilter
	events   []*OpsAlertEvent
	updates  int
}

func (r *errorCountAlertRepo) ListAlertRules(context.Context) ([]*OpsAlertRule, error) {
	return []*OpsAlertRule{r.rule}, nil
}
func (r *errorCountAlertRepo) CountErrorsByStatus(_ context.Context, filter *OpsErrorLogFilter) (map[int]int64, error) {
	r.filter = filter
	return r.counts, r.countErr
}
func (r *errorCountAlertRepo) GetActiveAlertEvent(context.Context, int64) (*OpsAlertEvent, error) {
	for _, event := range r.events {
		if event.Status == OpsAlertStatusFiring {
			return event, nil
		}
	}
	return nil, nil
}
func (r *errorCountAlertRepo) GetLatestAlertEvent(context.Context, int64) (*OpsAlertEvent, error) {
	if len(r.events) == 0 {
		return nil, nil
	}
	return r.events[len(r.events)-1], nil
}
func (r *errorCountAlertRepo) CreateAlertEvent(_ context.Context, event *OpsAlertEvent) (*OpsAlertEvent, error) {
	event.ID = int64(len(r.events) + 1)
	r.events = append(r.events, event)
	return event, nil
}
func (r *errorCountAlertRepo) UpdateAlertEventStatus(_ context.Context, id int64, status string, at *time.Time) error {
	for _, event := range r.events {
		if event.ID == id {
			event.Status = status
			event.ResolvedAt = at
		}
	}
	return nil
}
func (r *errorCountAlertRepo) UpdateAlertDeliveries(context.Context, int64, map[string]*OpsAlertDelivery) error {
	r.updates++
	return nil
}

func TestOpsErrorCountAlertLifecycle(t *testing.T) {
	rule := &OpsAlertRule{ID: 1, Name: "Upstream errors", Enabled: true, MetricType: OpsMetricUpstreamErrorCount,
		Operator: ">", Threshold: 20, WindowMinutes: 5, SustainedMinutes: 0, CooldownMinutes: 10,
		Filters: map[string]any{"platform": "openai", "group_id": 7, "status_codes": []int{502, 503}, "excluded_models": []string{"excluded-model"}},
	}
	repo := &errorCountAlertRepo{rule: rule, counts: map[int]int64{502: 12, 503: 8}}
	svc := NewOpsAlertEvaluatorService(nil, repo, nil, nil, nil, nil)
	svc.evaluateOnce(30 * time.Second)
	require.Empty(t, repo.events, "20 must not trigger a >20 rule")
	require.Equal(t, []string{"excluded-model"}, repo.filter.ExcludedModels)
	require.Equal(t, []int{502, 503}, repo.filter.StatusCodes)
	require.Equal(t, "upstream", repo.filter.Phase)
	require.True(t, repo.filter.IncludeRecoveredUpstream, "same provider-health scope as the error list")
	require.Equal(t, int64(7), *repo.filter.GroupID)
	require.Equal(t, "openai", repo.filter.Platform)
	require.Equal(t, 5*time.Minute, repo.filter.EndTime.Sub(*repo.filter.StartTime))
	require.WithinDuration(t, time.Now(), *repo.filter.EndTime, 2*time.Second, "window must not be truncated to the last full minute")

	repo.counts[503] = 9
	svc.evaluateOnce(30 * time.Second)
	require.Len(t, repo.events, 1)
	require.Equal(t, float64(21), *repo.events[0].MetricValue)
	require.Contains(t, repo.events[0].Description, "502: 12; 503: 9")
	svc.evaluateOnce(30 * time.Second)
	require.Len(t, repo.events, 1, "ongoing breach must not create duplicate alerts")

	repo.countErr = errors.New("database unavailable")
	svc.evaluateOnce(30 * time.Second)
	require.Equal(t, OpsAlertStatusFiring, repo.events[0].Status, "failed query is not a recovery")
	repo.countErr = nil
	repo.counts = map[int]int64{502: 18}
	svc.evaluateOnce(30 * time.Second)
	require.Equal(t, OpsAlertStatusResolved, repo.events[0].Status)
	repo.counts[502] = 30
	svc.evaluateOnce(30 * time.Second)
	require.Len(t, repo.events, 1, "cooldown blocks the next incident")
	repo.events[0].FiredAt = time.Now().Add(-11 * time.Minute)
	svc.evaluateOnce(30 * time.Second)
	require.Len(t, repo.events, 2)
}

func TestOpsErrorCountFilters(t *testing.T) {
	filters, err := ParseOpsErrorCountFilters(map[string]any{"excluded_models": []any{" model-A ", "model-A", "model-a"}})
	require.NoError(t, err)
	require.Equal(t, []string{"model-A", "model-a"}, filters.ExcludedModels)
	require.Equal(t, []int{502, 503}, filters.StatusCodes)
	for _, input := range []map[string]any{
		{"status_codes": []int{}}, {"status_codes": []int{200}}, {"status_codes": []float64{502.5}},
		{"status_codes": "502,503"}, {"excluded_models": []any{123}}, {"excluded_models": []string{" "}},
	} {
		_, err := ParseOpsErrorCountFilters(input)
		require.Error(t, err)
	}
	require.Error(t, normalizeOpsErrorCountRule(&OpsAlertRule{MetricType: OpsMetricUpstreamErrorCount, Threshold: 20.5}))
}

func TestOpsAlertDeliveriesRetryAndDeduplicate(t *testing.T) {
	repo := &errorCountAlertRepo{}
	svc := NewOpsAlertEvaluatorService(nil, repo, nil, nil, nil, nil)
	event := &OpsAlertEvent{ID: 1}
	emailCalls, qqCalls := 0, 0
	email := func() error { emailCalls++; return nil }
	qq := func() error { qqCalls++; return errors.New("QQ bot connection failed or timed out") }
	require.True(t, svc.deliverAlert(context.Background(), event, "email", "admin@example.com", email))
	require.False(t, svc.deliverAlert(context.Background(), event, "qq", "1234", qq))
	require.False(t, svc.deliverAlert(context.Background(), event, "qq", "1234", qq), "respect retry delay")
	require.Equal(t, 1, qqCalls)
	for i := 0; i < 3; i++ {
		deliveries := opsAlertDeliveries(event)
		deliveries["qq:1234"].NextAttemptAt = time.Now().Add(-time.Second)
		event.Dimensions["notifications"] = deliveries
		// Simulate reading persisted JSON and a fresh evaluator after a restart.
		raw, err := json.Marshal(event)
		require.NoError(t, err)
		event = &OpsAlertEvent{}
		require.NoError(t, json.Unmarshal(raw, event))
		svc = NewOpsAlertEvaluatorService(nil, repo, nil, nil, nil, nil)
		svc.deliverAlert(context.Background(), event, "email", "admin@example.com", email)
		svc.deliverAlert(context.Background(), event, "qq", "1234", qq)
	}
	require.Equal(t, 1, emailCalls)
	require.Equal(t, 3, qqCalls, "initial attempt plus two retries")
	require.Equal(t, "failed", opsAlertDeliveries(event)["qq:1234"].Status)
}

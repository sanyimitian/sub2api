package service

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOpsAlertQQGroupValidation(t *testing.T) {
	rule := &OpsAlertRule{QQGroupOpenIDs: []string{" GROUP_A ", "GROUP_B", "GROUP_A"}}
	require.NoError(t, normalizeOpsAlertQQGroups(rule))
	require.Equal(t, []string{"GROUP_A", "GROUP_B"}, rule.QQGroupOpenIDs)
	for _, groups := range [][]string{{" "}, {"../group"}, {"a,b"}, {strings.Repeat("a", 129)}, make([]string, 101)} {
		require.Error(t, normalizeOpsAlertQQGroups(&OpsAlertRule{QQGroupOpenIDs: groups}))
	}
	require.NoError(t, normalizeOpsAlertQQGroups(&OpsAlertRule{}), "legacy rules retain the global default")
}

func TestOpsAlertQQMultipleGroupsRetryIndependently(t *testing.T) {
	ctx := context.Background()
	settings := newRuntimeSettingRepoStub()
	raw, err := json.Marshal(opsQQOfficialTestConfig())
	require.NoError(t, err)
	require.NoError(t, settings.Set(ctx, opsQQConfigKey, string(raw)))
	calls := map[string]int{}
	svc := &OpsService{settingRepo: settings, qqOfficialSender: opsQQOfficialSender{client: &http.Client{
		Transport: opsQQOfficialTransport(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path == "/app/getAppAccessToken" {
				return opsQQOfficialResponse(r, 200, `{"access_token":"token","expires_in":7200}`), nil
			}
			calls[r.URL.Path]++
			if r.URL.Path == "/v2/groups/GROUP_B/messages" && calls[r.URL.Path] == 1 {
				return opsQQOfficialResponse(r, 503, `{}`), nil
			}
			return opsQQOfficialResponse(r, 200, `{"id":"message"}`), nil
		}),
	}}}
	evaluator := NewOpsAlertEvaluatorService(svc, &errorCountAlertRepo{}, nil, nil, nil, nil)
	rule := &OpsAlertRule{ID: 1, NotifyQQ: true, QQGroupOpenIDs: []string{"GROUP_B", "GROUP_C"}}
	event := &OpsAlertEvent{ID: 1, FiredAt: time.Now()}
	evaluator.maybeSendAlertQQ(ctx, nil, rule, event)
	deliveries := opsAlertDeliveries(event)
	require.Equal(t, "failed", deliveries["qq:qq_official:100001:GROUP_B"].Status)
	require.Equal(t, "sent", deliveries["qq:qq_official:100001:GROUP_C"].Status, "one failed group must not block another")
	deliveries["qq:qq_official:100001:GROUP_B"].NextAttemptAt = time.Now().Add(-time.Second)
	event.Dimensions["notifications"] = deliveries
	// Reload persisted JSON to exercise retry deduplication after a restart.
	raw, err = json.Marshal(event)
	require.NoError(t, err)
	event = &OpsAlertEvent{}
	require.NoError(t, json.Unmarshal(raw, event))
	evaluator.maybeSendAlertQQ(ctx, nil, rule, event)
	require.Equal(t, map[string]int{"/v2/groups/GROUP_B/messages": 2, "/v2/groups/GROUP_C/messages": 1}, calls)
	require.Equal(t, "sent", opsAlertDeliveries(event)["qq:qq_official:100001:GROUP_B"].Status)

	// Another rule with no overrides still uses the global group.
	rule.QQGroupOpenIDs = nil
	evaluator.maybeSendAlertQQ(ctx, nil, rule, &OpsAlertEvent{ID: 2})
	require.Equal(t, 1, calls["/v2/groups/GROUP_A1/messages"])
}

func TestOpsAlertQQQueuesUnattemptedGroups(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	settings := newRuntimeSettingRepoStub()
	raw, err := json.Marshal(opsQQOfficialTestConfig())
	require.NoError(t, err)
	require.NoError(t, settings.Set(ctx, opsQQConfigKey, string(raw)))
	svc := &OpsService{settingRepo: settings, qqOfficialSender: opsQQOfficialSender{client: &http.Client{
		Transport: opsQQOfficialTransport(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path == "/app/getAppAccessToken" {
				return opsQQOfficialResponse(r, 200, `{"access_token":"token","expires_in":7200}`), nil
			}
			cancel()
			return opsQQOfficialResponse(r, 200, `{"id":"message"}`), nil
		}),
	}}}
	evaluator := NewOpsAlertEvaluatorService(svc, &errorCountAlertRepo{}, nil, nil, nil, nil)
	rule := &OpsAlertRule{NotifyQQ: true, QQGroupOpenIDs: []string{"GROUP_B", "GROUP_C"}}
	event := &OpsAlertEvent{ID: 1}
	evaluator.maybeSendAlertQQ(ctx, nil, rule, event)
	delivery := opsAlertDeliveries(event)["qq:qq_official:100001:GROUP_C"]
	require.NotNil(t, delivery)
	require.Equal(t, "pending", delivery.Status)
	require.Zero(t, delivery.Attempts, "unattempted groups must not spend their retry budget")
	evaluator.maybeSendAlertQQ(context.Background(), nil, rule, event)
	require.Equal(t, "sent", opsAlertDeliveries(event)["qq:qq_official:100001:GROUP_C"].Status)
}

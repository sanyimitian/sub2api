package service

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func qqWebhookTestService(t *testing.T) (*OpsService, *runtimeSettingRepoStub) {
	t.Helper()
	repo := newRuntimeSettingRepoStub()
	svc := &OpsService{settingRepo: repo}
	_, err := svc.UpdateQQNotificationConfig(context.Background(), &OpsQQNotificationUpdate{Provider: opsQQProviderOfficial, AppID: "11111111", AppSecret: "DG5g3B4j9X2KOErG"})
	require.NoError(t, err)
	return svc, repo
}

func qqWebhookSign(body []byte, timestamp string) string {
	return hex.EncodeToString(ed25519.Sign(opsQQWebhookPrivateKey("DG5g3B4j9X2KOErG"), append([]byte(timestamp), body...)))
}

func TestOpsQQWebhookOfficialVectors(t *testing.T) {
	svc, repo := qqWebhookTestService(t)
	before := repo.setCalls
	result, err := svc.HandleQQWebhook(context.Background(), "11111111", "", "", []byte(`{"d":{"plain_token":"Arq0D5A61EgUu4OxUvOp","event_ts":"1725442341"},"op":13}`))
	require.NoError(t, err)
	require.Equal(t, "Arq0D5A61EgUu4OxUvOp", result["plain_token"])
	require.Equal(t, "87befc99c42c651b3aac0278e71ada338433ae26fcb24307bdc5ad38c1adc2d01bcfcadc0842edac85e85205028a1132afe09280305f13aa6909ffc2d652c706", result["signature"])
	require.Equal(t, before, repo.setCalls)
	key := opsQQWebhookPrivateKey("naOC0ocQE3shWLAfffVLB1rhYPG7")
	require.Equal(t, ed25519.PublicKey{215, 195, 98, 254, 120, 174, 248, 31, 242, 50, 135, 180, 147, 98, 139, 93, 176, 42, 60, 79, 227, 11, 33, 94, 77, 25, 96, 155, 93, 118, 103, 58}, key.Public())
	body := []byte(`{ "op": 0,"d": {}, "t": "GATEWAY_EVENT_NAME"}`)
	sig := hex.EncodeToString(ed25519.Sign(key, append([]byte("1725442341"), body...)))
	require.True(t, verifyOpsQQWebhook(key, body, "1725442341", sig, time.Unix(1725442341, 0)))
	require.False(t, verifyOpsQQWebhook(key, append(body, ' '), "1725442341", sig, time.Unix(1725442341, 0)))
}

func TestOpsQQWebhookDiscoveryAndIsolation(t *testing.T) {
	svc, repo := qqWebhookTestService(t)
	ctx := context.Background()
	configBefore := repo.values[opsQQConfigKey]
	dispatch := func(eventType, group string) {
		t.Helper()
		body := []byte(fmt.Sprintf(`{"op":0,"t":%q,"d":{"group_openid":%q,"content":"private chat content"}}`, eventType, group))
		ts := strconv.FormatInt(time.Now().Unix(), 10)
		ack, err := svc.HandleQQWebhook(ctx, "11111111", ts, qqWebhookSign(body, ts), body)
		require.NoError(t, err)
		require.Equal(t, map[string]any{"op": 12, "d": 0}, ack)
	}
	dispatch("GROUP_AT_MESSAGE_CREATE", "GROUP_1")
	dispatch("GROUP_AT_MESSAGE_CREATE", "GROUP_1")
	dispatch("GROUP_ADD_ROBOT", "GROUP_2")
	// A new service can read discovered groups after restart, without enabling alerts.
	status, err := (&OpsService{settingRepo: repo}).GetQQWebhookStatus(ctx)
	require.NoError(t, err)
	require.True(t, status.Ready)
	require.Len(t, status.Groups, 2)
	require.Equal(t, "GROUP_2", status.Groups[0].GroupOpenID)
	require.NotNil(t, status.LastEventAt)
	require.Equal(t, OpsQQWebhookPath, status.CallbackPath)
	require.Equal(t, configBefore, repo.values[opsQQConfigKey], "discovery must not alter notification configuration")
	for _, raw := range repo.values {
		require.NotContains(t, raw, "private chat content")
	}
	dispatch("GROUP_DEL_ROBOT", "GROUP_1")
	status, err = svc.GetQQWebhookStatus(ctx)
	require.NoError(t, err)
	require.Len(t, status.Groups, 1)
	for i := 0; i < 55; i++ {
		dispatch("GROUP_ADD_ROBOT", fmt.Sprintf("GROUP_%d", i))
	}
	status, err = svc.GetQQWebhookStatus(ctx)
	require.NoError(t, err)
	require.Len(t, status.Groups, 50)
	_, err = svc.UpdateQQNotificationConfig(ctx, &OpsQQNotificationUpdate{Provider: opsQQProviderOfficial, AppID: "22222222", AppSecret: "other-secret"})
	require.NoError(t, err)
	status, err = svc.GetQQWebhookStatus(ctx)
	require.NoError(t, err)
	require.Empty(t, status.Groups)
	require.Nil(t, status.LastEventAt)
}

func TestOpsQQWebhookRejectsUntrustedRequests(t *testing.T) {
	svc, repo := qqWebhookTestService(t)
	body := []byte(`{"op":0,"t":"GROUP_AT_MESSAGE_CREATE","d":{"group_openid":"GROUP_1"}}`)
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	old := strconv.FormatInt(time.Now().Add(-11*time.Minute).Unix(), 10)
	future := strconv.FormatInt(time.Now().Add(2*time.Minute).Unix(), 10)
	for _, tc := range []struct {
		name, app, ts, sig string
		body               []byte
	}{
		{"missing signature", "11111111", ts, "", body},
		{"wrong app", "22222222", ts, qqWebhookSign(body, ts), body},
		{"missing app", "", ts, qqWebhookSign(body, ts), body},
		{"expired", "11111111", old, qqWebhookSign(body, old), body},
		{"future", "11111111", future, qqWebhookSign(body, future), body},
		{"bad timestamp", "11111111", "bad", qqWebhookSign(body, "bad"), body},
		{"tampered body", "11111111", ts, qqWebhookSign(body, ts), append(append([]byte{}, body...), ' ')},
		{"invalid hex", "11111111", ts, strings.Repeat("z", 128), body},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.HandleQQWebhook(context.Background(), tc.app, tc.ts, tc.sig, tc.body)
			require.ErrorIs(t, err, ErrOpsQQWebhookUnauthorized)
		})
	}
	require.Len(t, repo.values, 1)
	for _, bad := range []string{`{}`, `{"op":13,"d":{}}`, `{"op":13,"d":{"plain_token":"{}","event_ts":"123"}}`, strings.Repeat("x", OpsQQWebhookBodyLimit+1)} {
		_, err := svc.HandleQQWebhook(context.Background(), "11111111", "", "", []byte(bad))
		require.ErrorIs(t, err, ErrOpsQQWebhookPayload)
	}
}

func TestOpsQQWebhookErrorsAndUnsupportedEvents(t *testing.T) {
	ctx := context.Background()
	_, err := (&OpsService{}).HandleQQWebhook(ctx, "11111111", "", "", []byte(`{"op":13}`))
	require.ErrorIs(t, err, ErrOpsQQWebhookUnavailable)
	svc, repo := qqWebhookTestService(t)
	dispatch := func(body string) (map[string]any, error) {
		ts := strconv.FormatInt(time.Now().Unix(), 10)
		return svc.HandleQQWebhook(ctx, "11111111", ts, qqWebhookSign([]byte(body), ts), []byte(body))
	}
	ack, err := dispatch(`{"op":0,"t":"C2C_MESSAGE_CREATE","d":{}}`)
	require.NoError(t, err)
	require.Equal(t, 12, ack["op"])
	_, err = dispatch(`{"op":0,"t":"GROUP_ADD_ROBOT","d":{"group_openid":"../bad"}}`)
	require.ErrorIs(t, err, ErrOpsQQWebhookPayload)
	repo.setFn = func(string, string) error { return errors.New("database offline") }
	_, err = dispatch(`{"op":0,"t":"GROUP_ADD_ROBOT","d":{"group_openid":"GROUP_1"}}`)
	require.ErrorContains(t, err, "database offline")
	raw, err := json.Marshal(OpsQQWebhookStatus{Groups: []OpsQQDiscoveredGroup{}})
	require.NoError(t, err)
	require.NotContains(t, string(raw), "secret")
}

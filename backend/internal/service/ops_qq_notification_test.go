package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpsQQNotificationTransport(t *testing.T) {
	for _, test := range []struct {
		name, body string
		code       int
		wantError  bool
	}{
		{"success", `{"status":"ok","retcode":0}`, 200, false},
		{"bot error", `{"status":"failed","retcode":100}`, 200, true},
		{"missing retcode", `{"status":"ok"}`, 200, true},
		{"http error", `secret response`, 503, true},
		{"invalid body", `<html>invalid</html>`, 200, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "/send_group_msg", r.URL.Path)
				require.Equal(t, "Bearer secret-token", r.Header.Get("Authorization"))
				var payload map[string]any
				require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
				require.Equal(t, float64(123456), payload["group_id"])
				segments := payload["message"].([]any)
				require.Equal(t, "text", segments[0].(map[string]any)["type"], "untrusted model names must not execute CQ codes")
				w.WriteHeader(test.code)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			err := sendOpsQQMessage(context.Background(), &OpsQQNotificationConfig{APIURL: server.URL, GroupID: "123456", AccessToken: "secret-token"}, "[CQ:at,qq=all] test")
			if test.wantError {
				require.Error(t, err)
				require.NotContains(t, err.Error(), "secret")
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestOpsQQNotificationDoesNotFollowRedirect(t *testing.T) {
	called := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
	defer server.Close()
	err := sendOpsQQMessage(context.Background(), &OpsQQNotificationConfig{APIURL: server.URL, GroupID: "1", AccessToken: "secret"}, "test")
	require.Error(t, err)
	require.False(t, called)
}

func TestOpsQQNotificationSettingsToken(t *testing.T) {
	svc := &OpsService{settingRepo: newRuntimeSettingRepoStub()}
	ctx := context.Background()
	req := &OpsQQNotificationUpdate{Enabled: true, APIURL: "http://127.0.0.1:3000/", GroupID: "123456", AccessToken: "secret"}
	cfg, err := svc.UpdateQQNotificationConfig(ctx, req)
	require.NoError(t, err)
	require.Empty(t, cfg.AccessToken)
	require.True(t, cfg.AccessTokenConfigured)
	cfg, err = svc.GetQQNotificationConfig(ctx)
	require.NoError(t, err)
	require.Empty(t, cfg.AccessToken)
	req.AccessToken = ""
	req.GroupID = "654321"
	_, err = svc.UpdateQQNotificationConfig(ctx, req)
	require.NoError(t, err)
	stored, err := svc.getQQNotificationConfig(ctx)
	require.NoError(t, err)
	require.Equal(t, "secret", stored.AccessToken)
	req.ClearAccessToken = true
	cfg, err = svc.UpdateQQNotificationConfig(ctx, req)
	require.NoError(t, err)
	require.False(t, cfg.AccessTokenConfigured)
	for _, url := range []string{"file:///tmp/bot", "http://user:secret@localhost", "http://localhost?token=secret", "http://localhost#fragment"} {
		req.APIURL = url
		_, err = svc.UpdateQQNotificationConfig(ctx, req)
		require.Error(t, err)
	}
}

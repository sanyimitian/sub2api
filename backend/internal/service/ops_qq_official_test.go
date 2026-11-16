package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type opsQQOfficialTransport func(*http.Request) (*http.Response, error)

func (f opsQQOfficialTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func opsQQOfficialResponse(r *http.Request, status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}
}

func opsQQOfficialTestConfig() *OpsQQNotificationConfig {
	return &OpsQQNotificationConfig{Enabled: true, Provider: opsQQProviderOfficial, AppID: "100001", AppSecret: "secret-value", GroupOpenID: "GROUP_A1"}
}

func TestOpsQQOfficialSettings(t *testing.T) {
	ctx := context.Background()
	svc := &OpsService{settingRepo: newRuntimeSettingRepoStub()}
	// Existing records without a provider remain usable as OneBot settings.
	require.NoError(t, svc.settingRepo.Set(ctx, opsQQConfigKey, `{"enabled":true,"api_url":"http://localhost:3000","group_id":"123456","access_token":"legacy-token"}`))
	legacy, err := svc.GetQQNotificationConfig(ctx)
	require.NoError(t, err)
	require.Equal(t, opsQQProviderOneBot, legacy.Provider)
	require.Equal(t, "123456", legacy.recipient())
	require.True(t, legacy.AccessTokenConfigured)
	require.Empty(t, legacy.AccessToken)

	req := &OpsQQNotificationUpdate{Enabled: true, Provider: opsQQProviderOfficial, AppID: " 100001 ", AppSecret: " secret-value ", GroupOpenID: " GROUP_A1 "}
	cfg, err := svc.UpdateQQNotificationConfig(ctx, req)
	require.NoError(t, err)
	require.Empty(t, cfg.AppSecret)
	require.True(t, cfg.AppSecretConfigured)
	require.Equal(t, "100001", cfg.AppID)
	require.Equal(t, "GROUP_A1", cfg.GroupOpenID)
	require.Equal(t, "qq_official:100001:GROUP_A1", cfg.recipient())
	got, err := svc.GetQQNotificationConfig(ctx)
	require.NoError(t, err)
	raw, err := json.Marshal(got)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "secret-value")
	require.NotContains(t, string(raw), "legacy-token")

	req.AppSecret = " "
	req.GroupOpenID = "GROUP_B2"
	_, err = svc.UpdateQQNotificationConfig(ctx, req)
	require.NoError(t, err)
	stored, err := svc.getQQNotificationConfig(ctx)
	require.NoError(t, err)
	require.Equal(t, "secret-value", stored.AppSecret)
	require.Equal(t, "legacy-token", stored.AccessToken)
	req.AppSecret = "rotated-secret"
	_, err = svc.UpdateQQNotificationConfig(ctx, req)
	require.NoError(t, err)
	stored, err = svc.getQQNotificationConfig(ctx)
	require.NoError(t, err)
	require.Equal(t, "rotated-secret", stored.AppSecret)

	req.AppSecret, req.ClearAppSecret = "", true
	_, err = svc.UpdateQQNotificationConfig(ctx, req)
	require.Error(t, err, "an enabled official bot requires a secret")
	stored, err = svc.getQQNotificationConfig(ctx)
	require.NoError(t, err)
	require.Equal(t, "rotated-secret", stored.AppSecret, "invalid updates must not change the stored config")
	req.Enabled = false
	cfg, err = svc.UpdateQQNotificationConfig(ctx, req)
	require.NoError(t, err)
	require.False(t, cfg.AppSecretConfigured)
	stored, err = svc.getQQNotificationConfig(ctx)
	require.NoError(t, err)
	require.Empty(t, stored.AppSecret)
}

func TestOpsQQOfficialValidation(t *testing.T) {
	for _, change := range []func(*OpsQQNotificationConfig){
		func(c *OpsQQNotificationConfig) { c.Provider = "unknown" },
		func(c *OpsQQNotificationConfig) { c.AppID = "" },
		func(c *OpsQQNotificationConfig) { c.AppID = "100x" },
		func(c *OpsQQNotificationConfig) { c.AppSecret = "" },
		func(c *OpsQQNotificationConfig) { c.AppSecret = "secret\r\ninjected" },
		func(c *OpsQQNotificationConfig) { c.AppSecret = strings.Repeat("s", 4097) },
		func(c *OpsQQNotificationConfig) { c.GroupOpenID = "" },
		func(c *OpsQQNotificationConfig) { c.GroupOpenID = "../messages" },
		func(c *OpsQQNotificationConfig) { c.GroupOpenID = "group?query" },
		func(c *OpsQQNotificationConfig) { c.GroupOpenID = strings.Repeat("a", 129) },
	} {
		cfg := opsQQOfficialTestConfig()
		change(cfg)
		require.Error(t, validateOpsQQConfig(cfg))
	}
	require.NoError(t, validateOpsQQConfig(&OpsQQNotificationConfig{Provider: opsQQProviderOfficial}))
	err := (&OpsService{}).sendQQNotification(context.Background(), &OpsQQNotificationConfig{Provider: opsQQProviderOfficial}, "test")
	require.ErrorContains(t, err, "requires AppID")
}

func TestOpsQQOfficialTransportAndTokenLifecycle(t *testing.T) {
	for _, expires := range []string{`7200`, `"7200"`} {
		t.Run(expires, func(t *testing.T) {
			cfg := opsQQOfficialTestConfig()
			cfg.APIURL = "https://untrusted.example"
			cfg.AccessToken = "onebot-token"
			tokens, messages := 0, 0
			sender := &opsQQOfficialSender{client: &http.Client{Transport: opsQQOfficialTransport(func(r *http.Request) (*http.Response, error) {
				require.Equal(t, "https", r.URL.Scheme)
				require.Equal(t, "api.bot.qq.com", r.URL.Host)
				require.Equal(t, http.MethodPost, r.Method)
				require.Equal(t, "application/json", r.Header.Get("Content-Type"))
				if r.URL.Path == "/app/getAppAccessToken" {
					tokens++
					require.Empty(t, r.Header.Get("Authorization"))
					var payload map[string]string
					require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
					require.Equal(t, map[string]string{"appId": cfg.AppID, "clientSecret": cfg.AppSecret}, payload)
					return opsQQOfficialResponse(r, 200, fmt.Sprintf(`{"access_token":"token-%d","expires_in":%s}`, tokens, expires)), nil
				}
				messages++
				require.Equal(t, "/v2/groups/GROUP_A1/messages", r.URL.Path)
				require.Equal(t, fmt.Sprintf("QQBot token-%d", tokens), r.Header.Get("Authorization"))
				var payload map[string]any
				require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
				require.Equal(t, map[string]any{"msg_type": float64(0), "content": "【告警】502 [CQ:at,qq=all]"}, payload)
				return opsQQOfficialResponse(r, 200, `{"id":"message-id"}`), nil
			})}}
			ctx := context.Background()
			send := func() { require.NoError(t, sender.send(ctx, cfg, "【告警】502 [CQ:at,qq=all]")) }
			send()
			send()
			require.Equal(t, 1, tokens, "reuse a valid token")
			sender.mu.Lock()
			sender.refreshAt = time.Now().Add(-time.Second)
			sender.mu.Unlock()
			send()
			require.Equal(t, 2, tokens, "refresh an expired token")
			cfg.AppSecret = "rotated-secret"
			send()
			require.Equal(t, 3, tokens, "secret rotation must not reuse cached credentials")
			cfg.AppID = "100002"
			send()
			require.Equal(t, 4, tokens, "another bot must obtain its own token")
			require.Equal(t, 5, messages)
		})
	}
}

func TestOpsQQOfficialUnauthorizedRefreshIsBounded(t *testing.T) {
	for _, alwaysFail := range []bool{false, true} {
		t.Run(fmt.Sprint(alwaysFail), func(t *testing.T) {
			tokens, messages := 0, 0
			sender := &opsQQOfficialSender{client: &http.Client{Transport: opsQQOfficialTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path == "/app/getAppAccessToken" {
					tokens++
					return opsQQOfficialResponse(r, 200, fmt.Sprintf(`{"access_token":"token-%d","expires_in":7200}`, tokens)), nil
				}
				messages++
				if messages == 1 || alwaysFail {
					return opsQQOfficialResponse(r, 401, `{"code":11244,"message":"secret-value"}`), nil
				}
				require.Equal(t, "QQBot token-2", r.Header.Get("Authorization"))
				return opsQQOfficialResponse(r, 200, `{"id":"message-id"}`), nil
			})}}
			err := sender.send(context.Background(), opsQQOfficialTestConfig(), "alert")
			if alwaysFail {
				require.Error(t, err)
				require.NotContains(t, err.Error(), "secret-value")
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, 2, tokens)
			require.Equal(t, 2, messages)
		})
	}
}

func TestOpsQQOfficialErrorsAndRedirects(t *testing.T) {
	for _, tc := range []struct {
		name       string
		token      bool
		status     int
		body, hint string
	}{
		{"token rejected", true, 400, `{"errcode":40013,"errmsg":"secret-value"}`, "AppID"},
		{"token business error", true, 200, `{"code":40013,"access_token":"secret-value","expires_in":7200}`, "AppID"},
		{"token malformed", true, 200, `<html>secret-value</html>`, "invalid"},
		{"token missing expiry", true, 200, `{"access_token":"secret-value"}`, "invalid"},
		{"token bad expiry", true, 200, `{"access_token":"secret-value","expires_in":0}`, "invalid"},
		{"token header injection", true, 200, `{"access_token":"secret-value\r\nheader","expires_in":7200}`, "invalid"},
		{"token redirect", true, 307, `{}`, "HTTP 307"},
		{"message redirect", false, 307, `{}`, "HTTP 307"},
		{"permission", false, 400, `{"code":40034105,"message":"secret-value"}`, "allow proactive"},
		{"business error", false, 200, `{"code":40034105,"id":"id","message":"secret-value"}`, "allow proactive"},
		{"rate limit", false, 429, `{"code":40034100}`, "rate limit"},
		{"not member", false, 400, `{"code":40034101}`, "add the bot"},
		{"missing ID", false, 200, `{}`, "invalid"},
		{"invalid JSON", false, 200, `secret-value`, "invalid"},
		{"oversized body", false, 200, strings.Repeat(" ", 16*1024) + `{"id":"secret-value"}`, "invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			sender := &opsQQOfficialSender{client: &http.Client{Transport: opsQQOfficialTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				require.Equal(t, "api.bot.qq.com", r.URL.Host, "must not follow redirects with credentials")
				if !tc.token && r.URL.Path == "/app/getAppAccessToken" {
					return opsQQOfficialResponse(r, 200, `{"access_token":"cached-token","expires_in":7200}`), nil
				}
				resp := opsQQOfficialResponse(r, tc.status, tc.body)
				resp.Header.Set("Location", "https://untrusted.example/redirect")
				return resp, nil
			})}}
			err := sender.send(context.Background(), opsQQOfficialTestConfig(), "alert")
			require.ErrorContains(t, err, tc.hint)
			require.NotContains(t, err.Error(), "secret-value")
			require.NotContains(t, err.Error(), "cached-token")
			if tc.token {
				require.Equal(t, 1, calls)
			} else {
				require.Equal(t, 2, calls, "only authentication failures get an immediate retry")
			}
		})
	}
}

func TestOpsQQOfficialConcurrentTokenRefresh(t *testing.T) {
	var tokens atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	sender := &opsQQOfficialSender{client: &http.Client{Transport: opsQQOfficialTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/app/getAppAccessToken" {
			if tokens.Add(1) == 1 {
				close(entered)
			}
			<-release
			return opsQQOfficialResponse(r, 200, `{"access_token":"cached-token","expires_in":7200}`), nil
		}
		return opsQQOfficialResponse(r, 200, `{"id":"message-id"}`), nil
	})}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	errs := make(chan error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- sender.send(ctx, opsQQOfficialTestConfig(), "alert")
		}()
	}
	<-entered
	waiting, stop := context.WithTimeout(ctx, 20*time.Millisecond)
	defer stop()
	_, err := sender.accessToken(waiting, opsQQOfficialTestConfig())
	require.Error(t, err)
	close(release)
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.Equal(t, int32(1), tokens.Load())
}

func TestOpsQQOfficialTestAndAlertDelivery(t *testing.T) {
	ctx := context.Background()
	svc := &OpsService{settingRepo: newRuntimeSettingRepoStub()}
	_, err := svc.UpdateQQNotificationConfig(ctx, &OpsQQNotificationUpdate{
		Enabled: true, Provider: opsQQProviderOfficial, AppID: "100001", AppSecret: "secret-value", GroupOpenID: "GROUP_A1",
	})
	require.NoError(t, err)
	messages := 0
	fail := false
	svc.qqOfficialSender.client = &http.Client{Transport: opsQQOfficialTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/app/getAppAccessToken" {
			return opsQQOfficialResponse(r, 200, `{"access_token":"cached-token","expires_in":7200}`), nil
		}
		messages++
		if fail {
			return opsQQOfficialResponse(r, 400, `{"code":40034105,"message":"secret-value"}`), nil
		}
		return opsQQOfficialResponse(r, 200, `{"id":"message-id"}`), nil
	})}
	require.NoError(t, svc.TestQQNotification(ctx))
	repo := &errorCountAlertRepo{}
	evaluator := &OpsAlertEvaluatorService{opsRepo: repo, opsService: svc}
	rule := &OpsAlertRule{ID: 1, Name: "502 errors", NotifyQQ: true}
	event := &OpsAlertEvent{ID: 1, FiredAt: time.Now()}
	fail = true
	evaluator.maybeSendAlertQQ(ctx, nil, rule, event)
	key := "qq:qq_official:100001:GROUP_A1"
	delivery := opsAlertDeliveries(event)[key]
	require.NotNil(t, delivery)
	require.Equal(t, "failed", delivery.Status)
	require.NotContains(t, delivery.LastError, "secret-value")
	evaluator.maybeSendAlertQQ(ctx, nil, rule, event)
	require.Equal(t, 2, messages, "respect retry delay")
	fail = false
	delivery.NextAttemptAt = time.Now().Add(-time.Second)
	event.Dimensions["notifications"] = map[string]*OpsAlertDelivery{key: delivery}
	evaluator.maybeSendAlertQQ(ctx, nil, rule, event)
	delivery = opsAlertDeliveries(event)[key]
	require.Equal(t, "sent", delivery.Status)
	require.Equal(t, 2, delivery.Attempts)
	require.Empty(t, delivery.LastError)
	evaluator.maybeSendAlertQQ(ctx, nil, rule, event)
	require.Equal(t, 3, messages, "successful deliveries must not be repeated")
}

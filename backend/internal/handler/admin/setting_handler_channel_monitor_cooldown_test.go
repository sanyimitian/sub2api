package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type channelMonitorCooldownHandlerRepoStub struct {
	values map[string]string
}

func (r *channelMonitorCooldownHandlerRepoStub) Get(context.Context, string) (*service.Setting, error) {
	return nil, errors.New("unexpected Get")
}
func (r *channelMonitorCooldownHandlerRepoStub) GetValue(_ context.Context, key string) (string, error) {
	return r.values[key], nil
}
func (r *channelMonitorCooldownHandlerRepoStub) Set(_ context.Context, key, value string) error {
	r.values[key] = value
	return nil
}
func (r *channelMonitorCooldownHandlerRepoStub) GetMultiple(context.Context, []string) (map[string]string, error) {
	return nil, errors.New("unexpected GetMultiple")
}
func (r *channelMonitorCooldownHandlerRepoStub) SetMultiple(context.Context, map[string]string) error {
	return errors.New("unexpected SetMultiple")
}
func (r *channelMonitorCooldownHandlerRepoStub) GetAll(context.Context) (map[string]string, error) {
	return nil, errors.New("unexpected GetAll")
}
func (r *channelMonitorCooldownHandlerRepoStub) Delete(context.Context, string) error {
	return errors.New("unexpected Delete")
}

func newChannelMonitorCooldownHandler(t *testing.T, value string) (*SettingHandler, *channelMonitorCooldownHandlerRepoStub) {
	t.Helper()
	repo := &channelMonitorCooldownHandlerRepoStub{values: map[string]string{service.SettingKeyChannelMonitorCooldownSettings: value}}
	svc := service.NewSettingService(repo, &config.Config{})
	return NewSettingHandler(svc, nil, nil, nil, nil, nil, nil), repo
}

func callChannelMonitorCooldownHandler(t *testing.T, method, path string, body any, handler gin.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	var payload *bytes.Reader
	if body == nil {
		payload = bytes.NewReader(nil)
	} else {
		raw, err := json.Marshal(body)
		require.NoError(t, err)
		payload = bytes.NewReader(raw)
	}
	c.Request = httptest.NewRequest(method, path, payload)
	c.Request.Header.Set("Content-Type", "application/json")
	handler(c)
	return rec
}

func TestSettingHandler_ChannelMonitorCooldown_DefaultAndUpdate(t *testing.T) {
	h, repo := newChannelMonitorCooldownHandler(t, "")
	get := callChannelMonitorCooldownHandler(t, http.MethodGet, "/settings/channel-monitor-cooldown", nil, h.GetChannelMonitorCooldownSettings)
	require.Equal(t, http.StatusOK, get.Code)
	require.Contains(t, get.Body.String(), `"cooldown_minutes":[2,5,30,60,120]`)

	updated := service.ChannelMonitorCooldownSettings{
		Version: 1, CooldownMinutes: []int{3, 6, 9, 12, 15},
		SlowResponseThresholdSeconds: 20, PriorityIncrement: 2,
		MaxPriorityIncrease: 4, PriorityAutoRecoverySeconds: 7200,
	}
	put := callChannelMonitorCooldownHandler(t, http.MethodPut, "/settings/channel-monitor-cooldown", updated, h.UpdateChannelMonitorCooldownSettings)
	require.Equal(t, http.StatusOK, put.Code)
	require.Contains(t, repo.values[service.SettingKeyChannelMonitorCooldownSettings], `"cooldown_minutes":[3,6,9,12,15]`)

	reset := callChannelMonitorCooldownHandler(t, http.MethodPost, "/settings/channel-monitor-cooldown/reset", nil, h.ResetChannelMonitorCooldownSettings)
	require.Equal(t, http.StatusOK, reset.Code)
	require.Contains(t, repo.values[service.SettingKeyChannelMonitorCooldownSettings], `"cooldown_minutes":[2,5,30,60,120]`)
}

func TestSettingHandler_ChannelMonitorCooldown_InvalidUpdateDoesNotPersist(t *testing.T) {
	h, repo := newChannelMonitorCooldownHandler(t, "")
	before := repo.values[service.SettingKeyChannelMonitorCooldownSettings]
	bad := service.ChannelMonitorCooldownSettings{Version: 1, CooldownMinutes: []int{5, 4, 3, 2, 1}, SlowResponseThresholdSeconds: 12, PriorityIncrement: 1, MaxPriorityIncrease: 3, PriorityAutoRecoverySeconds: 3600}
	rec := callChannelMonitorCooldownHandler(t, http.MethodPut, "/settings/channel-monitor-cooldown", bad, h.UpdateChannelMonitorCooldownSettings)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, before, repo.values[service.SettingKeyChannelMonitorCooldownSettings])
}

var _ service.SettingRepository = (*channelMonitorCooldownHandlerRepoStub)(nil)

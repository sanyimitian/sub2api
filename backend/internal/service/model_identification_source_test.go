package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type identificationSourceRepo struct {
	SettingRepository
	value string
	err   error
}

func (r *identificationSourceRepo) GetValue(context.Context, string) (string, error) {
	return r.value, r.err
}
func (r *identificationSourceRepo) Set(_ context.Context, _ string, value string) error {
	if r.err != nil {
		return r.err
	}
	r.value = value
	return nil
}

type identificationSourceTransport func(*http.Request) (*http.Response, error)

func (f identificationSourceTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
func identificationResponse(body string, status int) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

const identificationGroupsJSON = `{"code":0,"data":[{"group_id":10,"display_group_id":88,"name":"主站分组","platform":"responses","plan":{"id":1,"group_id":10,"reasoning_effort":"high"},"endpoint":"must-not-forward","api_key":"secret"}]}`

func TestModelIdentificationSourceNormalizesPublicURLs(t *testing.T) {
	for _, raw := range []string{"https://8.8.8.8", "https://8.8.8.8/api/v1", "https://8.8.8.8/api/v1/model-identification", " https://8.8.8.8/api/v1/model-identification/groups/ "} {
		got, err := normalizeIdentificationSource(raw)
		require.NoError(t, err)
		require.Equal(t, "https://8.8.8.8/api/v1/model-identification", got)
	}
	got, err := normalizeIdentificationSource("https://8.8.8.8/prefix/api/v1/model-identification/groups")
	require.NoError(t, err)
	require.Equal(t, "https://8.8.8.8/prefix/api/v1/model-identification", got)
	for _, raw := range []string{"http://8.8.8.8", "https://127.0.0.1", "https://10.0.0.1", "https://user:pass@8.8.8.8", "https://8.8.8.8?token=secret", "https://8.8.8.8#fragment"} {
		_, err := normalizeIdentificationSource(raw)
		require.Error(t, err, raw)
	}
}
func TestModelIdentificationSourceReadsOnlyPublicDataAndCaches(t *testing.T) {
	repo := &identificationSourceRepo{}
	svc := NewModelIdentificationSourceService(repo)
	calls := 0
	svc.client.Transport = identificationSourceTransport(func(req *http.Request) (*http.Response, error) {
		calls++
		require.Equal(t, http.MethodGet, req.Method)
		require.Empty(t, req.Header.Get("Authorization"))
		require.Empty(t, req.Header.Get("Cookie"))
		require.Equal(t, "1", req.Header.Get(ModelIdentificationProxyHeader))
		switch req.URL.Path {
		case "/api/v1/model-identification/groups":
			return identificationResponse(identificationGroupsJSON, 200), nil
		case "/api/v1/model-identification/plans/1/results":
			return identificationResponse(`{"code":0,"data":[{"id":1,"plan_id":1,"status":"failed","response_text":"private","error_message":"private-error","reasoning_effort":"low"},{"id":2,"plan_id":1,"status":"success","response_text":"<svg></svg>","reasoning_effort":"high"}]}`, 200), nil
		default:
			t.Fatalf("unexpected path: %s", req.URL.Path)
			return nil, nil
		}
	})
	config, err := svc.Save(context.Background(), "https://8.8.8.8/api/v1/model-identification/groups")
	require.NoError(t, err)
	require.True(t, config.ReadOnly)
	require.Equal(t, config.SourceURL, repo.value)
	require.ErrorIs(t, svc.RequireLocal(context.Background()), ErrModelIdentificationReadOnly)
	groups, err := svc.Groups(context.Background(), repo.value, true)
	require.NoError(t, err)
	publicJSON, err := json.Marshal(groups)
	require.NoError(t, err)
	require.NotContains(t, string(publicJSON), "must-not-forward")
	require.NotContains(t, string(publicJSON), "secret")
	require.NotContains(t, string(publicJSON), "api_key")

	rows, err := svc.Results(context.Background(), repo.value, 1)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Empty(t, rows[0].ResponseText)
	require.NotContains(t, rows[0].ErrorMessage, "private")
	require.Equal(t, "low", rows[0].ReasoningEffort)
	require.Equal(t, "<svg></svg>", rows[1].ResponseText)
	rows[1].ResponseText = "mutated"
	rows, err = svc.Results(context.Background(), repo.value, 1)
	require.NoError(t, err)
	require.Equal(t, "<svg></svg>", rows[1].ResponseText)
	require.Equal(t, 3, calls, "validation, groups and results each fetched once")
	_, err = svc.Results(context.Background(), repo.value, 999)
	require.ErrorIs(t, err, ErrGroupNotFound)
	require.Equal(t, 3, calls)
	_, err = svc.Save(context.Background(), "")
	require.NoError(t, err)
	require.Empty(t, repo.value)
	require.NoError(t, svc.RequireLocal(context.Background()))
}
func TestModelIdentificationSourceRejectsInvalidOriginWithoutChangingSetting(t *testing.T) {
	for _, tc := range []struct {
		body   string
		status int
	}{
		{"<html>login</html>", 200}, {`{"data":null}`, 200}, {`{"code":1,"data":[]}`, 200},
		{`{"code":0,"data":[{"group_id":1,"plan":{"id":1,"account_id":99}}]}`, 200},
		{"", 302}, {"", 401}, {"", 409}, {"", 500},
	} {
		repo := &identificationSourceRepo{value: "existing-source"}
		svc := NewModelIdentificationSourceService(repo)
		svc.client.Transport = identificationSourceTransport(func(*http.Request) (*http.Response, error) { return identificationResponse(tc.body, tc.status), nil })
		_, err := svc.Save(context.Background(), "https://8.8.8.8")
		require.Error(t, err)
		require.Equal(t, "existing-source", repo.value)
	}
	repo := &identificationSourceRepo{}
	svc := NewModelIdentificationSourceService(repo)
	svc.client.Transport = identificationSourceTransport(func(*http.Request) (*http.Response, error) {
		r := identificationResponse(identificationGroupsJSON, 200)
		r.Header.Set(ModelIdentificationInstanceHeader, svc.InstanceID())
		return r, nil
	})
	_, err := svc.Save(context.Background(), "https://8.8.8.8")
	require.ErrorContains(t, err, "不能将本站")
	require.Empty(t, repo.value)
	repo.err = errors.New("database unavailable")
	require.Error(t, svc.RequireLocal(context.Background()), "不能因配置读取失败恢复检测")
}

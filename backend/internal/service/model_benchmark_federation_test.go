package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type benchmarkFederationRepo struct {
	SettingRepository
	values map[string]string
}

func (r *benchmarkFederationRepo) GetValue(_ context.Context, key string) (string, error) {
	v, ok := r.values[key]
	if !ok {
		return "", ErrSettingNotFound
	}
	return v, nil
}
func (r *benchmarkFederationRepo) Set(_ context.Context, key, value string) error {
	r.values[key] = value
	return nil
}
func (r *benchmarkFederationRepo) GetAll(context.Context) (map[string]string, error) {
	return r.values, nil
}
func (r *benchmarkFederationRepo) Delete(_ context.Context, key string) error {
	delete(r.values, key)
	return nil
}

type benchmarkFederationCipher struct{}

func (benchmarkFederationCipher) Encrypt(value string) (string, error) {
	return base64.StdEncoding.EncodeToString([]byte(value)), nil
}
func (benchmarkFederationCipher) Decrypt(value string) (string, error) {
	b, err := base64.StdEncoding.DecodeString(value)
	return string(b), err
}
func benchmarkFederationFixture() (*ModelBenchmarkFederationService, *benchmarkFederationRepo) {
	repo := &benchmarkFederationRepo{values: map[string]string{}}
	return NewModelBenchmarkFederationService(repo, benchmarkFederationCipher{}), repo
}

func TestModelBenchmarkFederationTokensAreDistinctRevocableAndNeverReturnedAgain(t *testing.T) {
	ctx := context.Background()
	main, repo := benchmarkFederationFixture()
	a, tokenA, err := main.CreateSite(ctx, "甲站")
	require.NoError(t, err)
	b, tokenB, err := main.CreateSite(ctx, "乙站")
	require.NoError(t, err)
	require.NotEqual(t, a.ID, b.ID)
	require.NotEqual(t, tokenA, tokenB)
	got, err := main.Authenticate(ctx, tokenA)
	require.NoError(t, err)
	require.Equal(t, a, got)
	_, err = main.Authenticate(ctx, b.ID+tokenA[32:])
	require.Error(t, err)
	_, err = main.Authenticate(ctx, "")
	require.Error(t, err)
	list, err := main.Sites(ctx)
	require.NoError(t, err)
	require.Len(t, list, 2)
	serialized, err := json.Marshal(list)
	require.NoError(t, err)
	require.NotContains(t, string(serialized), tokenA)
	require.NotContains(t, repo.values[benchmarkSitePrefix+a.ID], tokenA)
	_, err = main.SaveSource(ctx, "https://8.8.8.8", tokenA)
	require.ErrorContains(t, err, "本站")
	require.NoError(t, main.RevokeSite(ctx, a.ID))
	_, err = main.Authenticate(ctx, tokenA)
	require.Error(t, err)
	_, err = main.Authenticate(ctx, tokenB)
	require.NoError(t, err)
}

func TestModelBenchmarkFederationSourceValidatesEncryptsAndPreservesOnFailure(t *testing.T) {
	ctx := context.Background()
	main, _ := benchmarkFederationFixture()
	site, token, err := main.CreateSite(ctx, "接入站")
	require.NoError(t, err)
	satellite, repo := benchmarkFederationFixture()
	calls := 0
	satellite.client.Transport = identificationSourceTransport(func(req *http.Request) (*http.Response, error) {
		calls++
		require.Equal(t, "https://8.8.8.8/prefix/api/v1/model-benchmarks/federation/info", req.URL.String())
		require.Equal(t, "Bearer "+token, req.Header.Get("Authorization"))
		require.Empty(t, req.Header.Get("Cookie"))
		return identificationResponse(`{"code":0,"data":{"version":1,"site_id":"`+site.ID+`"}}`, 200), nil
	})
	source, err := satellite.SaveSource(ctx, "https://8.8.8.8/prefix/api/v1/model-benchmarks/public-results", token)
	require.NoError(t, err)
	require.True(t, source.HasToken)
	require.Equal(t, site.ID, source.SiteID)
	require.NotContains(t, repo.values[benchmarkSourceKey], token)
	value, err := satellite.Config(ctx)
	require.NoError(t, err)
	require.Equal(t, source, value)
	raw, err := json.Marshal(value)
	require.NoError(t, err)
	require.NotContains(t, string(raw), token)
	_, err = satellite.SaveSource(ctx, source.SourceURL, "")
	require.NoError(t, err)
	require.Equal(t, 2, calls)
	original := repo.values[benchmarkSourceKey]
	satellite.client.Transport = identificationSourceTransport(func(req *http.Request) (*http.Response, error) {
		return identificationResponse(`{"code":401,"message":"bad token"}`, 401), nil
	})
	_, err = satellite.SaveSource(ctx, source.SourceURL, token)
	require.Error(t, err)
	require.Equal(t, original, repo.values[benchmarkSourceKey])
	for _, address := range []string{"http://8.8.8.8", "https://127.0.0.1", "https://10.0.0.1", "https://user:pass@8.8.8.8", "https://8.8.8.8?x=1", "https://8.8.8.8#x"} {
		_, err := satellite.SaveSource(ctx, address, token)
		require.Error(t, err, address)
		require.Equal(t, original, repo.values[benchmarkSourceKey])
	}
	_, err = satellite.SaveSource(ctx, "https://1.1.1.1", "")
	require.Error(t, err)
	cleared, err := satellite.SaveSource(ctx, "", "")
	require.NoError(t, err)
	require.Empty(t, cleared.SourceURL)
	require.False(t, cleared.HasToken)
}

func TestModelBenchmarkFederationRemoteRequestUsesOnlyStoredCredentialsAndDoesNotRetry(t *testing.T) {
	ctx := context.Background()
	svc, repo := benchmarkFederationFixture()
	token := strings.Repeat("a", 32) + "." + strings.Repeat("b", 64)
	encrypted, _ := svc.encryptor.Encrypt(token)
	data, _ := json.Marshal(benchmarkSourceSecret{ModelBenchmarkSource: ModelBenchmarkSource{SourceURL: "https://8.8.8.8/api/v1/model-benchmarks/federation"}, EncryptedToken: encrypted})
	repo.values[benchmarkSourceKey] = string(data)
	calls := 0
	svc.client.Transport = identificationSourceTransport(func(req *http.Request) (*http.Response, error) {
		calls++
		require.Equal(t, "/api/v1/model-benchmarks/federation/users/7/runs", req.URL.Path)
		require.Equal(t, "Bearer "+token, req.Header.Get("Authorization"))
		require.Empty(t, req.Header.Get("Cookie"))
		require.Empty(t, req.Header.Get("X-User-ID"))
		body, err := io.ReadAll(req.Body)
		require.NoError(t, err)
		require.JSONEq(t, `{"model":"m"}`, string(body))
		return nil, errors.New("network failure")
	})
	_, err := svc.RemoteRequest(ctx, "POST", "/users/7/runs", strings.NewReader(`{"model":"m"}`))
	require.Error(t, err)
	require.Equal(t, 1, calls)
}

func TestModelBenchmarkConnectionKeepsDestinationAndCredentialTogether(t *testing.T) {
	ctx := context.Background()
	svc, repo := benchmarkFederationFixture()
	save := func(host, site, secret string) {
		token := strings.Repeat(site, 32) + "." + strings.Repeat(secret, 64)
		encrypted, err := svc.encryptor.Encrypt(token)
		require.NoError(t, err)
		data, err := json.Marshal(benchmarkSourceSecret{ModelBenchmarkSource: ModelBenchmarkSource{
			SourceURL: "https://" + host + "/api/v1/model-benchmarks/federation", SiteID: strings.Repeat(site, 32),
		}, EncryptedToken: encrypted})
		require.NoError(t, err)
		repo.values[benchmarkSourceKey] = string(data)
	}
	save("first.example", "a", "b")
	connection, err := svc.Connection(ctx)
	require.NoError(t, err)
	save("second.example", "c", "d")
	config, err := svc.Config(ctx)
	require.NoError(t, err)
	require.Contains(t, config.SourceURL, "second.example")
	var hosts, credentials []string
	svc.client.Transport = identificationSourceTransport(func(req *http.Request) (*http.Response, error) {
		hosts = append(hosts, req.URL.Host)
		credentials = append(credentials, req.Header.Get("Authorization"))
		return identificationResponse(`{"code":0,"data":{}}`, 200), nil
	})
	response, err := connection.RemoteRequest(ctx, "POST", "/users/7/runs", strings.NewReader(`{}`))
	require.NoError(t, err)
	response.Body.Close()
	response, err = svc.RemoteRequest(ctx, "POST", "/users/7/runs", strings.NewReader(`{}`))
	require.NoError(t, err)
	response.Body.Close()
	require.Equal(t, []string{"first.example", "second.example"}, hosts)
	require.Equal(t, []string{"Bearer " + strings.Repeat("a", 32) + "." + strings.Repeat("b", 64), "Bearer " + strings.Repeat("c", 32) + "." + strings.Repeat("d", 64)}, credentials)
}

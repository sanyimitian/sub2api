package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const benchmarkSourceKey = "model_benchmark_source"
const benchmarkSitePrefix = "model_benchmark_site_"

type ModelBenchmarkSource struct {
	SourceURL string `json:"source_url"`
	SiteID    string `json:"site_id"`
	HasToken  bool   `json:"has_token"`
}

type benchmarkSourceSecret struct {
	ModelBenchmarkSource
	EncryptedToken string `json:"encrypted_token"`
}

type ModelBenchmarkSite struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedAt string `json:"created_at"`
}

type benchmarkSiteSecret struct {
	ModelBenchmarkSite
	TokenHash string `json:"token_hash"`
}

// 站点身份由主站签发的密钥确定，不能由浏览器传入的站点编号决定。
type ModelBenchmarkFederationService struct {
	repo      SettingRepository
	encryptor SecretEncryptor
	client    *http.Client
}

// 一次请求使用同一份来源与凭据，避免配置切换时把请求发到另一个主站。
type ModelBenchmarkConnection struct {
	ModelBenchmarkSource
	encryptedToken string
	service        *ModelBenchmarkFederationService
}

func (s *ModelBenchmarkFederationService) Connection(ctx context.Context) (*ModelBenchmarkConnection, error) {
	config, err := s.source(ctx)
	if err != nil {
		return nil, err
	}
	return &ModelBenchmarkConnection{ModelBenchmarkSource: config.ModelBenchmarkSource, encryptedToken: config.EncryptedToken, service: s}, nil
}

func NewModelBenchmarkFederationService(repo SettingRepository, encryptor SecretEncryptor) *ModelBenchmarkFederationService {
	client := newSSRFSafeHTTPClientWithHeaderTimeout(25*time.Second, 10*time.Second)
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &ModelBenchmarkFederationService{repo: repo, encryptor: encryptor, client: client}
}

func (s *ModelBenchmarkFederationService) source(ctx context.Context) (*benchmarkSourceSecret, error) {
	result := &benchmarkSourceSecret{}
	if s == nil {
		return result, nil
	}
	raw, err := s.repo.GetValue(ctx, benchmarkSourceKey)
	if errors.Is(err, ErrSettingNotFound) {
		return result, nil
	}
	if err != nil {
		return nil, err
	}
	if raw != "" {
		err = json.Unmarshal([]byte(raw), result)
	}
	result.HasToken = result.EncryptedToken != ""
	return result, err
}

func (s *ModelBenchmarkFederationService) Config(ctx context.Context) (*ModelBenchmarkSource, error) {
	config, err := s.source(ctx)
	if err != nil {
		return nil, err
	}
	return &config.ModelBenchmarkSource, nil
}

func normalizeBenchmarkSource(raw string) (string, error) {
	value := strings.TrimRight(strings.TrimSpace(raw), "/")
	if value == "" {
		return "", nil
	}
	u, err := url.Parse(value)
	if err != nil || len(value) > 2048 || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" {
		return "", errors.New("请填写不带认证信息、参数或片段的主站 HTTPS 地址")
	}
	if err := validateEndpoint(value); err != nil {
		return "", err
	}
	p := strings.TrimRight(u.Path, "/")
	p = strings.TrimSuffix(strings.TrimSuffix(p, "/public-results"), "/federation")
	if !strings.HasSuffix(p, "/api/v1/model-benchmarks") {
		p = strings.TrimSuffix(p, "/api/v1") + "/api/v1/model-benchmarks"
	}
	u.Path = p + "/federation"
	return u.String(), nil
}

func (s *ModelBenchmarkFederationService) SaveSource(ctx context.Context, raw, token string) (*ModelBenchmarkSource, error) {
	address, err := normalizeBenchmarkSource(raw)
	if err != nil {
		return nil, err
	}
	config := &benchmarkSourceSecret{}
	if address != "" {
		old, err := s.source(ctx)
		if err != nil {
			return nil, err
		}
		token = strings.TrimSpace(token)
		if token == "" && address == old.SourceURL && old.EncryptedToken != "" {
			token, err = s.encryptor.Decrypt(old.EncryptedToken)
			if err != nil {
				return nil, errors.New("接入密钥无法读取，请重新填写")
			}
		}
		if !validBenchmarkSiteToken(token) {
			return nil, errors.New("请填写主站签发的站点接入密钥")
		}
		if _, err := s.Authenticate(ctx, token); err == nil {
			return nil, errors.New("不能把本站设为自己的主站")
		}
		sites, err := s.Sites(ctx)
		if err != nil {
			return nil, err
		}
		if len(sites) > 0 {
			return nil, errors.New("本站已有接入站点，请先撤销这些接入，再切换为其他主站")
		}
		// 验证远端身份、协议和站点归属后再保存，失败时保留原配置。
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, address+"/info", nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := s.client.Do(req)
		if err != nil {
			return nil, errors.New("无法连接主站，请检查地址与网络")
		}
		defer res.Body.Close()
		data, err := io.ReadAll(io.LimitReader(res.Body, 4097))
		var envelope struct {
			Code int `json:"code"`
			Data struct {
				Version int    `json:"version"`
				SiteID  string `json:"site_id"`
			} `json:"data"`
		}
		if err != nil || len(data) > 4096 || res.StatusCode != http.StatusOK || json.Unmarshal(data, &envelope) != nil || envelope.Code != 0 || envelope.Data.Version != 1 || envelope.Data.SiteID != token[:32] {
			return nil, errors.New("主站验证失败，请检查接入密钥并确认来源站正在本地执行测试")
		}
		config.ModelBenchmarkSource = ModelBenchmarkSource{SourceURL: address, SiteID: envelope.Data.SiteID, HasToken: true}
		config.EncryptedToken, err = s.encryptor.Encrypt(token)
		if err != nil {
			return nil, errors.New("接入密钥无法保存")
		}
	}
	data, err := json.Marshal(config)
	if err != nil {
		return nil, err
	}
	if err = s.repo.Set(ctx, benchmarkSourceKey, string(data)); err != nil {
		return nil, err
	}
	return &config.ModelBenchmarkSource, nil
}

func validBenchmarkSiteToken(token string) bool {
	if len(token) != 97 || token[32] != '.' {
		return false
	}
	_, err := hex.DecodeString(token[:32] + token[33:])
	return err == nil
}

func (s *ModelBenchmarkFederationService) Sites(ctx context.Context) ([]ModelBenchmarkSite, error) {
	all, err := s.repo.GetAll(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]ModelBenchmarkSite, 0)
	for key, raw := range all {
		if !strings.HasPrefix(key, benchmarkSitePrefix) {
			continue
		}
		var site benchmarkSiteSecret
		if err := json.Unmarshal([]byte(raw), &site); err != nil {
			return nil, err
		}
		items = append(items, site.ModelBenchmarkSite)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt < items[j].CreatedAt })
	return items, nil
}

func (s *ModelBenchmarkFederationService) CreateSite(ctx context.Context, name string) (*ModelBenchmarkSite, string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 100 || strings.ContainsAny(name, "\r\n\x00") {
		return nil, "", errors.New("请填写 1–100 个字符的站点名称")
	}
	identity, secret := make([]byte, 16), make([]byte, 32)
	if _, err := rand.Read(identity); err != nil {
		return nil, "", err
	}
	if _, err := rand.Read(secret); err != nil {
		return nil, "", err
	}
	site := ModelBenchmarkSite{ID: hex.EncodeToString(identity), Name: name, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	token := site.ID + "." + hex.EncodeToString(secret)
	hash := sha256.Sum256([]byte(token))
	data, _ := json.Marshal(benchmarkSiteSecret{ModelBenchmarkSite: site, TokenHash: hex.EncodeToString(hash[:])})
	if err := s.repo.Set(ctx, benchmarkSitePrefix+site.ID, string(data)); err != nil {
		return nil, "", err
	}
	return &site, token, nil
}

func (s *ModelBenchmarkFederationService) RevokeSite(ctx context.Context, id string) error {
	if len(id) != 32 {
		return errors.New("站点编号不正确")
	}
	if _, err := hex.DecodeString(id); err != nil {
		return errors.New("站点编号不正确")
	}
	return s.repo.Delete(ctx, benchmarkSitePrefix+id)
}

func (s *ModelBenchmarkFederationService) Authenticate(ctx context.Context, token string) (*ModelBenchmarkSite, error) {
	if s == nil || !validBenchmarkSiteToken(token) {
		return nil, errors.New("站点认证失败")
	}
	raw, err := s.repo.GetValue(ctx, benchmarkSitePrefix+token[:32])
	if err != nil {
		return nil, errors.New("站点认证失败")
	}
	var site benchmarkSiteSecret
	hash := sha256.Sum256([]byte(token))
	if json.Unmarshal([]byte(raw), &site) != nil || subtle.ConstantTimeCompare([]byte(site.TokenHash), []byte(hex.EncodeToString(hash[:]))) != 1 {
		return nil, errors.New("站点认证失败")
	}
	return &site.ModelBenchmarkSite, nil
}

// RemoteRequest 从受保护的配置读取凭据，不转发浏览器的登录头或 Cookie。
func (s *ModelBenchmarkFederationService) RemoteRequest(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	connection, err := s.Connection(ctx)
	if err != nil {
		return nil, err
	}
	return connection.RemoteRequest(ctx, method, path, body)
}

func (connection *ModelBenchmarkConnection) RemoteRequest(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	if connection.SourceURL == "" {
		return nil, errors.New("尚未配置主站")
	}
	token, err := connection.service.encryptor.Decrypt(connection.encryptedToken)
	if err != nil {
		return nil, errors.New("接入密钥无法读取")
	}
	req, err := http.NewRequestWithContext(ctx, method, connection.SourceURL+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	return connection.service.client.Do(req)
}

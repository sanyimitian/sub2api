package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

const SettingKeyModelIdentificationSource = "model_identification_source_url"
const ModelIdentificationProxyHeader = "X-Model-Identification-Proxy"
const ModelIdentificationInstanceHeader = "X-Model-Identification-Instance"

var ErrModelIdentificationReadOnly = errors.New("本站正在复用其他站点的鉴定结果，请在来源站点管理检测")

type ModelIdentificationSource struct {
	SourceURL string `json:"source_url"`
	ReadOnly  bool   `json:"read_only"`
}

type identificationCachedResponse struct {
	data    []byte
	expires time.Time
}

// 仅代理公开结果，不携带访问者身份或检测凭证；请求前缀由管理员保存。
type ModelIdentificationSourceService struct {
	instance string
	repo     SettingRepository
	client   *http.Client
	mu       sync.Mutex
	cache    map[string]identificationCachedResponse
	flights  singleflight.Group
}

func NewModelIdentificationSourceService(repo SettingRepository) *ModelIdentificationSourceService {
	client := newSSRFSafeHTTPClientWithHeaderTimeout(30*time.Second, 10*time.Second)
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	token := make([]byte, 16)
	if _, err := rand.Read(token); err != nil {
		panic(err)
	}
	return &ModelIdentificationSourceService{instance: hex.EncodeToString(token), repo: repo, client: client, cache: make(map[string]identificationCachedResponse)}
}

func (s *ModelIdentificationSourceService) Config(ctx context.Context) (*ModelIdentificationSource, error) {
	if s == nil {
		return &ModelIdentificationSource{}, nil
	}
	raw, err := s.repo.GetValue(ctx, SettingKeyModelIdentificationSource)
	if errors.Is(err, ErrSettingNotFound) {
		raw, err = "", nil
	}
	if err != nil {
		return nil, err
	}
	return &ModelIdentificationSource{SourceURL: raw, ReadOnly: raw != ""}, nil
}

func normalizeIdentificationSource(raw string) (string, error) {
	value := strings.TrimRight(strings.TrimSpace(raw), "/")
	if value == "" {
		return "", nil
	}
	u, err := url.Parse(value)
	if err != nil || u.User != nil || len(value) > 2048 || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" {
		return "", errors.New("请填写不带认证信息、参数或片段的公开接口地址")
	}
	if err := validateEndpoint(value); err != nil {
		return "", err
	}
	p := strings.TrimRight(u.Path, "/")
	p = strings.TrimSuffix(p, "/groups")
	if !strings.HasSuffix(p, "/api/v1/model-identification") {
		p = strings.TrimSuffix(p, "/api/v1") + "/api/v1/model-identification"
	}
	u.Path = p
	return u.String(), nil
}

func (s *ModelIdentificationSourceService) Save(ctx context.Context, raw string) (*ModelIdentificationSource, error) {
	value, err := normalizeIdentificationSource(raw)
	if err != nil {
		return nil, err
	}
	if value != "" {
		// 保存前验证来源是可读取的主站，防止错填本站或形成站点循环。
		if _, err = s.Groups(ctx, value, false); err != nil {
			return nil, err
		}
	}
	if err = s.repo.Set(ctx, SettingKeyModelIdentificationSource, value); err != nil {
		return nil, err
	}
	s.mu.Lock()
	clear(s.cache)
	s.mu.Unlock()
	return &ModelIdentificationSource{SourceURL: value, ReadOnly: value != ""}, nil
}

func (s *ModelIdentificationSourceService) RequireLocal(ctx context.Context) error {
	source, err := s.Config(ctx)
	if err != nil {
		return err
	}
	if source.ReadOnly {
		return ErrModelIdentificationReadOnly
	}
	return nil
}

func (s *ModelIdentificationSourceService) fetch(ctx context.Context, endpoint string, cached bool) ([]byte, error) {
	if cached {
		s.mu.Lock()
		item, ok := s.cache[endpoint]
		s.mu.Unlock()
		if ok && time.Now().Before(item.expires) {
			return item.data, nil
		}
	}
	result := s.flights.DoChan(endpoint+strconv.FormatBool(cached), func() (any, error) {
		fetchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, errors.New("鉴定来源地址无效")
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set(ModelIdentificationProxyHeader, "1")
		resp, err := s.client.Do(req)
		if err != nil {
			return nil, errors.New("无法连接鉴定来源，请检查地址及网络")
		}
		defer resp.Body.Close()
		if resp.Header.Get(ModelIdentificationInstanceHeader) == s.instance {
			return nil, errors.New("不能将本站设置为自己的鉴定来源")
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("鉴定来源返回 HTTP %d，请使用直接执行检测的主站", resp.StatusCode)
		}
		const maxBody = 256 << 20
		data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
		if err != nil || len(data) > maxBody {
			return nil, errors.New("鉴定来源响应过大或不完整")
		}
		if cached && len(data) <= 4<<20 {
			s.mu.Lock()
			if len(s.cache) >= 16 {
				clear(s.cache)
			}
			s.cache[endpoint] = identificationCachedResponse{data: data, expires: time.Now().Add(15 * time.Second)}
			s.mu.Unlock()
		}
		return data, nil
	})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-result:
		if result.Err != nil {
			return nil, result.Err
		}
		return result.Val.([]byte), nil
	}
}

func (s *ModelIdentificationSourceService) Groups(ctx context.Context, source string, cached bool) ([]*ModelIdentificationGroup, error) {
	data, err := s.fetch(ctx, source+"/groups", cached)
	if err != nil {
		return nil, err
	}
	var body struct {
		Code int                         `json:"code"`
		Data []*ModelIdentificationGroup `json:"data"`
	}
	if json.Unmarshal(data, &body) != nil || body.Code != 0 || body.Data == nil {
		return nil, errors.New("鉴定来源未返回有效的分组列表")
	}
	for _, g := range body.Data {
		if g == nil || g.GroupID <= 0 || g.Plan == nil || g.Plan.ID <= 0 || g.Plan.AccountID != 0 || g.Plan.GroupID == nil || *g.Plan.GroupID != g.GroupID {
			return nil, errors.New("鉴定来源分组格式无效")
		}
	}
	return body.Data, nil
}
func (s *ModelIdentificationSourceService) Results(ctx context.Context, source string, id int64) ([]*DegradationTestResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	groups, err := s.Groups(ctx, source, true)
	if err != nil {
		return nil, err
	}
	found := false
	for _, g := range groups {
		if g.Plan.ID == id {
			found = true
			break
		}
	}
	if !found {
		return nil, ErrGroupNotFound
	}
	data, err := s.fetch(ctx, source+"/plans/"+strconv.FormatInt(id, 10)+"/results", true)
	if err != nil {
		return nil, err
	}
	var body struct {
		Code int                      `json:"code"`
		Data []*DegradationTestResult `json:"data"`
	}
	if json.Unmarshal(data, &body) != nil || body.Code != 0 || body.Data == nil || len(body.Data) > DegradationTestMaxResults {
		return nil, errors.New("鉴定来源未返回有效的检测记录")
	}
	for _, result := range body.Data {
		if result == nil || result.PlanID != id {
			return nil, errors.New("鉴定来源记录归属无效")
		}
		if result.Status != "success" {
			result.ResponseText = ""
			result.ErrorMessage = "本次检测失败，请等待下次检测或联系管理员"
		}
	}
	return body.Data, nil
}

func (s *ModelIdentificationSourceService) InstanceID() string {
	if s == nil {
		return ""
	}
	return s.instance
}

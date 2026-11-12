package service

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
)

type BenchmarkGroupOption struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Platform string `json:"platform"`
}

type BenchmarkGroupTarget struct {
	GroupID   int64  `json:"group_id"`
	GroupName string `json:"group_name"`
	BaseURL   string `json:"base_url"`
	APIKey    string `json:"api_key"`
	Model     string `json:"model"`
	Effort    string `json:"effort"`
}

type benchmarkGroupAdmin interface {
	GetGroup(context.Context, int64) (*Group, error)
	GetGroupModelsListCandidates(context.Context, int64, string) ([]string, error)
}

type benchmarkGroupKeys interface {
	GetAvailableGroups(context.Context, int64) ([]Group, error)
	List(context.Context, int64, pagination.PaginationParams, APIKeyListFilters) ([]APIKey, *pagination.PaginationResult, error)
	Create(context.Context, int64, CreateAPIKeyRequest) (*APIKey, error)
}

// Group tests use normal gateway authentication and billing. Credentials are
// prepared on the server and are never returned by the options endpoints.
type ModelBenchmarkGroupService struct {
	admin   benchmarkGroupAdmin
	keys    benchmarkGroupKeys
	baseURL func(context.Context) (string, error)
	mu      sync.Mutex
}

func NewModelBenchmarkGroupService(admin AdminService, keys *APIKeyService, settings *SettingService) *ModelBenchmarkGroupService {
	return &ModelBenchmarkGroupService{admin: admin, keys: keys, baseURL: func(ctx context.Context) (string, error) {
		raw, err := settings.settingRepo.GetValue(ctx, SettingKeyAPIBaseURL)
		if err != nil && !errors.Is(err, ErrSettingNotFound) {
			return "", errors.New("无法读取本站 API 地址配置")
		}
		if strings.TrimSpace(raw) == "" {
			raw = settings.GetFrontendURL(ctx)
		}
		return benchmarkGatewayURL(raw)
	}}
}

func benchmarkGatewayURL(raw string) (string, error) {
	value := strings.TrimRight(strings.TrimSpace(raw), "/")
	u, err := url.Parse(value)
	if err != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" || len(value) > 2048 {
		return "", infraerrors.BadRequest("BENCHMARK_BASE_URL_REQUIRED", "请先在系统设置中配置本站 API 基础地址，再创建分组定时测试")
	}
	// Never derive the credential destination from the request Host or a browser input.
	if u.Path == "" {
		u.Path = "/v1"
	}
	return u.String(), nil
}

func (s *ModelBenchmarkGroupService) Groups(ctx context.Context, userID int64) ([]BenchmarkGroupOption, error) {
	groups, err := s.keys.GetAvailableGroups(ctx, userID)
	if err != nil {
		return nil, err
	}
	items := make([]BenchmarkGroupOption, 0, len(groups))
	for _, group := range groups {
		if group.Status == StatusActive {
			items = append(items, BenchmarkGroupOption{group.ID, group.Name, group.Platform})
		}
	}
	return items, nil
}

func (s *ModelBenchmarkGroupService) group(ctx context.Context, userID, groupID int64) (*Group, error) {
	if userID <= 0 || groupID <= 0 {
		return nil, infraerrors.BadRequest("BENCHMARK_INVALID_GROUP", "请选择有效的分组")
	}
	groups, err := s.Groups(ctx, userID)
	if err != nil {
		return nil, err
	}
	if !slices.ContainsFunc(groups, func(g BenchmarkGroupOption) bool { return g.ID == groupID }) {
		return nil, infraerrors.BadRequest("BENCHMARK_GROUP_UNAVAILABLE", "分组已停用或当前管理员账号无权使用该分组")
	}
	group, err := s.admin.GetGroup(ctx, groupID)
	if err != nil {
		return nil, err
	}
	if group.Status != StatusActive {
		return nil, infraerrors.BadRequest("BENCHMARK_GROUP_UNAVAILABLE", "分组已停用")
	}
	return group, nil
}

func (s *ModelBenchmarkGroupService) models(ctx context.Context, group *Group) ([]string, error) {
	models, err := s.admin.GetGroupModelsListCandidates(ctx, group.ID, group.Platform)
	if err != nil {
		return nil, err
	}
	models = group.ModelAllowlist.FilterForListing(models)
	models = slices.DeleteFunc(models, func(m string) bool { return !benchmarkGroupModel(m) })
	if models == nil {
		return []string{}, nil
	}
	sort.Strings(models)
	return slices.Compact(models), nil
}

func benchmarkGroupModel(value string) bool {
	return value != "" && utf8.ValidString(value) && utf8.RuneCountInString(value) <= 200 && strings.TrimSpace(value) == value && !strings.Contains(value, "*") && strings.IndexFunc(value, unicode.IsControl) < 0
}

func (s *ModelBenchmarkGroupService) Models(ctx context.Context, userID, groupID int64) ([]string, error) {
	group, err := s.group(ctx, userID, groupID)
	if err != nil {
		return nil, err
	}
	return s.models(ctx, group)
}

func (s *ModelBenchmarkGroupService) Target(ctx context.Context, userID, groupID int64, model, effort string) (*BenchmarkGroupTarget, error) {
	group, err := s.group(ctx, userID, groupID)
	if err != nil {
		return nil, err
	}
	models, err := s.models(ctx, group)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(models, model) {
		return nil, infraerrors.BadRequest("BENCHMARK_INVALID_MODEL", "请选择该分组提供的模型")
	}
	if !slices.Contains([]string{"low", "medium", "high", "xhigh"}, effort) {
		return nil, infraerrors.BadRequest("BENCHMARK_INVALID_EFFORT", "推理强度不受支持")
	}
	baseURL, err := s.baseURL(ctx)
	if err != nil {
		return nil, err
	}
	// Reuse only this administrator's dedicated key, and only while it still
	// belongs to the selected group. Never reuse an arbitrary user's credential.
	s.mu.Lock()
	defer s.mu.Unlock()
	name := fmt.Sprintf("model-benchmark-group-%d", group.ID)
	keys, _, err := s.keys.List(ctx, userID, pagination.PaginationParams{Page: 1, PageSize: 100}, APIKeyListFilters{Search: name, GroupID: &group.ID})
	if err != nil {
		return nil, err
	}
	key := ""
	for _, candidate := range keys {
		if candidate.Name != name || candidate.UserID != userID || candidate.GroupID == nil || *candidate.GroupID != group.ID {
			continue
		}
		if !candidate.IsActive() || candidate.IsExpired() || candidate.IsQuotaExhausted() {
			return nil, infraerrors.BadRequest("BENCHMARK_KEY_UNAVAILABLE", "该分组的专用测试密钥已停用、过期或额度不足，请在密钥管理中处理")
		}
		key = candidate.Key
		break
	}
	if key == "" {
		created, err := s.keys.Create(ctx, userID, CreateAPIKeyRequest{Name: name, GroupID: &group.ID})
		if err != nil {
			return nil, err
		}
		key = created.Key
	}
	return &BenchmarkGroupTarget{group.ID, group.Name, baseURL, key, model, effort}, nil
}

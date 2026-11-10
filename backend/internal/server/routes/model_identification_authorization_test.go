package routes

import (
	"context"
	"database/sql"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/pkg/adminaccess"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type identificationAuthUsers struct {
	service.UserRepository
	users map[int64]*service.User
}

func (r *identificationAuthUsers) GetByID(_ context.Context, id int64) (*service.User, error) {
	user, ok := r.users[id]
	if !ok {
		return nil, service.ErrUserNotFound
	}
	copy := *user
	return &copy, nil
}
func (*identificationAuthUsers) GetUserAvatar(context.Context, int64) (*service.UserAvatar, error) {
	return nil, nil
}
func (*identificationAuthUsers) UpdateUserLastActiveAt(context.Context, int64, time.Time) error {
	return nil
}

type identificationAuthPlans struct {
	service.DegradationTestPlanRepository
}

func (*identificationAuthPlans) GetByID(_ context.Context, id int64) (*service.DegradationTestPlan, error) {
	switch id {
	case 1:
		target := int64(10)
		return &service.DegradationTestPlan{ID: 1, GroupID: &target, ModelID: "public-model"}, nil
	case 2:
		return &service.DegradationTestPlan{ID: 2, AccountID: 99, ModelID: "private-model"}, nil
	default:
		return nil, sql.ErrNoRows
	}
}

type identificationAuthResults struct {
	service.DegradationTestResultRepository
}

func (*identificationAuthResults) ListByPlanID(_ context.Context, id int64, _ int) ([]*service.DegradationTestResult, error) {
	if id != 1 {
		panic("不应读取私有账号计划的结果")
	}
	return []*service.DegradationTestResult{
		{ID: 11, PlanID: 1, Status: "failed", ResponseText: "private-partial", ErrorMessage: "private-error"},
		{ID: 12, PlanID: 1, Status: "success", ResponseText: "<svg>public-artwork</svg>"},
	}, nil
}

type identificationAuthTargets struct {
	service.ModelIdentificationRepository
}

func (*identificationAuthTargets) GetTarget(_ context.Context, id int64) (*service.ModelIdentificationTarget, error) {
	if id != 10 {
		panic("不应把展示分组编号当成目标编号")
	}
	display := int64(88)
	return &service.ModelIdentificationTarget{ID: 10, DisplayGroupID: &display, Name: "共享分组", Endpoint: "https://private.example.com", EncryptedAPIKey: "cipher-secret", Protocol: "responses"}, nil
}
func (*identificationAuthTargets) ListGroups(context.Context) ([]*service.ModelIdentificationGroup, error) {
	display, target := int64(88), int64(10)
	return []*service.ModelIdentificationGroup{{GroupID: target, DisplayGroupID: &display, Name: "共享分组", Platform: "responses", Plan: &service.DegradationTestPlan{ID: 1, GroupID: &target}}}, nil
}

type identificationAuthEncryptor struct{}

func (identificationAuthEncryptor) Encrypt(string) (string, error) {
	panic("权限拒绝前不应保存配置")
}
func (identificationAuthEncryptor) Decrypt(string) (string, error) { return "admin-secret", nil }

func TestModelIdentificationRealAuthenticationBoundaries(t *testing.T) {
	gin.SetMode(gin.TestMode)
	users := &identificationAuthUsers{users: map[int64]*service.User{
		1: {ID: 1, Email: "admin@example.invalid", Role: service.RoleAdmin, Status: service.StatusActive, TokenVersion: 1, TokenVersionResolved: true},
		2: {ID: 2, Email: "user@example.invalid", Role: service.RoleUser, Status: service.StatusActive, TokenVersion: 1, TokenVersionResolved: true},
		3: {ID: 3, Email: "disabled@example.invalid", Role: service.RoleAdmin, Status: "disabled", TokenVersion: 1, TokenVersionResolved: true},
	}}
	auth := service.NewAuthService(nil, users, nil, nil, &config.Config{JWT: config.JWTConfig{Secret: "authorization-test-secret", ExpireHour: 1}}, nil, nil, nil, nil, nil, nil, nil, nil)
	userSvc := service.NewUserService(users, nil, nil, nil)
	mint := func(id int64, role string, revoked bool) string {
		t.Helper()
		user := *users.users[id]
		user.Role = role
		if revoked {
			user.TokenVersion = 0
		}
		token, err := auth.GenerateToken(context.Background(), &user)
		require.NoError(t, err)
		return token
	}
	adminToken := mint(1, service.RoleAdmin, false)
	userToken := mint(2, service.RoleUser, false)
	spoofedRoleToken := mint(2, service.RoleAdmin, false)
	revokedToken := mint(1, service.RoleAdmin, true)
	disabledToken := mint(3, service.RoleAdmin, false)
	svc := service.NewModelIdentificationService(service.NewDegradationTestService(&identificationAuthPlans{}, &identificationAuthResults{}), &identificationAuthTargets{}, identificationAuthEncryptor{})
	h := &handler.Handlers{Admin: &handler.AdminHandlers{}, ModelIdentification: handler.NewModelIdentificationHandler(svc, nil)}
	router := gin.New()
	audit := middleware.AuditLogMiddleware(func(c *gin.Context) { c.Next() })
	stepUp := middleware.StepUpAuthMiddleware(func(c *gin.Context) { c.Next() })
	RegisterAdminRoutes(router.Group("/api/v1"), h, middleware.NewAdminAuthMiddleware(auth, userSvc, nil, nil), audit, stepUp, nil, nil)
	RegisterUserRoutes(router.Group("/api/v1"), h, middleware.NewJWTAuthMiddleware(auth, userSvc, nil, nil), audit, nil, nil)
	request := func(method, path, token string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader("{}"))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-User-Role", "admin")
		req.Header.Set("X-Admin", "true")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	routes := []struct{ method, path string }{
		{"GET", "/api/v1/admin/model-identification/plans/1"},
		{"POST", "/api/v1/admin/model-identification/plans"},
		{"PUT", "/api/v1/admin/model-identification/plans/1"},
		{"DELETE", "/api/v1/admin/model-identification/plans/1"},
		{"POST", "/api/v1/admin/model-identification/plans/1/run"},
		{"GET", "/api/v1/admin/groups/all?include_inactive=true"},
	}
	for _, route := range routes {
		for _, identity := range []struct {
			name, token string
			status      int
		}{
			{"anonymous", "", 401}, {"normal-user", userToken, 403}, {"forged-role", spoofedRoleToken, 403}, {"revoked", revokedToken, 401}, {"disabled", disabledToken, 401},
		} {
			t.Run(route.method+route.path+"/"+identity.name, func(t *testing.T) {
				w := request(route.method, route.path, identity.token)
				require.Equal(t, identity.status, w.Code, w.Body.String())
				require.NotContains(t, w.Body.String(), "admin-secret")
			})
		}
	}
	for _, path := range []string{"/api/v1/model-identification/groups", "/api/v1/model-identification/plans/1/results"} {
		require.Equal(t, 401, request("GET", path, "").Code)
		require.Equal(t, 401, request("GET", path, revokedToken).Code)
		require.Equal(t, 401, request("GET", path, disabledToken).Code)
		w := request("GET", path+"?admin=true&role=admin", userToken)
		require.Equal(t, 200, w.Code, w.Body.String())
		for _, secret := range []string{"admin-secret", "private.example.com", "cipher-secret", "private-partial", "private-error", "api_key", "endpoint"} {
			require.NotContains(t, w.Body.String(), secret)
		}
	}
	for _, id := range []string{"2", "10", "88", "999"} {
		require.Equal(t, 404, request("GET", "/api/v1/model-identification/plans/"+id+"/results?admin=true", userToken).Code)
	}
	require.Equal(t, 404, request("GET", "/api/v1/admin/model-identification/plans/2", adminToken).Code)
	w := request("GET", "/api/v1/admin/model-identification/plans/1", adminToken)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "admin-secret")
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	w = request("GET", "/api/v1/model-identification/plans/1/results", adminToken)
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), "private-error")
	w = request("GET", "/api/v1/model-identification/plans/1/results?admin=true", spoofedRoleToken)
	require.Equal(t, 200, w.Code)
	require.NotContains(t, w.Body.String(), "private-error")
}

func TestModelIdentificationScopedAdminPermissionsDenyByDefault(t *testing.T) {
	policy := adminaccess.Policy{}
	rule := adminaccess.Rule{AllGroups: true, IncludeUngrouped: true, AccountTypes: []string{"oauth", "apikey"}}
	for _, permission := range adminaccess.PermissionCatalog() {
		if permission.Scope == "global" {
			policy.GlobalPermissions = append(policy.GlobalPermissions, permission.ID)
		} else {
			rule.Permissions = append(rule.Permissions, permission.ID)
		}
	}
	policy.Rules = []adminaccess.Rule{rule}
	for _, route := range []struct{ method, path string }{
		{"GET", "/api/v1/admin/model-identification/plans/:id"},
		{"POST", "/api/v1/admin/model-identification/plans"},
		{"PUT", "/api/v1/admin/model-identification/plans/:id"},
		{"DELETE", "/api/v1/admin/model-identification/plans/:id"},
		{"POST", "/api/v1/admin/model-identification/plans/:id/run"},
	} {
		require.False(t, adminaccess.RouteAllowed(policy, route.method, route.path), route.path)
	}
}

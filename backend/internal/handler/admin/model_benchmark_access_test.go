//go:build unit

package admin

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type benchmarkCreditUsers struct {
	benchmarkUsersStub
	credited  bool
	err       error
	checkedID int64
}

func (s *benchmarkCreditUsers) HasUserBalanceCredit(_ context.Context, id int64) (bool, error) {
	s.checkedID = id
	return s.credited, s.err
}

func TestModelBenchmarkUserBalanceCreditGate(t *testing.T) {
	for _, tc := range []struct {
		name           string
		role           string
		credited       bool
		missingService bool
		err            error
		want           int
	}{
		{name: "未充值普通用户", role: service.RoleUser, want: 403},
		{name: "已入账普通用户", role: service.RoleUser, credited: true, want: 200},
		{name: "管理员豁免", role: service.RoleAdmin, want: 200},
		{name: "缺少角色不能绕过", want: 403},
		{name: "查询失败不放行", role: service.RoleUser, err: errors.New("database unavailable"), want: 503},
		{name: "未注入服务不放行", role: service.RoleUser, missingService: true, want: 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hits := 0
			worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits++
				require.True(t, strings.HasPrefix(r.URL.Path, "/users/7/"))
				_, _ = w.Write([]byte(`{}`))
			}))
			defer worker.Close()
			t.Setenv("MODEL_BENCHMARK_WORKER_URL", worker.URL)
			t.Setenv("MODEL_BENCHMARK_WORKER_TOKEN", "worker-secret")
			h := NewModelBenchmarkHandler()
			users := &benchmarkCreditUsers{credited: tc.credited, err: tc.err}
			if !tc.missingService {
				h.users = users
			}
			router := gin.New()
			router.Use(func(c *gin.Context) {
				c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 7})
				c.Set(string(middleware.ContextKeyUserRole), tc.role)
			})
			router.POST("/api/v1/model-benchmarks/runs", h.User)
			router.POST("/api/v1/model-benchmarks/runs/:id/rerun", h.User)
			router.POST("/api/v1/model-benchmarks/runs/:id/cancel", h.User)
			router.GET("/api/v1/model-benchmarks/runs", h.User)
			id := strings.Repeat("a", 32)
			for _, path := range []string{"/runs", "/runs/" + id + "/rerun"} {
				hits = 0
				w := httptest.NewRecorder()
				req := httptest.NewRequest("POST", "/api/v1/model-benchmarks"+path, strings.NewReader(`{"role":"admin","owner_user_id":8,"total_recharged":100,"balance":100}`))
				router.ServeHTTP(w, req)
				require.Equal(t, tc.want, w.Code, w.Body.String())
				if tc.want == 200 {
					require.Equal(t, 1, hits)
				} else {
					require.Zero(t, hits, "被拒绝的请求不能进入执行器或消耗测试次数")
				}
				if tc.want == 403 {
					require.Contains(t, w.Body.String(), "当前帐号权重太低,无法进行掺水测试,请联系管理员")
				}
			}
			if tc.role == service.RoleAdmin || tc.missingService {
				require.Zero(t, users.checkedID)
			} else {
				require.EqualValues(t, 7, users.checkedID, "只检查认证主体，忽略伪造的用户编号")
			}
			for _, endpoint := range []struct{ method, path string }{{"GET", "/runs"}, {"POST", "/runs/" + id + "/cancel"}} {
				w := httptest.NewRecorder()
				router.ServeHTTP(w, httptest.NewRequest(endpoint.method, "/api/v1/model-benchmarks"+endpoint.path, nil))
				require.Equal(t, 200, w.Code, "查看和取消历史测试不受充值门槛限制")
			}
		})
	}
}

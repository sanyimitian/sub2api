package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type assessmentRepoCapture struct {
	service.UsageLogRepository
	filter service.DownstreamAssessmentFilter
	params pagination.PaginationParams
	called bool
	err    error
}

func (r *assessmentRepoCapture) GetDownstreamAssessment(_ context.Context, params pagination.PaginationParams, filter service.DownstreamAssessmentFilter) (*service.DownstreamAssessmentResult, error) {
	r.filter, r.params, r.called = filter, params, true
	return &service.DownstreamAssessmentResult{Items: []service.DownstreamAssessmentUser{}, Days: filter.Days, DailyThreshold: filter.DailyThreshold, Page: params.Page, PageSize: params.PageSize}, r.err
}

type assessmentAdminStub struct {
	service.AdminService
	err error
}

func (s *assessmentAdminStub) GetGroup(_ context.Context, id int64) (*service.Group, error) {
	return &service.Group{ID: id}, s.err
}

func assessmentRouter(repo *assessmentRepoCapture, admin *assessmentAdminStub) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	h := NewUsageHandler(service.NewUsageService(repo, nil, nil, nil), nil, admin, nil)
	router.GET("/assessment", h.GetDownstreamAssessment)
	return router
}

func assessmentQuery() url.Values {
	return url.Values{"group_id": {"7"}, "daily_threshold": {"1000000000"}, "start_date": {"2026-10-01"}, "end_date": {"2026-10-07"}, "timezone": {"Asia/Shanghai"}}
}

func TestDownstreamAssessmentValidation(t *testing.T) {
	cases := []struct{ key, value string }{
		{"group_id", ""}, {"group_id", "0"}, {"group_id", "-1"}, {"group_id", "bad"},
		{"daily_threshold", ""}, {"daily_threshold", "0"}, {"daily_threshold", "-1"},
		{"daily_threshold", "1.5"}, {"daily_threshold", "NaN"}, {"daily_threshold", "9223372036854775808"},
		{"start_date", ""}, {"start_date", "2026-02-30"}, {"end_date", ""},
		{"end_date", "2026-09-30"}, {"timezone", "Invalid/Zone"},
		{"sort_by", "email"}, {"sort_by", "total_tokens; DROP TABLE users"}, {"sort_by", ""},
		{"sort_order", "invalid"}, {"sort_order", "desc; --"}, {"sort_order", ""},
	}
	for _, tc := range cases {
		t.Run(tc.key+"="+tc.value, func(t *testing.T) {
			repo := &assessmentRepoCapture{}
			query := assessmentQuery()
			query.Set(tc.key, tc.value)
			rec := httptest.NewRecorder()
			assessmentRouter(repo, &assessmentAdminStub{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/assessment?"+query.Encode(), nil))
			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			require.False(t, repo.called)
		})
	}
}

func TestDownstreamAssessmentDatesAndPagination(t *testing.T) {
	for _, tc := range []struct {
		name, start, end, zone, startUTC, endUTC string
		days                                     int
	}{
		{"inclusive week", "2026-10-01", "2026-10-07", "Asia/Shanghai", "2026-09-30T16:00:00Z", "2026-10-07T16:00:00Z", 7},
		{"single day", "2026-10-01", "2026-10-01", "Asia/Shanghai", "2026-09-30T16:00:00Z", "2026-10-01T16:00:00Z", 1},
		{"spring DST", "2026-03-07", "2026-03-09", "America/New_York", "2026-03-07T05:00:00Z", "2026-03-10T04:00:00Z", 3},
		{"fall DST", "2026-10-31", "2026-11-02", "America/New_York", "2026-10-31T04:00:00Z", "2026-11-03T05:00:00Z", 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &assessmentRepoCapture{}
			query := assessmentQuery()
			query.Set("start_date", tc.start)
			query.Set("end_date", tc.end)
			query.Set("timezone", tc.zone)
			query.Set("page", "2")
			query.Set("page_size", "50")
			rec := httptest.NewRecorder()
			assessmentRouter(repo, &assessmentAdminStub{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/assessment?"+query.Encode(), nil))
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			require.Equal(t, tc.days, repo.filter.Days)
			require.Equal(t, tc.startUTC, repo.filter.StartTime.UTC().Format(time.RFC3339))
			require.Equal(t, tc.endUTC, repo.filter.EndTime.UTC().Format(time.RFC3339))
			require.Equal(t, int64(1000000000), repo.filter.DailyThreshold)
			require.Equal(t, int64(7), repo.filter.GroupID)
			require.Equal(t, "total_tokens", repo.filter.SortBy)
			require.Equal(t, "asc", repo.filter.SortOrder)
			require.Equal(t, 2, repo.params.Page)
			require.Equal(t, 50, repo.params.PageSize)
			var body struct {
				Data service.DownstreamAssessmentResult `json:"data"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
			require.NotNil(t, body.Data.Items)
			require.Equal(t, tc.days, body.Data.Days)
		})
	}
}

func TestDownstreamAssessmentSorting(t *testing.T) {
	for _, field := range []string{"total_tokens", "daily_average"} {
		for _, direction := range []string{"asc", "desc"} {
			t.Run(field+"/"+direction, func(t *testing.T) {
				repo := &assessmentRepoCapture{}
				query := assessmentQuery()
				query.Set("sort_by", field)
				query.Set("sort_order", direction)
				query.Set("page", "2")
				rec := httptest.NewRecorder()
				assessmentRouter(repo, &assessmentAdminStub{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/assessment?"+query.Encode(), nil))
				require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
				require.Equal(t, field, repo.filter.SortBy)
				require.Equal(t, direction, repo.filter.SortOrder)
				require.Equal(t, 2, repo.params.Page)
			})
		}
	}
}

func TestDownstreamAssessmentErrors(t *testing.T) {
	t.Run("missing group", func(t *testing.T) {
		repo := &assessmentRepoCapture{}
		rec := httptest.NewRecorder()
		assessmentRouter(repo, &assessmentAdminStub{err: service.ErrGroupNotFound}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/assessment?"+assessmentQuery().Encode(), nil))
		require.Equal(t, http.StatusNotFound, rec.Code)
		require.False(t, repo.called)
	})
	t.Run("database error", func(t *testing.T) {
		repo := &assessmentRepoCapture{err: errors.New("database unavailable")}
		rec := httptest.NewRecorder()
		assessmentRouter(repo, &assessmentAdminStub{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/assessment?"+assessmentQuery().Encode(), nil))
		require.Equal(t, http.StatusInternalServerError, rec.Code)
	})
}

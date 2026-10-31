package admin

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type balanceRechargeRouteService struct {
	*stubAdminService
	directCalls   int
	recordedCalls int
}

func (s *balanceRechargeRouteService) UpdateUserBalance(ctx context.Context, userID int64, amount float64, operation, notes string) (*service.User, error) {
	s.directCalls++
	return s.stubAdminService.UpdateUserBalance(ctx, userID, amount, operation, notes)
}

func (s *balanceRechargeRouteService) RecordManualRecharge(ctx context.Context, userID int64, amount float64, platform, recipient, notes string) (*service.User, error) {
	s.recordedCalls++
	return s.stubAdminService.RecordManualRecharge(ctx, userID, amount, platform, recipient, notes)
}

func TestManualRechargeAmountThreshold(t *testing.T) {
	for _, tc := range []struct {
		name          string
		body          string
		status        int
		directCalls   int
		recordedCalls int
	}{
		{"below threshold", `{"balance":2.99,"operation":"add"}`, http.StatusOK, 1, 0},
		{"threshold missing receipt", `{"balance":3,"operation":"add"}`, http.StatusBadRequest, 0, 0},
		{"threshold with receipt", `{"balance":3,"operation":"add","platform":"wechat","recipient":"鹏"}`, http.StatusOK, 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			router := gin.New()
			svc := &balanceRechargeRouteService{stubAdminService: newStubAdminService()}
			router.POST("/api/v1/admin/users/:id/balance", NewUserHandler(svc, nil, nil, nil, nil, nil, nil).UpdateBalance)
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/users/1/balance", bytes.NewBufferString(tc.body))
			request.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(recorder, request)
			require.Equal(t, tc.status, recorder.Code, recorder.Body.String())
			require.Equal(t, tc.directCalls, svc.directCalls)
			require.Equal(t, tc.recordedCalls, svc.recordedCalls)
		})
	}
}

func TestManualRechargeRequiresReceiptDetails(t *testing.T) {
	router, _ := setupAdminRouter()
	for _, tc := range []struct {
		name   string
		body   string
		status int
	}{
		{"missing receipt", `{"balance":10,"operation":"add"}`, http.StatusBadRequest},
		{"other without notes", `{"balance":10,"operation":"add","platform":"other","recipient":"鹏"}`, http.StatusBadRequest},
		{"wechat without notes", `{"balance":10,"operation":"add","platform":"wechat","recipient":"鹏"}`, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/users/1/balance", bytes.NewBufferString(tc.body))
			request.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(recorder, request)
			require.Equal(t, tc.status, recorder.Code, recorder.Body.String())
		})
	}
}

func TestManualRechargeDeductionRequiresNotesAndValidScreenshot(t *testing.T) {
	router, _ := setupAdminRouter()
	handler := NewUserHandler(newStubAdminService(), nil, nil, nil, nil, nil, nil)
	router.POST("/api/v1/admin/users/manual-recharges/deductions", handler.DeductManualRecharge)
	for _, tc := range []struct {
		name   string
		notes  string
		file   []byte
		status int
	}{
		{"blank notes", "  ", nil, http.StatusBadRequest},
		{"invalid screenshot", "手续费", []byte("plain text"), http.StatusBadRequest},
		{"valid no screenshot", "手续费", nil, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body bytes.Buffer
			writer := multipart.NewWriter(&body)
			for key, value := range map[string]string{"recipient": "鹏", "amount": "10", "notes": tc.notes} {
				require.NoError(t, writer.WriteField(key, value))
			}
			if tc.file != nil {
				file, err := writer.CreateFormFile("screenshot", "test.png")
				require.NoError(t, err)
				_, err = file.Write(tc.file)
				require.NoError(t, err)
			}
			require.NoError(t, writer.Close())
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/users/manual-recharges/deductions", &body)
			request.Header.Set("Content-Type", writer.FormDataContentType())
			router.ServeHTTP(recorder, request)
			require.Equal(t, tc.status, recorder.Code, recorder.Body.String())
		})
	}
}

package admin

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

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

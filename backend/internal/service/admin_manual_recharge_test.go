//go:build unit

package service

import (
	"context"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/stretchr/testify/require"
)

func TestDeductManualRechargeScreenshotArguments(t *testing.T) {
	for _, tc := range []struct {
		name       string
		screenshot []byte
		mime       string
		wantImage  any
		wantMIME   any
	}{
		{"without screenshot", nil, "", nil, nil},
		{"with screenshot", []byte{0x89, 'P', 'N', 'G'}, "image/png", []byte{0x89, 'P', 'N', 'G'}, "image/png"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
			svc := &adminServiceImpl{entClient: client}
			mock.ExpectBegin()
			mock.ExpectExec("UPDATE manual_recharge_accounts").WithArgs("鹏", 10.0).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectExec("INSERT INTO manual_recharge_deductions").WithArgs("鹏", 10.0, "手续费", tc.wantImage, tc.wantMIME).WillReturnResult(sqlmock.NewResult(1, 1))
			mock.ExpectCommit()
			require.NoError(t, svc.DeductManualRecharge(context.Background(), "鹏", 10, "手续费", tc.screenshot, tc.mime))
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

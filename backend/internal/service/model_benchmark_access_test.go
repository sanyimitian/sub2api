//go:build unit

package service

import (
	"context"
	"database/sql"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func TestHasUserBalanceCredit(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup string
		want  bool
	}{
		{"从未入账", "", false},
		{"注册默认赠送", "UPDATE users SET balance = 100", false},
		{"注册优惠码赠送", "UPDATE users SET balance = 20, total_recharged = 20; INSERT INTO promo_code_usages VALUES (7, 20)", false},
		{"多次注册赠送不算充值", "UPDATE users SET total_recharged = 30; INSERT INTO promo_code_usages VALUES (7, 10), (7, 20)", false},
		{"赠送后实际充值", "UPDATE users SET total_recharged = 21; INSERT INTO promo_code_usages VALUES (7, 20); INSERT INTO redeem_codes VALUES (7, 'used', 1, 'balance')", true},
		{"余额已用完", "UPDATE users SET balance = 0; INSERT INTO redeem_codes VALUES (7, 'used', 1, 'balance')", true},
		{"余额为负", "UPDATE users SET balance = -10; INSERT INTO redeem_codes VALUES (7, 'used', 1, 'balance')", true},
		{"注册优惠码被删除后累计金额仍不能放行", "UPDATE users SET total_recharged = 20", false},
		{"在线充值或余额兑换", "INSERT INTO redeem_codes VALUES (7, 'used', 1, 'balance')", true},
		{"管理员增加余额", "INSERT INTO redeem_codes VALUES (7, 'used', 1, 'admin_balance')", true},
		{"手动充值历史", "INSERT INTO manual_recharge_records VALUES (7, 1)", true},
		{"在线充值的兑换码被删除后仍有订单凭据", "INSERT INTO payment_orders VALUES (7, 'balance', 1, '2026-11-22')", true},
		{"待入账订单不算充值", "INSERT INTO payment_orders VALUES (7, 'balance', 1, NULL)", false},
		{"已完成订阅订单不算余额", "INSERT INTO payment_orders VALUES (7, 'subscription', 1, '2026-11-22')", false},
		{"返佣已转余额", "INSERT INTO user_affiliate_ledger VALUES (7, 'transfer', 1)", true},
		{"未转余额的返佣", "INSERT INTO user_affiliate_ledger VALUES (7, 'rebate', 1)", false},
		{"订阅兑换不算余额", "INSERT INTO redeem_codes VALUES (7, 'used', 1, 'subscription')", false},
		{"并发数兑换不算余额", "INSERT INTO redeem_codes VALUES (7, 'used', 1, 'concurrency')", false},
		{"邀请码不算余额", "INSERT INTO redeem_codes VALUES (7, 'used', 1, 'invitation')", false},
		{"未使用的兑换码", "INSERT INTO redeem_codes VALUES (7, 'unused', 1, 'balance')", false},
		{"扣减余额", "INSERT INTO redeem_codes VALUES (7, 'used', -1, 'admin_balance')", false},
		{"零元记录", "INSERT INTO redeem_codes VALUES (7, 'used', 0, 'balance'); INSERT INTO manual_recharge_records VALUES (7, 0); INSERT INTO user_affiliate_ledger VALUES (7, 'transfer', 0)", false},
		{"其他用户入账", "INSERT INTO redeem_codes VALUES (8, 'used', 1, 'balance'); INSERT INTO manual_recharge_records VALUES (8, 1); INSERT INTO user_affiliate_ledger VALUES (8, 'transfer', 1)", false},
		{"已删除用户", "UPDATE users SET deleted_at = '2026-11-22', total_recharged = 100", false},
		{"不存在的用户", "DELETE FROM users", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, err := sql.Open("sqlite", ":memory:")
			require.NoError(t, err)
			db.SetMaxOpenConns(1)
			client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.SQLite, db)))
			t.Cleanup(func() { _ = client.Close() })
			_, err = db.Exec(`
				CREATE TABLE users (id INTEGER, balance NUMERIC DEFAULT 0, total_recharged NUMERIC DEFAULT 0, deleted_at TEXT);
				CREATE TABLE promo_code_usages (user_id INTEGER, bonus_amount NUMERIC);
				CREATE TABLE redeem_codes (used_by INTEGER, status TEXT, value NUMERIC, type TEXT);
				CREATE TABLE manual_recharge_records (user_id INTEGER, amount NUMERIC);
				CREATE TABLE user_affiliate_ledger (user_id INTEGER, action TEXT, amount NUMERIC);
				CREATE TABLE payment_orders (user_id INTEGER, order_type TEXT, amount NUMERIC, completed_at TEXT);
				INSERT INTO users (id) VALUES (7);
			` + tc.setup)
			require.NoError(t, err)
			svc := &adminServiceImpl{entClient: client}
			got, err := svc.HasUserBalanceCredit(context.Background(), 7)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
			_, err = db.Exec("DROP TABLE users")
			require.NoError(t, err)
			got, err = svc.HasUserBalanceCredit(context.Background(), 7)
			require.Error(t, err)
			require.False(t, got, "查询异常不能放行")
		})
	}
	got, err := (&adminServiceImpl{}).HasUserBalanceCredit(context.Background(), 7)
	require.Error(t, err)
	require.False(t, got)
}

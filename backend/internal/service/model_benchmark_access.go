package service

import (
	"context"
	"errors"
)

// HasUserBalanceCredit 使用已落库的入账历史，不以当前余额或浏览器提交的数据判断。
// 累计充值混有注册优惠码赠送，且赠送记录可被级联删除，不能用于证明充值。
// 在线充值、管理员加款和返佣分别使用其实际入账记录；不要求当前余额为正。
func (s *adminServiceImpl) HasUserBalanceCredit(ctx context.Context, userID int64) (bool, error) {
	if s.entClient == nil {
		return false, errors.New("balance credit lookup requires database access")
	}
	const query = `
		SELECT EXISTS (
			SELECT 1 FROM users u
			WHERE u.id = $1 AND u.deleted_at IS NULL AND (
				EXISTS (
					SELECT 1 FROM redeem_codes r WHERE r.used_by = u.id
					AND r.status = 'used' AND r.value > 0
					AND r.type IN ('balance', 'admin_balance')
				)
				OR EXISTS (
					SELECT 1 FROM manual_recharge_records m
					WHERE m.user_id = u.id AND m.amount > 0
				)
				OR EXISTS (
					SELECT 1 FROM user_affiliate_ledger a
					WHERE a.user_id = u.id AND a.action = 'transfer' AND a.amount > 0
				)
				OR EXISTS (
					SELECT 1 FROM payment_orders p
					WHERE p.user_id = u.id AND p.order_type = 'balance'
					AND p.amount > 0 AND p.completed_at IS NOT NULL
				)
			)
		)`
	rows, err := s.entClient.QueryContext(ctx, query, userID)
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return false, err
		}
		return false, errors.New("balance credit lookup returned no result")
	}
	var credited bool
	if err := rows.Scan(&credited); err != nil {
		return false, err
	}
	return credited, rows.Err()
}

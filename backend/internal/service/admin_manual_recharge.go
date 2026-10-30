package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

type ManualRechargeFilter struct {
	From          *time.Time
	To            *time.Time
	Platform      string
	Recipient     string
	Search        string
	Page          int
	PageSize      int
	DeductionPage int
}

type ManualRechargeRecord struct {
	ID        int64     `json:"id"`
	UserID    int64     `json:"user_id"`
	UserEmail string    `json:"user_email"`
	Amount    float64   `json:"amount"`
	Platform  string    `json:"platform"`
	Recipient string    `json:"recipient"`
	Notes     string    `json:"notes"`
	CreatedAt time.Time `json:"created_at"`
}

type ManualRechargeBreakdown struct {
	Platform  string  `json:"platform"`
	Recipient string  `json:"recipient"`
	Count     int64   `json:"count"`
	Amount    float64 `json:"amount"`
}

type ManualRechargeAccount struct {
	Recipient string  `json:"recipient"`
	Balance   float64 `json:"balance"`
}

type ManualRechargeDeduction struct {
	ID            int64     `json:"id"`
	Recipient     string    `json:"recipient"`
	Amount        float64   `json:"amount"`
	Notes         string    `json:"notes"`
	CreatedAt     time.Time `json:"created_at"`
	HasScreenshot bool      `json:"has_screenshot"`
}

type ManualRechargeStats struct {
	Items          []ManualRechargeRecord    `json:"items"`
	Total          int64                     `json:"total"`
	Amount         float64                   `json:"amount"`
	Breakdown      []ManualRechargeBreakdown `json:"breakdown"`
	Accounts       []ManualRechargeAccount   `json:"accounts"`
	Deductions     []ManualRechargeDeduction `json:"deductions"`
	Deducted       float64                   `json:"deducted"`
	DeductionTotal int64                     `json:"deduction_total"`
	DeductionPage  int                       `json:"deduction_page"`
	Page           int                       `json:"page"`
	PageSize       int                       `json:"page_size"`
}

func validManualRechargePlatform(platform string) bool {
	return platform == "wechat" || platform == "alipay" || platform == "other"
}

func validManualRechargeRecipient(recipient string) bool {
	return recipient == "鹏" || recipient == "杰" || recipient == "翔"
}

func (s *adminServiceImpl) RecordManualRecharge(ctx context.Context, userID int64, amount float64, platform, recipient, notes string) (*User, error) {
	notes = strings.TrimSpace(notes)
	if !validManualRechargePlatform(platform) || !validManualRechargeRecipient(recipient) ||
		platform == "other" && notes == "" || amount <= 0 || math.IsNaN(amount) || math.IsInf(amount, 0) {
		return nil, infraerrors.BadRequest("INVALID_MANUAL_RECHARGE", "无效的充值信息")
	}
	if s.entClient == nil {
		return nil, errors.New("manual recharge requires a database transaction")
	}
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	txCtx := dbent.NewTxContext(ctx, tx)
	change, err := s.userRepo.AdjustBalance(txCtx, userID, amount)
	if err != nil {
		return nil, err
	}
	userEntity, err := tx.Client().User.Get(txCtx, userID)
	if err != nil {
		return nil, err
	}
	code, err := GenerateRedeemCode()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	adjustment := &RedeemCode{Code: code, Type: AdjustmentTypeAdminBalance, Value: change.New - change.Old,
		Status: StatusUsed, UsedBy: &userID, UsedAt: &now, Notes: notes}
	if err := s.redeemCodeRepo.Create(txCtx, adjustment); err != nil {
		return nil, err
	}
	_, err = tx.Client().ExecContext(txCtx, `
		INSERT INTO manual_recharge_records (user_id, user_email, amount, platform, recipient, notes, redeem_code_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`, userID, userEntity.Email, adjustment.Value, platform, recipient, notes, adjustment.ID)
	if err != nil {
		return nil, fmt.Errorf("save manual recharge record: %w", err)
	}
	_, err = tx.Client().ExecContext(txCtx, `
		INSERT INTO manual_recharge_accounts (recipient, balance) VALUES ($1, $2)
		ON CONFLICT (recipient) DO UPDATE SET balance = manual_recharge_accounts.balance + EXCLUDED.balance, updated_at = NOW()`, recipient, adjustment.Value)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	if s.authCacheInvalidator != nil {
		s.authCacheInvalidator.InvalidateAuthCacheByUserID(ctx, userID)
	}
	if s.billingCacheService != nil {
		if err := s.billingCacheService.InvalidateUserBalance(ctx, userID); err != nil {
			logger.LegacyPrintf("service.admin", "invalidate user balance cache failed: user_id=%d err=%v", userID, err)
		}
	}
	s.tryAccrueAffiliateRebateForAdminRecharge(ctx, userID, "add", amount)
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		logger.LegacyPrintf("service.admin", "reload recharged user failed: user_id=%d err=%v", userID, err)
		return &User{ID: userID, Email: userEntity.Email, Balance: change.New}, nil
	}
	return user, nil
}

func (s *adminServiceImpl) DeductManualRecharge(ctx context.Context, recipient string, amount float64, notes string, screenshot []byte, screenshotMIME string) error {
	notes = strings.TrimSpace(notes)
	if !validManualRechargeRecipient(recipient) ||
		amount <= 0 || math.IsNaN(amount) || math.IsInf(amount, 0) || notes == "" {
		return infraerrors.BadRequest("INVALID_MANUAL_RECHARGE_DEDUCTION", "扣费信息无效，请检查金额、收款人和备注")
	}
	if len(screenshot) > 5<<20 || len(screenshot) > 0 && screenshotMIME != "image/png" && screenshotMIME != "image/jpeg" && screenshotMIME != "image/webp" {
		return infraerrors.BadRequest("INVALID_DEDUCTION_SCREENSHOT", "截图仅支持 5 MB 以内的 PNG、JPEG 或 WebP")
	}
	if s.entClient == nil {
		return errors.New("manual recharge deduction requires database access")
	}
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	txCtx := dbent.NewTxContext(ctx, tx)
	result, err := tx.Client().ExecContext(txCtx, `UPDATE manual_recharge_accounts SET balance = balance - $2, updated_at = NOW()
		WHERE recipient = $1 AND balance >= $2`, recipient, amount)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed == 0 {
		return infraerrors.Conflict("MANUAL_RECHARGE_INSUFFICIENT_BALANCE", "收款人账面余额不足")
	}
	_, err = tx.Client().ExecContext(txCtx, `INSERT INTO manual_recharge_deductions (recipient, amount, notes, screenshot, screenshot_mime)
		VALUES ($1, $2, $3, $4, $5)`, recipient, amount, notes, screenshot, screenshotMIME)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *adminServiceImpl) GetManualRechargeScreenshot(ctx context.Context, id int64) ([]byte, string, error) {
	if s.entClient == nil {
		return nil, "", errors.New("manual recharge screenshot requires database access")
	}
	rows, err := s.entClient.QueryContext(ctx, `SELECT screenshot, screenshot_mime FROM manual_recharge_deductions WHERE id = $1 AND screenshot IS NOT NULL`, id)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, "", infraerrors.NotFound("DEDUCTION_SCREENSHOT_NOT_FOUND", "截图不存在")
	}
	var data []byte
	var mime string
	if err := rows.Scan(&data, &mime); err != nil {
		return nil, "", err
	}
	return data, mime, rows.Err()
}

func (s *adminServiceImpl) GetManualRechargeStats(ctx context.Context, filter ManualRechargeFilter) (*ManualRechargeStats, error) {
	if s.entClient == nil {
		return nil, errors.New("manual recharge statistics require database access")
	}
	if filter.Page < 1 {
		filter.Page = 1
	}
	if filter.DeductionPage < 1 {
		filter.DeductionPage = 1
	}
	if filter.PageSize < 1 || filter.PageSize > 100 {
		filter.PageSize = 20
	}
	if filter.Platform != "" && !validManualRechargePlatform(filter.Platform) ||
		filter.Recipient != "" && !validManualRechargeRecipient(filter.Recipient) {
		return nil, infraerrors.BadRequest("INVALID_MANUAL_RECHARGE_FILTER", "无效的统计筛选条件")
	}
	const where = ` FROM manual_recharge_records r WHERE
		($1::timestamptz IS NULL OR r.created_at >= $1) AND
		($2::timestamptz IS NULL OR r.created_at < $2) AND
		($3::text = '' OR r.platform = $3) AND
		($4::text = '' OR r.recipient = $4) AND
		($5::text = '' OR r.user_email ILIKE '%' || $5 || '%' OR r.user_id::text = $5)`
	args := []any{filter.From, filter.To, filter.Platform, filter.Recipient, strings.TrimSpace(filter.Search)}
	result := &ManualRechargeStats{Items: []ManualRechargeRecord{}, Breakdown: []ManualRechargeBreakdown{}, Accounts: []ManualRechargeAccount{}, Deductions: []ManualRechargeDeduction{}, Page: filter.Page, PageSize: filter.PageSize, DeductionPage: filter.DeductionPage}
	accountRows, err := s.entClient.QueryContext(ctx, `SELECT recipient, balance FROM manual_recharge_accounts ORDER BY recipient`)
	if err != nil {
		return nil, err
	}
	for accountRows.Next() {
		var account ManualRechargeAccount
		if err := accountRows.Scan(&account.Recipient, &account.Balance); err != nil {
			_ = accountRows.Close()
			return nil, err
		}
		result.Accounts = append(result.Accounts, account)
	}
	err = accountRows.Err()
	closeErr := accountRows.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	deductionRows, err := s.entClient.QueryContext(ctx, `SELECT id, recipient, amount, notes, created_at, screenshot IS NOT NULL
		FROM manual_recharge_deductions WHERE ($1::timestamptz IS NULL OR created_at >= $1)
		AND ($2::timestamptz IS NULL OR created_at < $2)
		AND ($3::text = '' OR recipient = $3) ORDER BY created_at DESC, id DESC LIMIT $4 OFFSET $5`,
		filter.From, filter.To, filter.Recipient, filter.PageSize, (filter.DeductionPage-1)*filter.PageSize)
	if err != nil {
		return nil, err
	}
	for deductionRows.Next() {
		var item ManualRechargeDeduction
		if err := deductionRows.Scan(&item.ID, &item.Recipient, &item.Amount, &item.Notes, &item.CreatedAt, &item.HasScreenshot); err != nil {
			_ = deductionRows.Close()
			return nil, err
		}
		result.Deductions = append(result.Deductions, item)
	}
	err = deductionRows.Err()
	closeErr = deductionRows.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	deductionSumRows, err := s.entClient.QueryContext(ctx, `SELECT COUNT(*), COALESCE(SUM(amount), 0) FROM manual_recharge_deductions
		WHERE ($1::timestamptz IS NULL OR created_at >= $1) AND ($2::timestamptz IS NULL OR created_at < $2)
		AND ($3::text = '' OR recipient = $3)`, filter.From, filter.To, filter.Recipient)
	if err != nil {
		return nil, err
	}
	if !deductionSumRows.Next() {
		err = deductionSumRows.Err()
		_ = deductionSumRows.Close()
		return nil, err
	}
	err = deductionSumRows.Scan(&result.DeductionTotal, &result.Deducted)
	closeErr = deductionSumRows.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	rows, err := s.entClient.QueryContext(ctx, `SELECT COUNT(*), COALESCE(SUM(r.amount), 0)`+where, args...)
	if err != nil {
		return nil, err
	}
	if !rows.Next() {
		err = rows.Err()
		_ = rows.Close()
		return nil, err
	}
	err = rows.Scan(&result.Total, &result.Amount)
	closeErr = rows.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	rows, err = s.entClient.QueryContext(ctx, `SELECT r.platform, r.recipient, COUNT(*), COALESCE(SUM(r.amount), 0)`+where+` GROUP BY r.platform, r.recipient ORDER BY r.platform, r.recipient`, args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var item ManualRechargeBreakdown
		if err := rows.Scan(&item.Platform, &item.Recipient, &item.Count, &item.Amount); err != nil {
			_ = rows.Close()
			return nil, err
		}
		result.Breakdown = append(result.Breakdown, item)
	}
	err = rows.Err()
	closeErr = rows.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	listArgs := append(args, filter.PageSize, (filter.Page-1)*filter.PageSize)
	rows, err = s.entClient.QueryContext(ctx, `SELECT r.id, r.user_id, r.user_email, r.amount, r.platform, r.recipient, r.notes, r.created_at`+where+` ORDER BY r.created_at DESC, r.id DESC LIMIT $6 OFFSET $7`, listArgs...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var item ManualRechargeRecord
		if err := rows.Scan(&item.ID, &item.UserID, &item.UserEmail, &item.Amount, &item.Platform, &item.Recipient, &item.Notes, &item.CreatedAt); err != nil {
			_ = rows.Close()
			return nil, err
		}
		result.Items = append(result.Items, item)
	}
	err = rows.Err()
	closeErr = rows.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	return result, nil
}

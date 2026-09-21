package handler

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// This file is the handler-side integration boundary for account latency
// monitoring. Gateway handlers only start and complete an attempt here; timing
// policy and account-switch behavior stay in the monitor implementation.
func startAccountLatencyRequestWatch(
	monitor *service.AccountLatencyMonitor,
	ctx context.Context,
	groupID *int64,
	account *service.Account,
) (context.Context, *service.AccountLatencyRequestWatch) {
	if monitor == nil || groupID == nil || account == nil {
		return ctx, nil
	}
	return monitor.StartRequestWatch(ctx, *groupID, account)
}

func completeAccountLatencyRequestWatch(
	watch *service.AccountLatencyRequestWatch,
	firstTokenMs *int,
	success bool,
	currentErr error,
	excludedIDs map[int64]struct{},
) (error, bool, bool) {
	if watch == nil {
		return currentErr, false, false
	}
	failoverErr := watch.Complete(firstTokenMs, success)
	handled := watch.Handled()
	if failoverErr == nil {
		return currentErr, handled, false
	}
	for accountID := range watch.RetryExcludedIDs() {
		excludedIDs[accountID] = struct{}{}
	}
	return failoverErr, true, true
}

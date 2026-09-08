package service

import (
	"context"
	"time"
)

const ChannelMonitorRequestLedgerTTL = 10 * time.Minute

// ChannelMonitorTransportState is the gateway-visible terminal state of one
// account attempt. Unknown is intentionally not treated as success.
type ChannelMonitorTransportState string

const (
	ChannelMonitorTransportUnknown   ChannelMonitorTransportState = "transport_unknown"
	ChannelMonitorTransportFailed    ChannelMonitorTransportState = "transport_failed"
	ChannelMonitorTransportSucceeded ChannelMonitorTransportState = "transport_succeeded"
)

// ChannelMonitorLedgerAttempt is one actual target account attempt recorded by
// a signed monitor request. Generation is copied from the account cooldown
// state observed before the attempt started.
type ChannelMonitorLedgerAttempt struct {
	AccountID       int64                        `json:"account_id"`
	Generation      int64                        `json:"generation"`
	TransportState  ChannelMonitorTransportState `json:"transport_state"`
	Terminal        bool                         `json:"terminal"`
	DurationSeconds int                          `json:"duration_seconds"`
}

// ChannelMonitorRequestLedger is the shared request-level audit record. The
// key includes monitor ID, request ID and model so primary and extra models can
// never complete one another's records.
type ChannelMonitorRequestLedger struct {
	MonitorID   int64                         `json:"monitor_id"`
	RequestID   string                        `json:"request_id"`
	Attempts    []ChannelMonitorLedgerAttempt `json:"attempts"`
	Completed   bool                          `json:"completed"`
	FinalStatus string                        `json:"final_status,omitempty"`
}

// ChannelMonitorFinalAttribution is returned after the Redis transaction that
// applies all account cooldown changes and locks the request as completed.
type ChannelMonitorFinalAttribution struct {
	Ledger  ChannelMonitorRequestLedger
	Claimed bool
}

// ChannelMonitorRequestLedgerStore is deliberately independent from the
// account cooldown store. Implementations must use shared durable state; a
// process-local fallback would lose cross-instance attribution.
type ChannelMonitorRequestLedgerStore interface {
	RegisterAttempt(context.Context, int64, string, int64, int64, time.Duration) (ChannelMonitorLedgerAttempt, error)
	RecordTransport(context.Context, int64, string, int64, ChannelMonitorTransportState, int) error
	Read(context.Context, int64, string) (ChannelMonitorRequestLedger, error)
	Finalize(context.Context, int64, string, string, time.Time, []int) (ChannelMonitorFinalAttribution, error)
}

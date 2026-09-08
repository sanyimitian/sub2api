package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type attributionLedgerStub struct {
	mu            sync.Mutex
	ledger        ChannelMonitorRequestLedger
	claimed       bool
	registerErr   error
	recordErr     error
	finalizeErr   error
	finalizeCalls int
	finalStatus   string
	requestIDs    []string
}

func (s *attributionLedgerStub) RegisterAttempt(_ context.Context, monitorID int64, requestID string, accountID, generation int64, _ time.Duration) (ChannelMonitorLedgerAttempt, error) {
	if s.registerErr != nil {
		return ChannelMonitorLedgerAttempt{}, s.registerErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ledger.MonitorID = monitorID
	s.ledger.RequestID = requestID
	for _, attempt := range s.ledger.Attempts {
		if attempt.AccountID == accountID {
			return attempt, nil
		}
	}
	attempt := ChannelMonitorLedgerAttempt{AccountID: accountID, Generation: generation, TransportState: ChannelMonitorTransportUnknown}
	s.ledger.Attempts = append(s.ledger.Attempts, attempt)
	return attempt, nil
}

func (s *attributionLedgerStub) RecordTransport(_ context.Context, _ int64, _ string, accountID int64, state ChannelMonitorTransportState, durationSeconds int) error {
	if s.recordErr != nil {
		return s.recordErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.ledger.Attempts {
		if s.ledger.Attempts[i].AccountID == accountID {
			if s.ledger.Attempts[i].TransportState != ChannelMonitorTransportFailed {
				s.ledger.Attempts[i].TransportState = state
			}
			s.ledger.Attempts[i].Terminal = true
			s.ledger.Attempts[i].DurationSeconds = durationSeconds
			return nil
		}
	}
	return errors.New("attempt not found")
}

func (s *attributionLedgerStub) Read(_ context.Context, _ int64, _ string) (ChannelMonitorRequestLedger, error) {
	return ChannelMonitorRequestLedger{}, errors.New("unused")
}

func (s *attributionLedgerStub) Finalize(_ context.Context, monitorID int64, requestID, status string, _ time.Time, _ []int) (ChannelMonitorFinalAttribution, error) {
	if s.finalizeErr != nil {
		return ChannelMonitorFinalAttribution{}, s.finalizeErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.finalizeCalls++
	s.finalStatus = status
	s.requestIDs = append(s.requestIDs, requestID)
	if s.claimed {
		return ChannelMonitorFinalAttribution{Ledger: s.ledger}, nil
	}
	for _, attempt := range s.ledger.Attempts {
		if !attempt.Terminal {
			return ChannelMonitorFinalAttribution{}, errors.New("ledger incomplete")
		}
	}
	s.claimed = true
	s.ledger.MonitorID = monitorID
	s.ledger.RequestID = requestID
	s.ledger.Completed = true
	s.ledger.FinalStatus = status
	return ChannelMonitorFinalAttribution{Ledger: s.ledger, Claimed: true}, nil
}

func TestChannelMonitorObserverDefersSuccessAndFailureUntilFinalAttribution(t *testing.T) {
	cooldown := NewMemoryChannelMonitorCooldownStore()
	ledger := &attributionLedgerStub{}
	observer := NewChannelMonitorProbeObserver(cooldown, nil, nil)
	observer.SetRequestLedger(ledger)
	ctx := WithChannelMonitorProbe(context.Background(), ChannelMonitorProbe{MonitorID: 4, RequestID: "req"})

	attempt := observer.Begin(ctx, &Account{ID: 41, Type: AccountTypeAPIKey}, time.Now())
	require.NotNil(t, attempt)
	observer.Finish(ctx, attempt, ChannelMonitorProbeOutcome{Duration: 3 * time.Second})
	current, err := cooldown.Current(ctx, 41)
	require.NoError(t, err)
	require.Zero(t, current.AccountID, "网关成功不得提前修改冷却")
	require.Equal(t, ChannelMonitorTransportSucceeded, ledger.ledger.Attempts[0].TransportState)
	require.True(t, ledger.ledger.Attempts[0].Terminal)
}

func TestChannelMonitorObserverFinalFailureAttributesEveryAttemptOnce(t *testing.T) {
	ledger := &attributionLedgerStub{ledger: ChannelMonitorRequestLedger{Attempts: []ChannelMonitorLedgerAttempt{
		{AccountID: 51, TransportState: ChannelMonitorTransportFailed, Terminal: true},
		{AccountID: 52, TransportState: ChannelMonitorTransportSucceeded, Terminal: true},
	}}}
	observer := NewChannelMonitorProbeObserver(NewMemoryChannelMonitorCooldownStore(), nil, nil)
	observer.SetRequestLedger(ledger)

	observer.Finalize(context.Background(), 5, "req", MonitorStatusFailed)
	observer.Finalize(context.Background(), 5, "req", MonitorStatusFailed)
	require.Equal(t, 2, ledger.finalizeCalls)
	require.Equal(t, MonitorStatusFailed, ledger.finalStatus)
	require.True(t, ledger.claimed)
}

func TestChannelMonitorObserverNonFailurePreservesFailedAndClearsSuccessful(t *testing.T) {
	ledger := &attributionLedgerStub{ledger: ChannelMonitorRequestLedger{Attempts: []ChannelMonitorLedgerAttempt{
		{AccountID: 61, TransportState: ChannelMonitorTransportFailed, Terminal: true},
		{AccountID: 62, Generation: 1, TransportState: ChannelMonitorTransportSucceeded, Terminal: true},
		{AccountID: 63, TransportState: ChannelMonitorTransportUnknown, Terminal: true},
	}}}
	observer := NewChannelMonitorProbeObserver(NewMemoryChannelMonitorCooldownStore(), nil, nil)
	observer.SetRequestLedger(ledger)

	observer.Finalize(context.Background(), 6, "req", MonitorStatusOperational)
	require.Equal(t, 1, ledger.finalizeCalls)
	require.Equal(t, MonitorStatusOperational, ledger.finalStatus)
}

func TestChannelMonitorServiceFinalizesIndependentModelRequestIDs(t *testing.T) {
	originalClient := monitorHTTPClient
	originalPingClient := monitorPingHTTPClient
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body := `{"content":[{"type":"text","text":"wrong challenge"}]}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    req,
		}, nil
	})
	monitorHTTPClient = &http.Client{Transport: transport}
	monitorPingHTTPClient = &http.Client{Transport: transport}
	t.Cleanup(func() {
		monitorHTTPClient = originalClient
		monitorPingHTTPClient = originalPingClient
	})

	ledger := &attributionLedgerStub{}
	observer := NewChannelMonitorProbeObserver(NewMemoryChannelMonitorCooldownStore(), nil, nil)
	observer.SetRequestLedger(ledger)
	svc := NewChannelMonitorService(nil, nil)
	svc.SetProbeSigner(NewChannelMonitorProbeSigner("secret"))
	svc.SetProbeObserver(observer)
	results := svc.runChecksConcurrent(context.Background(), &ChannelMonitor{
		ID:                91,
		Provider:          MonitorProviderAnthropic,
		Endpoint:          "https://monitor.test",
		APIKey:            "secret-key",
		PrimaryModel:      "primary",
		ExtraModels:       []string{"extra"},
		UseCurrentService: true,
	})

	require.Len(t, results, 2)
	require.Equal(t, 2, ledger.finalizeCalls)
	require.Len(t, ledger.requestIDs, 2)
	require.NotEqual(t, ledger.requestIDs[0], ledger.requestIDs[1])
	require.NotEmpty(t, ledger.requestIDs[0])
	require.NotEmpty(t, ledger.requestIDs[1])
}

func TestChannelMonitorObserverRedisUnavailableDisablesRequestWithoutCooldownFallback(t *testing.T) {
	base := NewMemoryChannelMonitorCooldownStore()
	observer := NewChannelMonitorProbeObserver(base, nil, nil)
	observer.SetRequestLedger(&attributionLedgerStub{registerErr: errors.New("redis unavailable")})
	ctx := WithChannelMonitorProbe(context.Background(), ChannelMonitorProbe{MonitorID: 71, RequestID: "redis-down"})
	attempt := observer.Begin(ctx, &Account{ID: 711, Type: AccountTypeAPIKey}, time.Now())
	require.Nil(t, attempt)
	observer.Finalize(ctx, 71, "redis-down", MonitorStatusFailed)
	cooling, err := base.IsCooling(context.Background(), 711, time.Now())
	require.NoError(t, err)
	require.False(t, cooling)
}

func TestChannelMonitorObserverFinalAttributionRedisFailureDoesNotChangeCheckResult(t *testing.T) {
	base := NewMemoryChannelMonitorCooldownStore()
	ledger := &attributionLedgerStub{finalizeErr: errors.New("redis unavailable"), ledger: ChannelMonitorRequestLedger{Attempts: []ChannelMonitorLedgerAttempt{{AccountID: 721, TransportState: ChannelMonitorTransportFailed, Terminal: true}}}}
	observer := NewChannelMonitorProbeObserver(base, nil, nil)
	observer.SetRequestLedger(ledger)
	observer.Finalize(context.Background(), 72, "final-down", MonitorStatusError)
	cooling, err := base.IsCooling(context.Background(), 721, time.Now())
	require.NoError(t, err)
	require.False(t, cooling)
	require.Zero(t, ledger.finalizeCalls)
}

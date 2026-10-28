package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/stretchr/testify/require"
)

func TestDownstreamWorkingDaysWithCalendar(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	require.NoError(t, err)
	friday, err := time.ParseInLocation("2006-01-02", "2026-10-16", loc)
	require.NoError(t, err)
	load := func(_ context.Context, year int) (map[string]bool, error) {
		require.Equal(t, 2026, year)
		return map[string]bool{"2026-10-10": false, "2026-10-13": true}, nil
	}
	days, err := downstreamWorkingDaysWithCalendar(context.Background(), friday, load)
	require.NoError(t, err)
	var dates []string
	for _, day := range days {
		dates = append(dates, day.Format("2006-01-02"))
	}
	require.Equal(t, []string{"2026-10-10", "2026-10-12", "2026-10-14", "2026-10-15", "2026-10-16"}, dates)
	_, err = downstreamWorkingDaysWithCalendar(context.Background(), friday, func(context.Context, int) (map[string]bool, error) {
		return nil, errors.New("calendar unavailable")
	})
	require.ErrorContains(t, err, "calendar unavailable")
}

func TestDownstreamWorkingDaysNoEligibleDay(t *testing.T) {
	friday, err := time.Parse("2006-01-02", "2026-10-16")
	require.NoError(t, err)
	days, err := downstreamWorkingDaysWithCalendar(context.Background(), friday, func(context.Context, int) (map[string]bool, error) {
		return map[string]bool{
			"2026-10-10": true, "2026-10-12": true, "2026-10-13": true,
			"2026-10-14": true, "2026-10-15": true, "2026-10-16": true,
		}, nil
	})
	require.NoError(t, err)
	require.Empty(t, days)
}

func TestDownstreamIsWorkingDay(t *testing.T) {
	friday, err := time.Parse("2006-01-02", "2026-10-23")
	require.NoError(t, err)
	calendar := map[string]bool{
		"2026-10-23": true,
		"2026-10-24": false,
	}
	require.False(t, downstreamIsWorkingDay(friday, calendar))
	require.True(t, downstreamIsWorkingDay(friday.AddDate(0, 0, 1), calendar))
	require.False(t, downstreamIsWorkingDay(friday.AddDate(0, 0, 2), calendar))
	require.True(t, downstreamIsWorkingDay(friday.AddDate(0, 0, 3), calendar))
}

func TestDownstreamDueDate(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	require.NoError(t, err)
	for _, tc := range []struct {
		now  string
		want string
	}{
		{"2026-10-16 23:54:59", "2026-10-09"},
		{"2026-10-16 23:55:00", "2026-10-16"},
		{"2026-10-17 00:00:00", "2026-10-16"},
		{"2026-10-21 12:00:00", "2026-10-16"},
		{"2026-10-23 23:54:59", "2026-10-16"},
		{"2027-01-04 09:00:00", "2027-01-01"},
	} {
		now, err := time.ParseInLocation("2006-01-02 15:04:05", tc.now, loc)
		require.NoError(t, err)
		require.Equal(t, tc.want, downstreamDueDate(now, loc, DownstreamAutoRateWeekly).Format("2006-01-02"), tc.now)
	}
	before, err := time.ParseInLocation("2006-01-02 15:04:05", "2026-10-10 23:54:59", loc)
	require.NoError(t, err)
	require.Equal(t, "2026-10-09", downstreamDueDate(before, loc, DownstreamAutoRateDaily).Format("2006-01-02"))
	require.Equal(t, "2026-10-10", downstreamDueDate(before.Add(time.Second), loc, DownstreamAutoRateDaily).Format("2006-01-02"))
}

type downstreamRetryRepo struct {
	UsageLogRepository
	config  DownstreamAutoRateConfig
	applied time.Time
	days    []time.Time
}

func (r *downstreamRetryRepo) GetDownstreamAutoRateConfig(context.Context) (*DownstreamAutoRateConfig, error) {
	return &r.config, nil
}

func (r *downstreamRetryRepo) SaveDownstreamAutoRateConfig(context.Context, DownstreamAutoRateConfig) error {
	return nil
}

func (r *downstreamRetryRepo) ApplyDownstreamAutoRates(_ context.Context, sunday time.Time, days []time.Time, _ DownstreamAutoRateConfig) (bool, error) {
	r.applied = sunday
	r.days = days
	date := sunday.Format("2006-01-02")
	r.config.LastRunDate = &date
	return true, nil
}

type downstreamRoundTripper func(*http.Request) (*http.Response, error)

func (fn downstreamRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestRunDownstreamAutoRatesRetriesMissedWeek(t *testing.T) {
	now := time.Date(2026, time.October, 17, 0, 0, 0, 0, timezone.Location())
	repo := &downstreamRetryRepo{config: DownstreamAutoRateConfig{Enabled: true, Mode: DownstreamAutoRateWeekly}}
	svc := &UsageService{usageRepo: repo}
	originalClient := downstreamHolidayClient
	defer func() { downstreamHolidayClient = originalClient }()
	requests := 0
	downstreamHolidayClient = &http.Client{Transport: downstreamRoundTripper(func(*http.Request) (*http.Response, error) {
		requests++
		if requests == 1 {
			return nil, errors.New("calendar temporarily unavailable")
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"year":2026,"days":[{"date":"2026-10-10","isOffDay":false}]}`)),
		}, nil
	})}

	applied, err := svc.RunDownstreamAutoRates(context.Background(), now)
	require.ErrorContains(t, err, "calendar temporarily unavailable")
	require.False(t, applied)
	applied, err = svc.RunDownstreamAutoRates(context.Background(), now.Add(time.Hour))
	require.NoError(t, err)
	require.True(t, applied)
	require.Equal(t, "2026-10-16", repo.applied.Format("2006-01-02"))
	require.Len(t, repo.days, 6)
	applied, err = svc.RunDownstreamAutoRates(context.Background(), now.Add(2*time.Hour))
	require.NoError(t, err)
	require.False(t, applied)
	require.Equal(t, 2, requests)
}

func TestRunDownstreamAutoRatesDailyKeepsRateOnDaysOff(t *testing.T) {
	loc := timezone.Location()
	repo := &downstreamRetryRepo{config: DownstreamAutoRateConfig{Enabled: true, Mode: DownstreamAutoRateDaily}}
	svc := &UsageService{usageRepo: repo}
	originalClient := downstreamHolidayClient
	defer func() { downstreamHolidayClient = originalClient }()
	downstreamHolidayClient = &http.Client{Transport: downstreamRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"year":2026,"days":[{"date":"2026-10-09","isOffDay":true},{"date":"2026-10-10","isOffDay":false}]}`))}, nil
	})}
	for _, tc := range []struct {
		now  time.Time
		want string
	}{
		{time.Date(2026, 10, 10, 23, 55, 0, 0, loc), "2026-10-10"},
		{time.Date(2026, 10, 11, 23, 55, 0, 0, loc), "2026-10-10"},
		{time.Date(2026, 10, 12, 0, 0, 0, 0, loc), "2026-10-10"},
		{time.Date(2026, 10, 12, 23, 55, 0, 0, loc), "2026-10-12"},
	} {
		applied, err := svc.RunDownstreamAutoRates(context.Background(), tc.now)
		require.NoError(t, err)
		require.Equal(t, tc.want, *repo.config.LastRunDate)
		require.Equal(t, tc.want == tc.now.Format("2006-01-02"), applied)
		require.Len(t, repo.days, 1)
	}
}

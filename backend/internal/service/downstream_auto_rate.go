package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/robfig/cron/v3"
)

type DownstreamAutoRateTier struct {
	MinTokens      int64   `json:"min_tokens"`
	RateMultiplier float64 `json:"rate_multiplier"`
}

type DownstreamAutoRateConfig struct {
	Enabled     bool                     `json:"enabled"`
	Mode        string                   `json:"mode"`
	GroupIDs    []int64                  `json:"group_ids"`
	Tiers       []DownstreamAutoRateTier `json:"tiers"`
	LastRunDate *string                  `json:"last_run_date,omitempty"`
}

const (
	DownstreamAutoRateDaily  = "daily"
	DownstreamAutoRateWeekly = "weekly"
)

type DownstreamAutoRateRepository interface {
	GetDownstreamAutoRateConfig(context.Context) (*DownstreamAutoRateConfig, error)
	SaveDownstreamAutoRateConfig(context.Context, DownstreamAutoRateConfig) error
	ApplyDownstreamAutoRates(context.Context, time.Time, []time.Time, DownstreamAutoRateConfig) (bool, error)
}

func (s *UsageService) autoRateRepo() (DownstreamAutoRateRepository, error) {
	repo, ok := s.usageRepo.(DownstreamAutoRateRepository)
	if !ok {
		return nil, fmt.Errorf("downstream auto rate repository is unavailable")
	}
	return repo, nil
}

func (s *UsageService) GetDownstreamAutoRateConfig(ctx context.Context) (*DownstreamAutoRateConfig, error) {
	repo, err := s.autoRateRepo()
	if err != nil {
		return nil, err
	}
	return repo.GetDownstreamAutoRateConfig(ctx)
}

func (s *UsageService) SaveDownstreamAutoRateConfig(ctx context.Context, cfg DownstreamAutoRateConfig) error {
	if cfg.Mode != DownstreamAutoRateDaily && cfg.Mode != DownstreamAutoRateWeekly {
		return fmt.Errorf("automatic rate mode must be daily or weekly")
	}
	if cfg.Enabled && len(cfg.GroupIDs) == 0 {
		return fmt.Errorf("select at least one group")
	}
	if len(cfg.Tiers) == 0 || len(cfg.Tiers) > 20 {
		return fmt.Errorf("provide 1 to 20 tiers")
	}
	seen := make(map[int64]bool, len(cfg.GroupIDs))
	for _, id := range cfg.GroupIDs {
		if id <= 0 || seen[id] {
			return fmt.Errorf("group IDs must be positive and unique")
		}
		seen[id] = true
	}
	sort.Slice(cfg.Tiers, func(i, j int) bool { return cfg.Tiers[i].MinTokens < cfg.Tiers[j].MinTokens })
	for i, tier := range cfg.Tiers {
		if tier.MinTokens <= 0 || (i > 0 && cfg.Tiers[i-1].MinTokens == tier.MinTokens) {
			return fmt.Errorf("tier thresholds must be positive and unique")
		}
		if math.IsNaN(tier.RateMultiplier) || math.IsInf(tier.RateMultiplier, 0) || tier.RateMultiplier <= 0 || tier.RateMultiplier > 100 || math.Abs(math.Round(tier.RateMultiplier*10000)-tier.RateMultiplier*10000) > 1e-8 {
			return fmt.Errorf("tier rates must be positive with at most four decimal places")
		}
	}
	repo, err := s.autoRateRepo()
	if err != nil {
		return err
	}
	return repo.SaveDownstreamAutoRateConfig(ctx, cfg)
}

type holidayCalendar struct {
	Year int `json:"year"`
	Days []struct {
		Date     string `json:"date"`
		IsOffDay bool   `json:"isOffDay"`
	} `json:"days"`
}

var downstreamHolidayClient = &http.Client{Timeout: 10 * time.Second}

func fetchDownstreamHolidays(ctx context.Context, year int) (map[string]bool, error) {
	url := fmt.Sprintf("https://raw.githubusercontent.com/NateScarlet/holiday-cn/master/%d.json", year)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := downstreamHolidayClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("holiday calendar %d returned HTTP %d", year, resp.StatusCode)
	}
	var calendar holidayCalendar
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&calendar); err != nil {
		return nil, err
	}
	if calendar.Year != year || len(calendar.Days) == 0 {
		return nil, fmt.Errorf("holiday calendar %d is incomplete", year)
	}
	off := make(map[string]bool, len(calendar.Days))
	for _, day := range calendar.Days {
		off[day.Date] = day.IsOffDay
	}
	return off, nil
}

func downstreamCalendarLoader() func(context.Context, int) (map[string]bool, error) {
	calendars := make(map[int]map[string]bool)
	return func(ctx context.Context, year int) (map[string]bool, error) {
		if days, ok := calendars[year]; ok {
			return days, nil
		}
		days, err := fetchDownstreamHolidays(ctx, year)
		if err == nil {
			calendars[year] = days
		}
		return days, err
	}
}

func downstreamWorkingDays(ctx context.Context, end time.Time) ([]time.Time, error) {
	return downstreamWorkingDaysWithCalendar(ctx, end, downstreamCalendarLoader())
}

func downstreamWorkingDaysWithCalendar(ctx context.Context, end time.Time, load func(context.Context, int) (map[string]bool, error)) ([]time.Time, error) {
	workingDays := make([]time.Time, 0, 7)
	for i := 6; i >= 0; i-- {
		day := end.AddDate(0, 0, -i)
		calendar, err := load(ctx, day.Year())
		if err != nil {
			return nil, err
		}
		if downstreamIsWorkingDay(day, calendar) {
			workingDays = append(workingDays, day)
		}
	}
	return workingDays, nil
}

func downstreamIsWorkingDay(day time.Time, calendar map[string]bool) bool {
	if isOffDay, listed := calendar[day.Format("2006-01-02")]; listed {
		return !isOffDay
	}
	return day.Weekday() != time.Saturday && day.Weekday() != time.Sunday
}

func downstreamDueDate(now time.Time, loc *time.Location, mode string) time.Time {
	today := now.In(loc)
	day := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, loc)
	if mode == DownstreamAutoRateWeekly {
		daysSinceFriday := (int(today.Weekday()) - int(time.Friday) + 7) % 7
		day = day.AddDate(0, 0, -daysSinceFriday)
	}
	runAt := time.Date(day.Year(), day.Month(), day.Day(), 23, 55, 0, 0, loc)
	if now.Before(runAt) {
		if mode == DownstreamAutoRateWeekly {
			return day.AddDate(0, 0, -7)
		}
		return day.AddDate(0, 0, -1)
	}
	return day
}

func downstreamLatestWorkingDay(ctx context.Context, day time.Time, load func(context.Context, int) (map[string]bool, error)) (time.Time, error) {
	for i := 0; i < 31; i++ {
		candidate := day.AddDate(0, 0, -i)
		calendar, err := load(ctx, candidate.Year())
		if err != nil {
			return time.Time{}, err
		}
		if downstreamIsWorkingDay(candidate, calendar) {
			return candidate, nil
		}
	}
	return time.Time{}, fmt.Errorf("no working day found in the past 31 days")
}

func (s *UsageService) RunDownstreamAutoRates(ctx context.Context, now time.Time) (bool, error) {
	repo, err := s.autoRateRepo()
	if err != nil {
		return false, err
	}
	cfg, err := repo.GetDownstreamAutoRateConfig(ctx)
	if err != nil || !cfg.Enabled {
		return false, err
	}
	if cfg.Mode != DownstreamAutoRateDaily && cfg.Mode != DownstreamAutoRateWeekly {
		return false, fmt.Errorf("invalid automatic rate mode %q", cfg.Mode)
	}
	date := downstreamDueDate(now, timezone.Location(), cfg.Mode)
	if cfg.Mode == DownstreamAutoRateDaily {
		date, err = downstreamLatestWorkingDay(ctx, date, downstreamCalendarLoader())
		if err != nil {
			return false, err
		}
	}
	if cfg.LastRunDate != nil && *cfg.LastRunDate == date.Format("2006-01-02") {
		return false, nil
	}
	workingDays := []time.Time{date}
	if cfg.Mode == DownstreamAutoRateWeekly {
		workingDays, err = downstreamWorkingDays(ctx, date)
		if err != nil {
			return false, err
		}
	}
	if len(workingDays) == 0 {
		return false, nil
	}
	return repo.ApplyDownstreamAutoRates(ctx, date, workingDays, *cfg)
}

func (s *UsageService) StartDownstreamAutoRates() {
	if _, err := s.autoRateRepo(); err != nil {
		return
	}
	s.autoRateMu.Lock()
	defer s.autoRateMu.Unlock()
	if s.autoRateCron != nil {
		return
	}
	c := cron.New(cron.WithLocation(timezone.Location()))
	run := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if _, err := s.RunDownstreamAutoRates(ctx, timezone.Now()); err != nil {
			logger.LegacyPrintf("service.downstream_auto_rate", "automatic adjustment failed: %v", err)
		}
	}
	if _, err := c.AddFunc("55 23 * * *", run); err != nil {
		logger.LegacyPrintf("service.downstream_auto_rate", "schedule failed: %v", err)
		return
	}
	if _, err := c.AddFunc("0 * * * *", run); err != nil {
		logger.LegacyPrintf("service.downstream_auto_rate", "schedule failed: %v", err)
		return
	}
	c.Start()
	s.autoRateCron = c
}

func (s *UsageService) StopDownstreamAutoRates() {
	s.autoRateMu.Lock()
	c := s.autoRateCron
	s.autoRateCron = nil
	s.autoRateMu.Unlock()
	if c != nil {
		<-c.Stop().Done()
	}
}

package repository

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const channelMonitorCooldownKeyPrefix = "channel_monitor:cooldown:account:"
const channelMonitorCooldownRetention = 7 * 24 * time.Hour

var channelMonitorCooldownFailureScript = redis.NewScript(`
local key = KEYS[1]
local now = tonumber(ARGV[1])
local count = tonumber(ARGV[2])
local cur = redis.call('HMGET', key, 'generation', 'streak', 'until_ms')
local generation = tonumber(cur[1]) or 0
local streak = tonumber(cur[2]) or 0
local until_ms = tonumber(cur[3]) or 0
if until_ms > now then
  return {generation, streak, until_ms}
end
streak = streak + 1
if streak < 1 then streak = 1 end
local idx = streak
if idx > count then idx = count end
local duration = tonumber(ARGV[2 + idx]) or 0
local next_until = now + duration
generation = redis.call('INCR', key .. ':generation')
redis.call('HSET', key, 'generation', generation, 'streak', streak, 'until_ms', next_until)
redis.call('PEXPIRE', key, duration + tonumber(ARGV[3 + count]))
redis.call('PEXPIRE', key .. ':generation', duration + tonumber(ARGV[3 + count]))
return {generation, streak, next_until}
`)

var channelMonitorCooldownSuccessScript = redis.NewScript(`
local key = KEYS[1]
local expected = tonumber(ARGV[1]) or 0
local now = tonumber(ARGV[2]) or 0
local cur = redis.call('HMGET', key, 'generation', 'until_ms')
local generation = tonumber(cur[1]) or 0
local until_ms = tonumber(cur[2]) or 0
-- Success must carry the generation observed when the attempt began.
-- Generation zero is intentionally ignored so a late attempt that began
-- before the first failure cannot clear that newly-created event.
if expected <= 0 then return 0 end
if generation == 0 or generation ~= expected then return 0 end
if until_ms > now then
  redis.call('HSET', key, 'streak', 0)
  return 0
end
redis.call('DEL', key)
return 1
`)

type channelMonitorCooldownStore struct{ rdb *redis.Client }

func NewChannelMonitorCooldownStore(rdb *redis.Client) service.ChannelMonitorCooldownStore {
	return &channelMonitorCooldownStore{rdb: rdb}
}

func channelMonitorCooldownKey(accountID int64) string {
	return channelMonitorCooldownKeyPrefix + strconv.FormatInt(accountID, 10)
}

func (s *channelMonitorCooldownStore) ObserveFailure(ctx context.Context, accountID int64, now time.Time, ladder []int) (service.ChannelMonitorCooldownEvent, error) {
	if s == nil || s.rdb == nil {
		return service.ChannelMonitorCooldownEvent{}, fmt.Errorf("channel monitor cooldown redis unavailable")
	}
	if accountID <= 0 || len(ladder) != 5 {
		return service.ChannelMonitorCooldownEvent{}, fmt.Errorf("invalid channel monitor cooldown input")
	}
	args := []any{now.UTC().UnixMilli(), len(ladder)}
	for _, minutes := range ladder {
		if minutes <= 0 {
			return service.ChannelMonitorCooldownEvent{}, fmt.Errorf("invalid cooldown duration")
		}
		args = append(args, int64(minutes)*int64(time.Minute/time.Millisecond))
	}
	args = append(args, int64(channelMonitorCooldownRetention/time.Millisecond))
	values, err := channelMonitorCooldownFailureScript.Run(ctx, s.rdb, []string{channelMonitorCooldownKey(accountID)}, args...).Slice()
	if err != nil {
		return service.ChannelMonitorCooldownEvent{}, err
	}
	if len(values) != 3 {
		return service.ChannelMonitorCooldownEvent{}, fmt.Errorf("unexpected redis cooldown result")
	}
	g, err := redisInt64Value(values[0])
	if err != nil {
		return service.ChannelMonitorCooldownEvent{}, err
	}
	streak, err := redisInt64Value(values[1])
	if err != nil {
		return service.ChannelMonitorCooldownEvent{}, err
	}
	until, err := redisInt64Value(values[2])
	if err != nil {
		return service.ChannelMonitorCooldownEvent{}, err
	}
	return service.ChannelMonitorCooldownEvent{AccountID: accountID, Generation: g, Streak: int(streak), Until: time.UnixMilli(until).UTC()}, nil
}

func (s *channelMonitorCooldownStore) IsCooling(ctx context.Context, accountID int64, now time.Time) (bool, error) {
	if s == nil || s.rdb == nil {
		return false, fmt.Errorf("channel monitor cooldown redis unavailable")
	}
	values, err := s.rdb.HMGet(ctx, channelMonitorCooldownKey(accountID), "until_ms").Result()
	if err != nil {
		return false, err
	}
	if len(values) == 0 || values[0] == nil {
		return false, nil
	}
	until, err := strconv.ParseInt(fmt.Sprint(values[0]), 10, 64)
	if err != nil {
		return false, nil
	}
	return until > now.UTC().UnixMilli(), nil
}

func (s *channelMonitorCooldownStore) Current(ctx context.Context, accountID int64) (service.ChannelMonitorCooldownEvent, error) {
	if s == nil || s.rdb == nil {
		return service.ChannelMonitorCooldownEvent{}, fmt.Errorf("channel monitor cooldown redis unavailable")
	}
	values, err := s.rdb.HMGet(ctx, channelMonitorCooldownKey(accountID), "generation", "streak", "until_ms").Result()
	if err != nil {
		return service.ChannelMonitorCooldownEvent{}, err
	}
	if len(values) < 3 || values[0] == nil {
		return service.ChannelMonitorCooldownEvent{AccountID: accountID}, nil
	}
	g, err := strconv.ParseInt(fmt.Sprint(values[0]), 10, 64)
	if err != nil {
		return service.ChannelMonitorCooldownEvent{}, nil
	}
	streak, _ := strconv.Atoi(fmt.Sprint(values[1]))
	until, _ := strconv.ParseInt(fmt.Sprint(values[2]), 10, 64)
	return service.ChannelMonitorCooldownEvent{AccountID: accountID, Generation: g, Streak: streak, Until: time.UnixMilli(until).UTC()}, nil
}

func (s *channelMonitorCooldownStore) ObserveSuccess(ctx context.Context, accountID, generation int64, now time.Time) error {
	if s == nil || s.rdb == nil {
		return fmt.Errorf("channel monitor cooldown redis unavailable")
	}
	_, err := channelMonitorCooldownSuccessScript.Run(ctx, s.rdb, []string{channelMonitorCooldownKey(accountID)}, generation, now.UTC().UnixMilli()).Result()
	return err
}

func redisInt64Value(value any) (int64, error) {
	switch v := value.(type) {
	case int64:
		return v, nil
	case int:
		return int64(v), nil
	case string:
		return strconv.ParseInt(v, 10, 64)
	case []byte:
		return strconv.ParseInt(string(v), 10, 64)
	default:
		return 0, fmt.Errorf("unexpected redis integer type %T", value)
	}
}

package repository

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

// apiKeyCooldownEventTTL is the retention window after an event expires. The
// hash must outlive the active window so a failure immediately after expiry
// can advance the same streak instead of starting from one again.
const apiKeyCooldownEventTTL = 7 * 24 * time.Hour

var apiKeyCooldownFailureScript = redis.NewScript(`
local key = KEYS[1]
local generation_key = key .. ':generation'
local now_ms = tonumber(ARGV[1])
local requested_until_ms = tonumber(ARGV[2])
local mode = ARGV[3]
local ladder_count = tonumber(ARGV[4])

local current = redis.call('HMGET', key, 'generation', 'streak', 'until_ms', 'persisted')
local generation = tonumber(current[1]) or 0
local streak = tonumber(current[2]) or 0
local until_ms = tonumber(current[3]) or 0
local persisted = tonumber(current[4]) or 0

if until_ms > now_ms then
  -- A later, reliable upstream reset may extend an active event. Never
  -- shorten it and never increment the streak for an in-window merge.
  if requested_until_ms > until_ms then
    until_ms = requested_until_ms
    persisted = 0
    redis.call('HSET', key, 'until_ms', until_ms, 'persisted', 0)
  end
  local desired_ttl = tonumber(ARGV[5 + ladder_count]) or 0
  local retention_ttl = tonumber(ARGV[6 + ladder_count]) or 0
  local remaining_ms = until_ms - now_ms
  local remaining_seconds = math.floor((remaining_ms + 999) / 1000)
  local event_ttl = remaining_seconds + retention_ttl
  if event_ttl > desired_ttl then desired_ttl = event_ttl end
  local current_ttl = redis.call('TTL', key)
  if current_ttl < desired_ttl then
    redis.call('EXPIRE', key, desired_ttl)
    redis.call('EXPIRE', generation_key, desired_ttl)
  end
  return {generation, streak, until_ms, 0, persisted}
end

streak = streak + 1
generation = redis.call('INCR', generation_key)
if generation < 1 then generation = 1 end

local index = streak - 1
if mode == 'cycle' then
  index = index % ladder_count
else
  if index >= ladder_count then index = ladder_count - 1 end
end
local duration_ms = tonumber(ARGV[5 + index])
local computed_until_ms = now_ms + duration_ms
if requested_until_ms > computed_until_ms then computed_until_ms = requested_until_ms end

redis.call('HSET', key, 'generation', generation, 'streak', streak, 'until_ms', computed_until_ms, 'persisted', 0)
local desired_ttl = tonumber(ARGV[5 + ladder_count]) or 0
local retention_ttl = tonumber(ARGV[6 + ladder_count]) or 0
local event_ttl = math.floor((computed_until_ms - now_ms + 999) / 1000) + retention_ttl
if event_ttl > desired_ttl then desired_ttl = event_ttl end
redis.call('EXPIRE', key, desired_ttl)
redis.call('EXPIRE', generation_key, desired_ttl)
return {generation, streak, computed_until_ms, 1, 0}
`)

var apiKeyCooldownCheckScript = redis.NewScript(`
local current = redis.call('HMGET', KEYS[1], 'generation', 'streak', 'until_ms', 'persisted')
local generation = tonumber(current[1]) or 0
local streak = tonumber(current[2]) or 0
local until_ms = tonumber(current[3]) or 0
local persisted = tonumber(current[4]) or 0
if generation > 0 and until_ms > tonumber(ARGV[1]) then
  return {generation, streak, until_ms, 1, persisted}
end
return {generation, streak, until_ms, 0, persisted}
`)

var apiKeyCooldownPersistedScript = redis.NewScript(`
local generation = tonumber(redis.call('HGET', KEYS[1], 'generation')) or 0
if generation == tonumber(ARGV[1]) and generation > 0 then
  redis.call('HSET', KEYS[1], 'persisted', 1)
  return 1
end
return 0
`)

var apiKeyCooldownSuccessScript = redis.NewScript(`
local now_ms = tonumber(ARGV[#ARGV]) or 0
local deleted = 0
for i = 1, #KEYS do
  local key = KEYS[i]
  local expected = tonumber(ARGV[i]) or 0
  local current = redis.call('HMGET', key, 'generation', 'until_ms')
  local generation = tonumber(current[1]) or 0
  local until_ms = tonumber(current[2]) or 0
  if generation == expected and generation > 0 and until_ms <= now_ms then
    redis.call('DEL', key)
    deleted = deleted + 1
  end
end
return deleted
`)

type apiKeyCooldownStore struct {
	rdb *redis.Client
}

func NewAPIKeyCooldownStore(rdb *redis.Client) service.APIKeyCooldownStore {
	return &apiKeyCooldownStore{rdb: rdb}
}

func (s *apiKeyCooldownStore) ObserveFailure(ctx context.Context, key service.APIKeyCooldownKey, policy service.APIKeyCooldownPolicy, now time.Time, upstreamReset *time.Time) (service.APIKeyCooldownEvent, error) {
	if s == nil || s.rdb == nil {
		return service.APIKeyCooldownEvent{}, fmt.Errorf("api key cooldown redis is unavailable")
	}
	if !policy.Enabled || len(policy.Cooldowns) == 0 {
		return service.APIKeyCooldownEvent{Key: key}, nil
	}
	if key.AccountID <= 0 {
		return service.APIKeyCooldownEvent{}, fmt.Errorf("invalid API key cooldown account id")
	}
	now = now.UTC()
	requestedUntil := int64(0)
	if upstreamReset != nil && upstreamReset.After(now) && key.Family == service.APIKeyFailureRateLimit {
		requestedUntil = upstreamReset.UTC().UnixMilli()
	}
	args := []any{now.UnixMilli(), requestedUntil, string(policy.Mode), len(policy.Cooldowns)}
	for _, seconds := range policy.Cooldowns {
		args = append(args, int64(seconds)*1000)
	}
	args = append(args, apiKeyCooldownRedisTTL(policy, now, requestedUntil), int64(apiKeyCooldownEventTTL/time.Second))
	values, err := apiKeyCooldownFailureScript.Run(ctx, s.rdb, []string{key.RedisKey()}, args...).Slice()
	if err != nil {
		return service.APIKeyCooldownEvent{}, fmt.Errorf("observe API key cooldown failure: %w", err)
	}
	if len(values) != 5 {
		return service.APIKeyCooldownEvent{}, fmt.Errorf("observe API key cooldown failure: unexpected result")
	}
	generation, err := redisInt64(values[0])
	if err != nil {
		return service.APIKeyCooldownEvent{}, err
	}
	streak, err := redisInt64(values[1])
	if err != nil {
		return service.APIKeyCooldownEvent{}, err
	}
	untilMS, err := redisInt64(values[2])
	if err != nil {
		return service.APIKeyCooldownEvent{}, err
	}
	created, err := redisInt64(values[3])
	if err != nil {
		return service.APIKeyCooldownEvent{}, err
	}
	persisted, err := redisInt64(values[4])
	if err != nil {
		return service.APIKeyCooldownEvent{}, err
	}
	return service.APIKeyCooldownEvent{Key: key, Generation: generation, Streak: streak, Until: time.UnixMilli(untilMS).UTC(), Created: created == 1, NeedsPersistence: persisted == 0}, nil
}

func (s *apiKeyCooldownStore) Check(ctx context.Context, key service.APIKeyCooldownKey, now time.Time) (service.APIKeyCooldownEvent, bool, error) {
	if s == nil || s.rdb == nil {
		return service.APIKeyCooldownEvent{}, false, fmt.Errorf("api key cooldown redis is unavailable")
	}
	values, err := apiKeyCooldownCheckScript.Run(ctx, s.rdb, []string{key.RedisKey()}, now.UTC().UnixMilli()).Slice()
	if err != nil {
		return service.APIKeyCooldownEvent{}, false, fmt.Errorf("check API key cooldown: %w", err)
	}
	if len(values) != 5 {
		return service.APIKeyCooldownEvent{}, false, fmt.Errorf("check API key cooldown: unexpected result")
	}
	generation, err := redisInt64(values[0])
	if err != nil {
		return service.APIKeyCooldownEvent{}, false, err
	}
	streak, err := redisInt64(values[1])
	if err != nil {
		return service.APIKeyCooldownEvent{}, false, err
	}
	untilMS, err := redisInt64(values[2])
	if err != nil {
		return service.APIKeyCooldownEvent{}, false, err
	}
	active, err := redisInt64(values[3])
	if err != nil {
		return service.APIKeyCooldownEvent{}, false, err
	}
	persisted, err := redisInt64(values[4])
	if err != nil {
		return service.APIKeyCooldownEvent{}, false, err
	}
	return service.APIKeyCooldownEvent{Key: key, Generation: generation, Streak: streak, Until: time.UnixMilli(untilMS).UTC(), NeedsPersistence: generation > 0 && persisted == 0}, active == 1, nil
}

func (s *apiKeyCooldownStore) MarkPersisted(ctx context.Context, key service.APIKeyCooldownKey, generation int64) error {
	if s == nil || s.rdb == nil {
		return fmt.Errorf("api key cooldown redis is unavailable")
	}
	if generation <= 0 {
		return nil
	}
	if _, err := apiKeyCooldownPersistedScript.Run(ctx, s.rdb, []string{key.RedisKey()}, generation).Result(); err != nil {
		return fmt.Errorf("mark API key cooldown persisted: %w", err)
	}
	return nil
}

func (s *apiKeyCooldownStore) ResetSuccess(ctx context.Context, keys []service.APIKeyCooldownKey, token service.APIKeyCooldownAttemptToken, now time.Time) error {
	if s == nil || s.rdb == nil {
		return fmt.Errorf("api key cooldown redis is unavailable")
	}
	if len(keys) == 0 {
		return nil
	}
	redisKeys := make([]string, 0, len(keys))
	args := make([]any, 0, len(keys)+1)
	for _, key := range keys {
		redisKey := key.RedisKey()
		generation, ok := token.Generations[redisKey]
		if !ok {
			continue
		}
		redisKeys = append(redisKeys, redisKey)
		args = append(args, generation)
	}
	if len(redisKeys) == 0 {
		return nil
	}
	args = append(args, now.UTC().UnixMilli())
	_, err := apiKeyCooldownSuccessScript.Run(ctx, s.rdb, redisKeys, args...).Result()
	if err != nil {
		return fmt.Errorf("reset API key cooldown success: %w", err)
	}
	return nil
}

func apiKeyCooldownRedisTTL(policy service.APIKeyCooldownPolicy, now time.Time, requestedUntilMS int64) int64 {
	retentionSeconds := int64(apiKeyCooldownEventTTL / time.Second)
	maxWindowSeconds := int64(0)
	for _, seconds := range policy.Cooldowns {
		if seconds > 0 && int64(seconds) > maxWindowSeconds {
			maxWindowSeconds = int64(seconds)
		}
	}
	if requestedUntilMS > now.UnixMilli() {
		deltaMS := requestedUntilMS - now.UnixMilli()
		requestedSeconds := (deltaMS + 999) / 1000
		if requestedSeconds > maxWindowSeconds {
			maxWindowSeconds = requestedSeconds
		}
	}
	if maxWindowSeconds <= 0 {
		maxWindowSeconds = 1
	}
	// Keep the same seven-day post-expiry retention as the historical fixed
	// TTL, while allowing administrator-configured or upstream windows longer
	// than seven days to remain observable.
	return maxWindowSeconds + retentionSeconds
}

func redisInt64(value any) (int64, error) {
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

var _ service.APIKeyCooldownStore = (*apiKeyCooldownStore)(nil)

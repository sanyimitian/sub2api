package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const channelMonitorRequestLedgerPrefix = "channel_monitor:ledger:"

var channelMonitorLedgerRegisterScript = redis.NewScript(`
local key = KEYS[1]
local account_id = tonumber(ARGV[1])
local generation = tonumber(ARGV[2]) or 0
local ttl_ms = tonumber(ARGV[3])
local attempts_raw = redis.call('HGET', key, 'attempts')
local attempts = {}
if attempts_raw then attempts = cjson.decode(attempts_raw) end
for _, attempt in ipairs(attempts) do
  if tonumber(attempt.account_id) == account_id then
    return cjson.encode(attempt)
  end
end
local attempt = {account_id = account_id, generation = generation, transport_state = 'transport_unknown', terminal = false}
table.insert(attempts, attempt)
redis.call('HSET', key, 'attempts', cjson.encode(attempts))
redis.call('PEXPIRE', key, ttl_ms)
return cjson.encode(attempt)
`)

var channelMonitorLedgerTransportScript = redis.NewScript(`
local key = KEYS[1]
local account_id = tonumber(ARGV[1])
local state = ARGV[2]
local duration_seconds = tonumber(ARGV[3]) or 0
local ttl_ms = tonumber(ARGV[4])
local attempts_raw = redis.call('HGET', key, 'attempts')
if not attempts_raw then return redis.error_reply('channel monitor request ledger not found') end
local attempts = cjson.decode(attempts_raw)
local found = false
for _, attempt in ipairs(attempts) do
  if tonumber(attempt.account_id) == account_id then
    found = true
    local current = attempt.transport_state or 'transport_unknown'
    if attempt.terminal ~= true then
      attempt.transport_state = state
    elseif current == 'transport_failed' or state == 'transport_failed' then
      attempt.transport_state = 'transport_failed'
    elseif current == 'transport_unknown' or state == 'transport_unknown' then
      attempt.transport_state = 'transport_unknown'
    else
      attempt.transport_state = 'transport_succeeded'
    end
    attempt.terminal = true
    local current_duration = tonumber(attempt.duration_seconds) or 0
    if duration_seconds > current_duration then attempt.duration_seconds = duration_seconds end
  end
end
if not found then return redis.error_reply('channel monitor request ledger attempt not found') end
redis.call('HSET', key, 'attempts', cjson.encode(attempts))
redis.call('PEXPIRE', key, ttl_ms)
return 1
`)

var channelMonitorLedgerFinalizeScript = redis.NewScript(`
local ledger_key = KEYS[1]
local final_status = ARGV[1]
local now = tonumber(ARGV[2])
local ladder_count = tonumber(ARGV[3])
local retention_ms = tonumber(ARGV[4 + ladder_count])
local ledger_ttl_ms = tonumber(ARGV[5 + ladder_count])
local attempts_raw = redis.call('HGET', ledger_key, 'attempts')
if not attempts_raw then return redis.error_reply('channel monitor request ledger not found') end
local attempts = cjson.decode(attempts_raw)
if #attempts ~= (#KEYS - 1) then
  return redis.error_reply('channel monitor request ledger changed during attribution')
end
for _, attempt in ipairs(attempts) do
  if attempt.terminal ~= true then
    return redis.error_reply('channel monitor request ledger incomplete')
  end
end
-- Validate every target key before the first mutation, so wrong Redis types
-- cannot leave a partially attributed multi-account request.
for index, _ in ipairs(attempts) do
  local cooldown_type = redis.call('TYPE', KEYS[index + 1])
  if cooldown_type.ok ~= 'none' and cooldown_type.ok ~= 'hash' then
    return redis.error_reply('invalid channel monitor cooldown key type')
  end
  local generation_type = redis.call('TYPE', KEYS[index + 1] .. ':generation')
  if generation_type.ok ~= 'none' and generation_type.ok ~= 'string' then
    return redis.error_reply('invalid channel monitor cooldown generation key type')
  end
end
local completed = redis.call('HGET', ledger_key, 'completed') or '0'
local existing_status = redis.call('HGET', ledger_key, 'final_status') or ''
if completed == '1' then
  return {0, attempts_raw, existing_status}
end
local final_failed = final_status == 'failed' or final_status == 'error'
for index, attempt in ipairs(attempts) do
  local key = KEYS[index + 1]
  local transport = attempt.transport_state or 'transport_unknown'
  local should_fail = final_failed or transport == 'transport_failed'
  if should_fail then
    local cur = redis.call('HMGET', key, 'generation', 'streak', 'until_ms')
    local generation = tonumber(cur[1]) or 0
    local streak = tonumber(cur[2]) or 0
    local until_ms = tonumber(cur[3]) or 0
    if until_ms <= now then
      streak = streak + 1
      if streak < 1 then streak = 1 end
      local ladder_index = streak
      if ladder_index > ladder_count then ladder_index = ladder_count end
      local duration = tonumber(ARGV[3 + ladder_index]) or 0
      until_ms = now + duration
      generation = redis.call('INCR', key .. ':generation')
      redis.call('HSET', key, 'generation', generation, 'streak', streak, 'until_ms', until_ms)
      redis.call('PEXPIRE', key, duration + retention_ms)
      redis.call('PEXPIRE', key .. ':generation', duration + retention_ms)
    end
  elseif transport == 'transport_succeeded' then
    local expected = tonumber(attempt.generation) or 0
    local cur = redis.call('HMGET', key, 'generation', 'until_ms')
    local generation = tonumber(cur[1]) or 0
    local until_ms = tonumber(cur[2]) or 0
    if expected > 0 and generation == expected then
      if until_ms > now then
        redis.call('HSET', key, 'streak', 0)
      else
        redis.call('DEL', key)
      end
    end
  end
end
redis.call('HSET', ledger_key, 'completed', '1', 'final_status', final_status)
redis.call('PEXPIRE', ledger_key, ledger_ttl_ms)
return {1, attempts_raw, final_status}
`)

type channelMonitorRequestLedgerStore struct{ rdb *redis.Client }

// NewChannelMonitorRequestLedgerStore creates the durable request-attempt
// ledger. A nil Redis client is an unavailable store, never an in-memory one.
func NewChannelMonitorRequestLedgerStore(rdb *redis.Client) service.ChannelMonitorRequestLedgerStore {
	return &channelMonitorRequestLedgerStore{rdb: rdb}
}

func channelMonitorRequestLedgerKey(monitorID int64, requestID string) string {
	sum := sha256.Sum256([]byte(requestID))
	return channelMonitorRequestLedgerPrefix + strconv.FormatInt(monitorID, 10) + ":" + hex.EncodeToString(sum[:])
}

func validateLedgerIdentity(monitorID int64, requestID string) error {
	if monitorID <= 0 || strings.TrimSpace(requestID) == "" {
		return fmt.Errorf("invalid channel monitor request ledger identity")
	}
	return nil
}

func (s *channelMonitorRequestLedgerStore) RegisterAttempt(ctx context.Context, monitorID int64, requestID string, accountID, generation int64, ttl time.Duration) (service.ChannelMonitorLedgerAttempt, error) {
	if s == nil || s.rdb == nil {
		return service.ChannelMonitorLedgerAttempt{}, fmt.Errorf("channel monitor request ledger redis unavailable")
	}
	if err := validateLedgerIdentity(monitorID, requestID); err != nil || accountID <= 0 {
		if err == nil {
			err = fmt.Errorf("invalid channel monitor request ledger attempt")
		}
		return service.ChannelMonitorLedgerAttempt{}, err
	}
	if ttl <= 0 {
		ttl = service.ChannelMonitorRequestLedgerTTL
	}
	value, err := channelMonitorLedgerRegisterScript.Run(ctx, s.rdb, []string{channelMonitorRequestLedgerKey(monitorID, requestID)}, accountID, generation, ttl.Milliseconds()).Result()
	if err != nil {
		return service.ChannelMonitorLedgerAttempt{}, err
	}
	var attempt service.ChannelMonitorLedgerAttempt
	if err := json.Unmarshal([]byte(fmt.Sprint(value)), &attempt); err != nil {
		return service.ChannelMonitorLedgerAttempt{}, fmt.Errorf("decode channel monitor request ledger attempt: %w", err)
	}
	return attempt, nil
}

func (s *channelMonitorRequestLedgerStore) RecordTransport(ctx context.Context, monitorID int64, requestID string, accountID int64, state service.ChannelMonitorTransportState, durationSeconds int) error {
	if s == nil || s.rdb == nil {
		return fmt.Errorf("channel monitor request ledger redis unavailable")
	}
	if err := validateLedgerIdentity(monitorID, requestID); err != nil || accountID <= 0 {
		if err == nil {
			err = fmt.Errorf("invalid channel monitor request ledger attempt")
		}
		return err
	}
	if state != service.ChannelMonitorTransportUnknown && state != service.ChannelMonitorTransportFailed && state != service.ChannelMonitorTransportSucceeded {
		return fmt.Errorf("invalid channel monitor transport state %q", state)
	}
	if durationSeconds < 0 {
		durationSeconds = 0
	}
	_, err := channelMonitorLedgerTransportScript.Run(ctx, s.rdb, []string{channelMonitorRequestLedgerKey(monitorID, requestID)}, accountID, string(state), durationSeconds, service.ChannelMonitorRequestLedgerTTL.Milliseconds()).Result()
	return err
}

func (s *channelMonitorRequestLedgerStore) Read(ctx context.Context, monitorID int64, requestID string) (service.ChannelMonitorRequestLedger, error) {
	if s == nil || s.rdb == nil {
		return service.ChannelMonitorRequestLedger{}, fmt.Errorf("channel monitor request ledger redis unavailable")
	}
	if err := validateLedgerIdentity(monitorID, requestID); err != nil {
		return service.ChannelMonitorRequestLedger{}, err
	}
	values, err := s.rdb.HMGet(ctx, channelMonitorRequestLedgerKey(monitorID, requestID), "attempts", "completed", "final_status").Result()
	if err != nil {
		return service.ChannelMonitorRequestLedger{}, err
	}
	if len(values) == 0 || values[0] == nil {
		return service.ChannelMonitorRequestLedger{}, fmt.Errorf("channel monitor request ledger not found")
	}
	return decodeLedger(monitorID, requestID, values[0], valuesAt(values, 1), valuesAt(values, 2))
}

func (s *channelMonitorRequestLedgerStore) Finalize(ctx context.Context, monitorID int64, requestID, finalStatus string, now time.Time, ladder []int) (service.ChannelMonitorFinalAttribution, error) {
	if s == nil || s.rdb == nil {
		return service.ChannelMonitorFinalAttribution{}, fmt.Errorf("channel monitor request ledger redis unavailable")
	}
	if err := validateLedgerIdentity(monitorID, requestID); err != nil || strings.TrimSpace(finalStatus) == "" || len(ladder) != 5 {
		if err == nil {
			err = fmt.Errorf("invalid channel monitor request ledger completion")
		}
		return service.ChannelMonitorFinalAttribution{}, err
	}
	ledger, err := s.Read(ctx, monitorID, requestID)
	if err != nil {
		return service.ChannelMonitorFinalAttribution{}, err
	}
	keys := make([]string, 1, len(ledger.Attempts)+1)
	keys[0] = channelMonitorRequestLedgerKey(monitorID, requestID)
	for _, attempt := range ledger.Attempts {
		keys = append(keys, channelMonitorCooldownKey(attempt.AccountID))
	}
	args := []any{finalStatus, now.UTC().UnixMilli(), len(ladder)}
	for _, minutes := range ladder {
		if minutes <= 0 {
			return service.ChannelMonitorFinalAttribution{}, fmt.Errorf("invalid cooldown duration")
		}
		args = append(args, int64(minutes)*int64(time.Minute/time.Millisecond))
	}
	args = append(args, int64(channelMonitorCooldownRetention/time.Millisecond), service.ChannelMonitorRequestLedgerTTL.Milliseconds())
	values, err := channelMonitorLedgerFinalizeScript.Run(ctx, s.rdb, keys, args...).Slice()
	if err != nil {
		return service.ChannelMonitorFinalAttribution{}, err
	}
	if len(values) != 3 {
		return service.ChannelMonitorFinalAttribution{}, fmt.Errorf("unexpected channel monitor request ledger completion result")
	}
	won, err := redisInt64Value(values[0])
	if err != nil {
		return service.ChannelMonitorFinalAttribution{}, err
	}
	ledger, err = decodeLedger(monitorID, requestID, values[1], true, values[2])
	return service.ChannelMonitorFinalAttribution{Ledger: ledger, Claimed: won == 1}, err
}

func valuesAt(values []any, index int) any {
	if index >= 0 && index < len(values) {
		return values[index]
	}
	return nil
}

func decodeLedger(monitorID int64, requestID string, attemptsValue, completedValue, statusValue any) (service.ChannelMonitorRequestLedger, error) {
	var attempts []service.ChannelMonitorLedgerAttempt
	if err := json.Unmarshal([]byte(fmt.Sprint(attemptsValue)), &attempts); err != nil {
		return service.ChannelMonitorRequestLedger{}, fmt.Errorf("decode channel monitor request ledger: %w", err)
	}
	completed := false
	switch value := completedValue.(type) {
	case bool:
		completed = value
	default:
		completed = fmt.Sprint(value) == "1"
	}
	return service.ChannelMonitorRequestLedger{MonitorID: monitorID, RequestID: requestID, Attempts: attempts, Completed: completed, FinalStatus: fmt.Sprint(statusValue)}, nil
}

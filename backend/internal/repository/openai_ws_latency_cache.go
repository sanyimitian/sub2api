package repository

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

const openAIWSLatencyStatePrefix = "openai_ws_latency:"

func (c *gatewayCache) GetOpenAIWSLatencyState(ctx context.Context, key string) ([]byte, error) {
	value, err := c.rdb.Get(ctx, openAIWSLatencyStatePrefix+key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	return value, err
}

var compareAndSwapOpenAIWSLatencyStateScript = redis.NewScript(`
local current = redis.call('GET', KEYS[1])
if (current == false and ARGV[1] ~= '') or (current ~= false and current ~= ARGV[1]) then
  return 0
end
redis.call('SET', KEYS[1], ARGV[2], 'PX', ARGV[3])
return 1
`)

func (c *gatewayCache) CompareAndSwapOpenAIWSLatencyState(ctx context.Context, key string, expected, next []byte, ttl time.Duration) (bool, error) {
	n, err := compareAndSwapOpenAIWSLatencyStateScript.Run(ctx, c.rdb,
		[]string{openAIWSLatencyStatePrefix + key}, expected, next, ttl.Milliseconds()).Int()
	return n == 1, err
}

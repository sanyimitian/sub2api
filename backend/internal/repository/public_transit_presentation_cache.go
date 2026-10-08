package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const publicTransitPresentationCachePrefix = "public_transit:presentation:"

type publicTransitPresentationCache struct {
	rdb *redis.Client
}

func NewPublicTransitPresentationCache(rdb *redis.Client) service.PublicTransitPresentationCache {
	return &publicTransitPresentationCache{rdb: rdb}
}

func publicTransitPresentationCacheKey(key string) string {
	digest := sha256.Sum256([]byte(key))
	return publicTransitPresentationCachePrefix + hex.EncodeToString(digest[:])
}

func (c *publicTransitPresentationCache) Get(ctx context.Context, key string) (service.PublicTransitPresentationState, bool, error) {
	if c == nil || c.rdb == nil {
		return service.PublicTransitPresentationState{}, false, fmt.Errorf("public transit presentation cache unavailable")
	}
	data, err := c.rdb.Get(ctx, publicTransitPresentationCacheKey(key)).Bytes()
	if err == redis.Nil {
		return service.PublicTransitPresentationState{}, false, nil
	}
	if err != nil {
		return service.PublicTransitPresentationState{}, false, err
	}
	var state service.PublicTransitPresentationState
	if err := json.Unmarshal(data, &state); err != nil {
		return service.PublicTransitPresentationState{}, false, err
	}
	return state, true, nil
}

func (c *publicTransitPresentationCache) Set(ctx context.Context, key string, state service.PublicTransitPresentationState) error {
	if c == nil || c.rdb == nil {
		return fmt.Errorf("public transit presentation cache unavailable")
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return c.rdb.Set(ctx, publicTransitPresentationCacheKey(key), data, 0).Err()
}

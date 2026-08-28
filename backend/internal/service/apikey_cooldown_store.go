package service

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"
)

// APIKeyCooldownKey identifies one shared consecutive-failure stream. Group
// identifiers are deliberately absent so an account-level event applies to
// every group that can select the account.
type APIKeyCooldownKey struct {
	AccountID int64
	Model     string
	Family    APIKeyFailureFamily
	Scope     APIKeyCooldownScope
}

func (k APIKeyCooldownKey) NormalizedModel() string {
	return NormalizeAPIKeyCooldownModel(k.Model)
}

func NormalizeAPIKeyCooldownModel(model string) string {
	return strings.ToLower(strings.TrimSpace(model))
}

func (k APIKeyCooldownKey) RedisKey() string {
	family := strings.TrimSpace(string(k.Family))
	if family == "" {
		family = string(APIKeyFailureUnknown)
	}
	if k.Scope == APIKeyCooldownScopeModel && k.NormalizedModel() != "" {
		return fmt.Sprintf("apikey_cooldown:v1:{%d}:model:%s:%s", k.AccountID, url.PathEscape(k.NormalizedModel()), family)
	}
	return fmt.Sprintf("apikey_cooldown:v1:{%d}:account:%s", k.AccountID, family)
}

type APIKeyCooldownEvent struct {
	Key              APIKeyCooldownKey
	Generation       int64
	Streak           int64
	Until            time.Time
	Created          bool
	NeedsPersistence bool
}

func (e APIKeyCooldownEvent) ActiveAt(now time.Time) bool {
	return e.Generation > 0 && e.Until.After(now)
}

type APIKeyCooldownStore interface {
	ObserveFailure(ctx context.Context, key APIKeyCooldownKey, policy APIKeyCooldownPolicy, now time.Time, upstreamReset *time.Time) (APIKeyCooldownEvent, error)
	Check(ctx context.Context, key APIKeyCooldownKey, now time.Time) (APIKeyCooldownEvent, bool, error)
	MarkPersisted(ctx context.Context, key APIKeyCooldownKey, generation int64) error
	ResetSuccess(ctx context.Context, keys []APIKeyCooldownKey, token APIKeyCooldownAttemptToken, now time.Time) error
}

// MemoryAPIKeyCooldownStore is a deterministic local implementation used as
// a dependency fallback and in unit tests. Production wiring replaces it with
// the Redis-backed implementation when the optional cache supports it.
type MemoryAPIKeyCooldownStore struct {
	mu          sync.Mutex
	events      map[string]APIKeyCooldownEvent
	generations map[string]int64
}

func NewMemoryAPIKeyCooldownStore() *MemoryAPIKeyCooldownStore {
	return &MemoryAPIKeyCooldownStore{events: make(map[string]APIKeyCooldownEvent), generations: make(map[string]int64)}
}

func (s *MemoryAPIKeyCooldownStore) ObserveFailure(ctx context.Context, key APIKeyCooldownKey, policy APIKeyCooldownPolicy, now time.Time, upstreamReset *time.Time) (APIKeyCooldownEvent, error) {
	if err := ctx.Err(); err != nil {
		return APIKeyCooldownEvent{}, err
	}
	if key.AccountID <= 0 {
		return APIKeyCooldownEvent{}, fmt.Errorf("invalid API key cooldown account id")
	}
	if !policy.Enabled || len(policy.Cooldowns) == 0 {
		return APIKeyCooldownEvent{Key: key}, nil
	}
	now = now.UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.events == nil {
		s.events = make(map[string]APIKeyCooldownEvent)
	}
	if s.generations == nil {
		s.generations = make(map[string]int64)
	}
	redisKey := key.RedisKey()
	if current, ok := s.events[redisKey]; ok && current.ActiveAt(now) {
		if key.Family == APIKeyFailureRateLimit && upstreamReset != nil && upstreamReset.After(current.Until) {
			current.Until = upstreamReset.UTC()
			current.NeedsPersistence = true
			s.events[redisKey] = current
		}
		current.Created = false
		return current, nil
	}

	streak := int64(1)
	generation := s.generations[redisKey] + 1
	if generation <= 0 {
		generation = 1
	}
	if current, ok := s.events[redisKey]; ok {
		streak = current.Streak + 1
	}
	duration := calculateAPIKeyPolicyDuration(policy, key.Family, streak, now, upstreamReset)
	if duration <= 0 {
		return APIKeyCooldownEvent{Key: key, Streak: streak, Generation: generation}, nil
	}
	event := APIKeyCooldownEvent{
		Key:              key,
		Generation:       generation,
		Streak:           streak,
		Until:            now.Add(duration),
		Created:          true,
		NeedsPersistence: true,
	}
	s.generations[redisKey] = generation
	s.events[redisKey] = event
	return event, nil
}

func (s *MemoryAPIKeyCooldownStore) Check(ctx context.Context, key APIKeyCooldownKey, now time.Time) (APIKeyCooldownEvent, bool, error) {
	if err := ctx.Err(); err != nil {
		return APIKeyCooldownEvent{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	event, ok := s.events[key.RedisKey()]
	if !ok {
		return APIKeyCooldownEvent{}, false, nil
	}
	event.Created = false
	return event, event.ActiveAt(now.UTC()), nil
}

func (s *MemoryAPIKeyCooldownStore) MarkPersisted(ctx context.Context, key APIKeyCooldownKey, generation int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	redisKey := key.RedisKey()
	event, ok := s.events[redisKey]
	if ok && event.Generation == generation {
		event.NeedsPersistence = false
		event.Created = false
		s.events[redisKey] = event
	}
	return nil
}

func (s *MemoryAPIKeyCooldownStore) ResetSuccess(ctx context.Context, keys []APIKeyCooldownKey, token APIKeyCooldownAttemptToken, now time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, key := range keys {
		redisKey := key.RedisKey()
		current, ok := s.events[redisKey]
		if !ok {
			continue
		}
		expected, ok := token.Generations[redisKey]
		if !ok || expected != current.Generation || current.ActiveAt(now.UTC()) {
			continue
		}
		delete(s.events, redisKey)
	}
	return nil
}

func calculateAPIKeyPolicyDuration(policy APIKeyCooldownPolicy, family APIKeyFailureFamily, streak int64, now time.Time, upstreamReset *time.Time) time.Duration {
	if family == APIKeyFailureRateLimit && upstreamReset != nil && upstreamReset.After(now) {
		return upstreamReset.Sub(now)
	}
	if !policy.Enabled || len(policy.Cooldowns) == 0 {
		return 0
	}
	if streak < 1 {
		streak = 1
	}
	index := streak - 1
	if policy.Mode == APIKeyCooldownModeCycle {
		index %= int64(len(policy.Cooldowns))
	} else if index >= int64(len(policy.Cooldowns)) {
		index = int64(len(policy.Cooldowns) - 1)
	}
	return time.Duration(policy.Cooldowns[index]) * time.Second
}

var _ APIKeyCooldownStore = (*MemoryAPIKeyCooldownStore)(nil)

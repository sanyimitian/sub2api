package service

import (
	"context"
	"time"
)

func beginGatewayAPIKeyCooldownAttempt(rateLimit *RateLimitService, ctx context.Context, account *Account, model string, replaySafe bool, now time.Time) (*APIKeyCooldownAttempt, bool, error) {
	if rateLimit == nil {
		return nil, false, nil
	}
	return rateLimit.BeginAPIKeyCooldownAttempt(ctx, account, model, replaySafe, now)
}

func (s *GatewayService) BeginAPIKeyCooldownAttempt(ctx context.Context, account *Account, model string, replaySafe bool, now time.Time) (*APIKeyCooldownAttempt, bool, error) {
	if s == nil {
		return nil, false, nil
	}
	return beginGatewayAPIKeyCooldownAttempt(s.rateLimitService, ctx, account, model, replaySafe, now)
}

func (s *GatewayService) ObserveAPIKeyAttemptSuccess(ctx context.Context, account *Account, attempt *APIKeyCooldownAttempt, now time.Time) error {
	if s == nil || s.rateLimitService == nil {
		return nil
	}
	return s.rateLimitService.ObserveAPIKeyAttemptSuccess(ctx, account, attempt, now)
}

func (s *GatewayService) ObserveAPIKeyAttemptError(ctx context.Context, account *Account, attempt *APIKeyCooldownAttempt, upstreamErr error, now time.Time) (APIKeyCooldownDecision, error) {
	if s == nil || s.rateLimitService == nil {
		return APIKeyCooldownDecision{Disposition: APIKeyCooldownDispositionIgnored}, nil
	}
	return s.rateLimitService.ObserveAPIKeyAttemptError(ctx, account, attempt, upstreamErr, now)
}

func (s *OpenAIGatewayService) BeginAPIKeyCooldownAttempt(ctx context.Context, account *Account, model string, replaySafe bool, now time.Time) (*APIKeyCooldownAttempt, bool, error) {
	if s == nil {
		return nil, false, nil
	}
	return beginGatewayAPIKeyCooldownAttempt(s.rateLimitService, ctx, account, model, replaySafe, now)
}

func (s *OpenAIGatewayService) ObserveAPIKeyAttemptSuccess(ctx context.Context, account *Account, attempt *APIKeyCooldownAttempt, now time.Time) error {
	if s == nil || s.rateLimitService == nil {
		return nil
	}
	return s.rateLimitService.ObserveAPIKeyAttemptSuccess(ctx, account, attempt, now)
}

func (s *OpenAIGatewayService) ObserveAPIKeyAttemptError(ctx context.Context, account *Account, attempt *APIKeyCooldownAttempt, upstreamErr error, now time.Time) (APIKeyCooldownDecision, error) {
	if s == nil || s.rateLimitService == nil {
		return APIKeyCooldownDecision{Disposition: APIKeyCooldownDispositionIgnored}, nil
	}
	return s.rateLimitService.ObserveAPIKeyAttemptError(ctx, account, attempt, upstreamErr, now)
}

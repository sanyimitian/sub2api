package service

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"maps"
	"math"
	"strconv"
	"strings"
)

const (
	publicTransitPresentationAlgorithmVersion = 4
	publicCacheRateMaximum                    = 92.0
	publicCacheLowMinimum                     = 75.0
	publicCacheLowMaximum                     = 80.0
	publicCacheBoostedRateThreshold           = 92.0
	publicCacheBoostedRateMinimum             = 90.0
	publicCacheBoostedRateMaximum             = 93.0
	publicCacheMinimumTotal                   = int64(1000)
)

type PublicTransitCachePolicy struct {
	Enabled              bool              `json:"enabled"`
	IncreasePercent      float64           `json:"increase_percent"`
	GroupIncreasePercent map[int64]float64 `json:"group_increase_percent"`
	MaximumRate          float64           `json:"maximum_rate"`
	LowRateThreshold     float64           `json:"low_rate_threshold"`
	LowRateDisplayMin    float64           `json:"low_rate_display_min"`
	LowRateDisplayMax    float64           `json:"low_rate_display_max"`
}

func DefaultPublicTransitCachePolicy() PublicTransitCachePolicy {
	return PublicTransitCachePolicy{
		Enabled:              true,
		IncreasePercent:      10,
		GroupIncreasePercent: map[int64]float64{},
		MaximumRate:          publicCacheRateMaximum,
		LowRateThreshold:     publicCacheLowMinimum,
		LowRateDisplayMin:    publicCacheLowMinimum,
		LowRateDisplayMax:    publicCacheLowMaximum,
	}
}

func (p PublicTransitCachePolicy) Validate() error {
	values := []float64{p.IncreasePercent, p.LowRateThreshold, p.LowRateDisplayMin, p.LowRateDisplayMax, p.MaximumRate}
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 100 {
			return fmt.Errorf("cache presentation percentages must be between 0 and 100")
		}
	}
	for groupID, increasePercent := range p.GroupIncreasePercent {
		if groupID <= 0 || math.IsNaN(increasePercent) || math.IsInf(increasePercent, 0) || increasePercent < 0 || increasePercent > 100 {
			return fmt.Errorf("group cache increase percentages require a positive group ID and a value between 0 and 100")
		}
	}
	if p.LowRateDisplayMin >= p.LowRateDisplayMax || p.LowRateDisplayMax > p.MaximumRate {
		return fmt.Errorf("expected low display minimum < low display maximum <= maximum rate")
	}
	return nil
}

func (p PublicTransitCachePolicy) forGroup(groupID int64) PublicTransitCachePolicy {
	if increasePercent, ok := p.GroupIncreasePercent[groupID]; ok {
		p.IncreasePercent = increasePercent
	}
	p.GroupIncreasePercent = nil
	return p
}

// PublicTransitPresentationCache stores only source and presentation counts for
// the public cache disclosure. Implementations must keep entries across restarts.
type PublicTransitPresentationCache interface {
	Get(context.Context, string) (PublicTransitPresentationState, bool, error)
	Set(context.Context, string, PublicTransitPresentationState) error
}

type PublicTransitPresentationState struct {
	Version       int                      `json:"version"`
	SourceInput   int64                    `json:"source_input"`
	SourceCreated int64                    `json:"source_created"`
	SourceRead    int64                    `json:"source_read"`
	Input         int64                    `json:"input"`
	Created       int64                    `json:"created"`
	Read          int64                    `json:"read"`
	Policy        PublicTransitCachePolicy `json:"policy"`
}

// adjustPublicCacheCounts changes only values prepared for public output. It
// keeps the cache-hit formula coherent and makes low-rate results repeatable.
func adjustPublicCacheCounts(input, created, read int64, key string) (int64, int64, int64, float64) {
	return adjustPublicCacheCountsWithPolicy(input, created, read, key, DefaultPublicTransitCachePolicy())
}

func adjustPublicCacheCountsWithPolicy(input, created, read int64, key string, policy PublicTransitCachePolicy) (int64, int64, int64, float64) {
	if input < 0 {
		input = 0
	}
	if created < 0 {
		created = 0
	}
	if read < 0 {
		read = 0
	}

	originalTotal := input + created + read
	rateDenominator := originalTotal
	if rateDenominator < 1 {
		rateDenominator = 1
	}
	rawRate := float64(read) / float64(rateDenominator) * 100
	total := originalTotal
	if total < publicCacheMinimumTotal {
		total = publicCacheMinimumTotal
	}
	lowRate := rawRate < policy.LowRateThreshold
	if lowRate {
		minimumTotal := int64(math.Ceil(100/(policy.LowRateDisplayMax-policy.LowRateDisplayMin))) + 1
		if total < minimumTotal {
			total = minimumTotal
		}
	}
	targetRate := adjustPublicCacheRateWithPolicy(rawRate, key, policy)
	adjustedRead := int64(math.Round(float64(total) * targetRate / 100))
	maximumRate := policy.MaximumRate
	if lowRate {
		maximumRate = policy.LowRateDisplayMax
	}
	maximumRead := int64(math.Floor(float64(total) * maximumRate / 100))
	if lowRate {
		minimumRead := int64(math.Ceil(float64(total) * policy.LowRateDisplayMin / 100))
		if adjustedRead < minimumRead {
			adjustedRead = minimumRead
		}
	}
	if adjustedRead > maximumRead {
		adjustedRead = maximumRead
	}
	if adjustedRead >= total {
		adjustedRead = total - 1
	}
	uncached := total - adjustedRead
	uncachedTotal := input + created
	if uncachedTotal == 0 {
		input = uncached * 4 / 5
		created = uncached - input
	} else {
		input = int64(math.Round(float64(uncached) * float64(input) / float64(uncachedTotal)))
		created = uncached - input
	}
	return input, created, adjustedRead, float64(adjustedRead) / float64(total) * 100
}

func (s *PublicTransitService) publicCachePresentation(ctx context.Context, key string, input, created, read int64) (int64, int64, int64, float64, error) {
	return s.publicCachePresentationWithPolicy(ctx, key, input, created, read, DefaultPublicTransitCachePolicy())
}

func (s *PublicTransitService) publicCachePresentationWithPolicy(ctx context.Context, key string, input, created, read int64, policy PublicTransitCachePolicy) (int64, int64, int64, float64, error) {
	if !policy.Enabled {
		return input, created, read, publicCacheRate(input, created, read), nil
	}
	if s == nil || s.presentationCache == nil {
		outInput, outCreated, outRead, rate := adjustPublicCacheCountsWithPolicy(input, created, read, key, policy)
		return outInput, outCreated, outRead, rate, nil
	}
	state, found, err := s.presentationCache.Get(ctx, key)
	if err != nil {
		return 0, 0, 0, 0, fmt.Errorf("read public transit cache pool: %w", err)
	}
	if found && state.Version == publicTransitPresentationAlgorithmVersion &&
		state.SourceInput == input && state.SourceCreated == created && state.SourceRead == read && publicTransitCachePoliciesEqual(state.Policy, policy) {
		if err := s.presentationCache.Set(ctx, key, state); err != nil {
			return 0, 0, 0, 0, fmt.Errorf("write public transit cache pool: %w", err)
		}
		return state.Input, state.Created, state.Read, publicCacheRate(state.Input, state.Created, state.Read), nil
	}
	outInput, outCreated, outRead, rate := adjustPublicCacheCountsWithPolicy(input, created, read, key, policy)
	state = PublicTransitPresentationState{
		Version:     publicTransitPresentationAlgorithmVersion,
		SourceInput: input, SourceCreated: created, SourceRead: read,
		Input: outInput, Created: outCreated, Read: outRead, Policy: policy,
	}
	if err := s.presentationCache.Set(ctx, key, state); err != nil {
		return 0, 0, 0, 0, fmt.Errorf("write public transit cache pool: %w", err)
	}
	return outInput, outCreated, outRead, rate, nil
}

func publicTransitCachePoliciesEqual(left, right PublicTransitCachePolicy) bool {
	return left.Enabled == right.Enabled &&
		left.IncreasePercent == right.IncreasePercent &&
		maps.Equal(left.GroupIncreasePercent, right.GroupIncreasePercent) &&
		left.MaximumRate == right.MaximumRate &&
		left.LowRateThreshold == right.LowRateThreshold &&
		left.LowRateDisplayMin == right.LowRateDisplayMin &&
		left.LowRateDisplayMax == right.LowRateDisplayMax
}

func publicCacheRate(input, created, read int64) float64 {
	total := input + created + read
	if total <= 0 {
		return 0
	}
	return float64(read) / float64(total) * 100
}

func (s *PublicTransitService) publicCacheRatePresentation(ctx context.Context, key string, rate float64) (float64, error) {
	return s.publicCacheRatePresentationWithPolicy(ctx, key, rate, DefaultPublicTransitCachePolicy())
}

func (s *PublicTransitService) publicCacheRatePresentationWithPolicy(ctx context.Context, key string, rate float64, policy PublicTransitCachePolicy) (float64, error) {
	if !policy.Enabled {
		return rate / 100, nil
	}
	const denominator = int64(1_000_000)
	if math.IsNaN(rate) || math.IsInf(rate, 0) || rate < 0 {
		rate = 0
	}
	read := int64(math.Round(float64(denominator) * rate / 100))
	_, _, _, adjustedRate, err := s.publicCachePresentationWithPolicy(ctx, key, denominator-read, 0, read, policy)
	return adjustedRate / 100, err
}

func adjustPublicCacheRate(rate float64, key string) float64 {
	return adjustPublicCacheRateWithPolicy(rate, key, DefaultPublicTransitCachePolicy())
}

func adjustPublicCacheRateWithPolicy(rate float64, key string, policy PublicTransitCachePolicy) float64 {
	if math.IsNaN(rate) || math.IsInf(rate, 0) || rate < 0 {
		rate = 0
	}
	if rate < policy.LowRateThreshold {
		return deterministicPublicCacheRate(key, policy.LowRateDisplayMin, policy.LowRateDisplayMax)
	}

	boostedRate := rate * (1 + policy.IncreasePercent/100)
	if boostedRate >= publicCacheBoostedRateThreshold {
		randomizedRate := deterministicPublicCacheRate(key, publicCacheBoostedRateMinimum, publicCacheBoostedRateMaximum)
		return roundPublicCacheRate(randomizedRate, publicCacheRateDecimalPlaces(rate))
	}
	return math.Min(policy.MaximumRate, boostedRate)
}

func deterministicPublicCacheRate(key string, minimum, maximum float64) float64 {
	digest := sha256.Sum256([]byte(key))
	fraction := float64(binary.BigEndian.Uint64(digest[:8])) / float64(math.MaxUint64)
	return minimum + fraction*(maximum-minimum)
}

func publicCacheRateDecimalPlaces(rate float64) int {
	formatted := strconv.FormatFloat(rate, 'f', -1, 64)
	dot := strings.IndexByte(formatted, '.')
	if dot < 0 {
		return 0
	}
	return min(len(formatted)-dot-1, 15)
}

func roundPublicCacheRate(rate float64, decimalPlaces int) float64 {
	formatted := strconv.FormatFloat(rate, 'f', decimalPlaces, 64)
	rounded, err := strconv.ParseFloat(formatted, 64)
	if err != nil {
		return rate
	}
	return rounded
}

package service

import (
	"context"
	"math"
	"strconv"
	"strings"
)

const (
	defaultOpenAIWSLatencyProbeCount        = 6
	minOpenAIWSLatencyProbeCount            = 2
	maxOpenAIWSLatencyProbeCount            = 20
	DefaultOpenAIWSLatencySwitchWaitSeconds = 300
	MinOpenAIWSLatencySwitchWaitSeconds     = 1
	MaxOpenAIWSLatencySwitchWaitSeconds     = 3600
	DefaultOpenAIWSLatencyProbeReuseSeconds = 600
	MinOpenAIWSLatencyProbeReuseSeconds     = 1
	MaxOpenAIWSLatencyProbeReuseSeconds     = 3600
)

type OpenAIWSLatencySwitchSettings struct {
	Enabled           bool
	Consecutive       int
	SlowMS            int
	MinTPS            float64
	ProbeCount        int
	WaitSeconds       int
	ProbeReuseSeconds int
}

func parseOpenAIWSLatencySwitchInt(raw string, fallback, min, max int) int {
	n, err := strconv.Atoi(raw)
	if err != nil || n < min || n > max {
		return fallback
	}
	return n
}

func parseOpenAIWSLatencyMinTPS(raw string) float64 {
	n, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 {
		return 0
	}
	return n
}

func openAIWSLatencySwitchSettingsFromValues(enabled bool, consecutive, slowMS int, minTPS float64, probeCount, waitSeconds, probeReuseSeconds int) OpenAIWSLatencySwitchSettings {
	return OpenAIWSLatencySwitchSettings{
		Enabled:           enabled,
		Consecutive:       parseOpenAIWSLatencySwitchInt(strconv.Itoa(consecutive), 3, 1, 20),
		SlowMS:            parseOpenAIWSLatencySwitchInt(strconv.Itoa(slowMS), 15000, 1000, 300000),
		MinTPS:            parseOpenAIWSLatencyMinTPS(strconv.FormatFloat(minTPS, 'f', -1, 64)),
		ProbeCount:        parseOpenAIWSLatencySwitchInt(strconv.Itoa(probeCount), defaultOpenAIWSLatencyProbeCount, minOpenAIWSLatencyProbeCount, maxOpenAIWSLatencyProbeCount),
		WaitSeconds:       parseOpenAIWSLatencySwitchInt(strconv.Itoa(waitSeconds), DefaultOpenAIWSLatencySwitchWaitSeconds, MinOpenAIWSLatencySwitchWaitSeconds, MaxOpenAIWSLatencySwitchWaitSeconds),
		ProbeReuseSeconds: parseOpenAIWSLatencySwitchInt(strconv.Itoa(probeReuseSeconds), DefaultOpenAIWSLatencyProbeReuseSeconds, MinOpenAIWSLatencyProbeReuseSeconds, MaxOpenAIWSLatencyProbeReuseSeconds),
	}
}

func (s *SettingService) GetOpenAIWSLatencySwitchSettings(ctx context.Context) OpenAIWSLatencySwitchSettings {
	defaults := openAIWSLatencySwitchSettingsFromValues(false, 0, 0, 0, 0, 0, 0)
	if s == nil || s.settingRepo == nil {
		return defaults
	}
	s.getGatewayForwardingSettingsCached(ctx)
	if cached, ok := gatewayForwardingCache.Load().(*cachedGatewayForwardingSettings); ok && cached != nil {
		return openAIWSLatencySwitchSettingsFromValues(cached.latencySwitch.Enabled, cached.latencySwitch.Consecutive, cached.latencySwitch.SlowMS, cached.latencySwitch.MinTPS, cached.latencySwitch.ProbeCount, cached.latencySwitch.WaitSeconds, cached.latencySwitch.ProbeReuseSeconds)
	}
	return defaults
}

package service

import (
	"context"
	"strconv"
)

const (
	defaultOpenAIWSLatencyProbeCount = 6
	minOpenAIWSLatencyProbeCount     = 2
	maxOpenAIWSLatencyProbeCount     = 20
)

type OpenAIWSLatencySwitchSettings struct {
	Enabled     bool
	Consecutive int
	SlowMS      int
	ProbeCount  int
}

func parseOpenAIWSLatencySwitchInt(raw string, fallback, min, max int) int {
	n, err := strconv.Atoi(raw)
	if err != nil || n < min || n > max {
		return fallback
	}
	return n
}

func openAIWSLatencySwitchSettingsFromValues(enabled bool, consecutive, slowMS, probeCount int) OpenAIWSLatencySwitchSettings {
	return OpenAIWSLatencySwitchSettings{
		Enabled:     enabled,
		Consecutive: parseOpenAIWSLatencySwitchInt(strconv.Itoa(consecutive), 3, 1, 20),
		SlowMS:      parseOpenAIWSLatencySwitchInt(strconv.Itoa(slowMS), 15000, 1000, 300000),
		ProbeCount:  parseOpenAIWSLatencySwitchInt(strconv.Itoa(probeCount), defaultOpenAIWSLatencyProbeCount, minOpenAIWSLatencyProbeCount, maxOpenAIWSLatencyProbeCount),
	}
}

func (s *SettingService) GetOpenAIWSLatencySwitchSettings(ctx context.Context) OpenAIWSLatencySwitchSettings {
	defaults := openAIWSLatencySwitchSettingsFromValues(false, 0, 0, 0)
	if s == nil || s.settingRepo == nil {
		return defaults
	}
	s.getGatewayForwardingSettingsCached(ctx)
	if cached, ok := gatewayForwardingCache.Load().(*cachedGatewayForwardingSettings); ok && cached != nil {
		return openAIWSLatencySwitchSettingsFromValues(cached.latencySwitch.Enabled, cached.latencySwitch.Consecutive, cached.latencySwitch.SlowMS, cached.latencySwitch.ProbeCount)
	}
	return defaults
}

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

// OpenAIWSLatencySwitchSettings 区分用户慢请求判定、探测并发和结果保留时间。
// 用户首字慢阈值为必填；填写最低输出 TPS（每秒输出词元数）后，必须首字慢且输出
// 速度低于该值，才计入连续慢请求。连续达到 Consecutive 后才触发候选复用／探测流程。
// 探测候选采用固定的「首字低于 10 秒」速度门槛，不随 SlowMS 或 MinTPS 改变。
// ProbeCount 是可配置的并发探测会话数，默认 6，不要求这些会话属于不同账号。
// ProbeReuseSeconds 是未使用候选的共享保留时间，默认 600 秒但必须使用实际配置；
// 到期候选不再领取，有新触发且无其余合格候选时重新探测，不因到期就主动开启探测。
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

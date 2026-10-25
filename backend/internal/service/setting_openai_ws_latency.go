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
// 速度低于该值，才计入连续慢请求。备用池开启时只领取后台候选，关闭时保留原按需探测。
// 后台备用池候选采用独立的全局首字门槛（默认十秒），不随 SlowMS 或 MinTPS 改变。
// ProbeCount 是可配置的并发探测会话数，默认 6，不要求这些会话属于不同账号。
// ProbeReuseSeconds 是未使用候选的共享保留时间，默认 600 秒但必须使用实际配置；
// 到期候选不再领取；备用池开启时按全局目标补充，关闭时按原慢切换条件触发探测。
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

// GetOpenAISessionPoolSize 返回所有启用账号合计的空闲备用数量。
func (s *SettingService) GetOpenAISessionPoolSize(ctx context.Context) int {
	if s == nil || s.settingRepo == nil {
		return 6
	}
	s.getGatewayForwardingSettingsCached(ctx)
	if cached, ok := gatewayForwardingCache.Load().(*cachedGatewayForwardingSettings); ok && cached != nil {
		return parseOpenAIWSLatencySwitchInt(strconv.Itoa(cached.sessionPoolSize), 6, 1, 100)
	}
	return 6
}

// 首字截止与入池筛选使用同一个全局门槛；旧配置缺少该项时仍为十秒。
func (s *SettingService) GetOpenAISessionPoolFirstTokenTimeoutSeconds(ctx context.Context) int {
	if s == nil || s.settingRepo == nil {
		return 10
	}
	s.getGatewayForwardingSettingsCached(ctx)
	if cached, ok := gatewayForwardingCache.Load().(*cachedGatewayForwardingSettings); ok && cached != nil {
		return parseOpenAIWSLatencySwitchInt(strconv.Itoa(cached.sessionPoolFirstTokenTimeoutSeconds), 10, 1, 600)
	}
	return 10
}

// IsOpenAISessionPoolEnabled 缺少配置时保持关闭，兼容原调度与原按需慢切换。
func (s *SettingService) IsOpenAISessionPoolEnabled(ctx context.Context) bool {
	if s == nil || s.settingRepo == nil {
		return false
	}
	s.getGatewayForwardingSettingsCached(ctx)
	if cached, ok := gatewayForwardingCache.Load().(*cachedGatewayForwardingSettings); ok && cached != nil {
		return cached.sessionPoolEnabled
	}
	return false
}

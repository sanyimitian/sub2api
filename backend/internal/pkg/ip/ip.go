// Package ip 提供客户端 IP 地址提取工具。
package ip

import (
	"net"
	"strings"

	"github.com/gin-gonic/gin"
)

const forwardedIPSettingsKey = "sub2api.forwarded_ip_settings"

type forwardedIPSettings struct {
	trustForwarded bool
	headers        []string
	trustedProxies *CompiledIPRules
}

// SetForwardedIPSettings snapshots the forwarded-IP mode and custom header list
// for this request.
func SetForwardedIPSettings(c *gin.Context, enabled bool, headers []string, proxies ...*CompiledIPRules) {
	if c == nil {
		return
	}
	var trustedProxies *CompiledIPRules
	if len(proxies) > 0 {
		trustedProxies = proxies[0]
	}
	c.Set(forwardedIPSettingsKey, forwardedIPSettings{
		trustForwarded: enabled,
		headers:        append([]string(nil), headers...),
		trustedProxies: trustedProxies,
	})
}

// SetLegacyForwardedIPTrust controls custom forwarding headers; it never
// bypasses the trusted-proxy boundary.
func SetLegacyForwardedIPTrust(c *gin.Context, enabled bool) {
	SetForwardedIPSettings(c, enabled, nil)
}

func requestForwardedIPSettings(c *gin.Context) (forwardedIPSettings, bool) {
	if c == nil {
		return forwardedIPSettings{}, false
	}
	value, ok := c.Get(forwardedIPSettingsKey)
	if !ok {
		return forwardedIPSettings{}, false
	}
	settings, ok := value.(forwardedIPSettings)
	return settings, ok
}

// GetClientIP uses the trusted proxy chain for standard forwarding headers.
// Custom headers are accepted only from explicitly configured proxies, which
// must overwrite these headers before forwarding external requests.
func GetClientIP(c *gin.Context) string {
	if c == nil {
		return ""
	}
	settings, ok := requestForwardedIPSettings(c)
	if ok && settings.trustForwarded && matchesCompiledRules(net.ParseIP(c.RemoteIP()), settings.trustedProxies) {
		for _, header := range settings.headers {
			if address, valid := resolveForwardedChain(c.Request.Header.Values(header), settings.trustedProxies); valid {
				return address
			}
		}
	}
	return GetTrustedClientIP(c)
}

// resolveForwardedChain walks from the nearest hop to the first untrusted hop.
// A private address is still a valid client address; skipping it could expose an
// attacker-controlled address earlier in the chain.
func resolveForwardedChain(values []string, proxies *CompiledIPRules) (string, bool) {
	if len(values) == 0 {
		return "", false
	}
	parts := strings.Split(strings.Join(values, ","), ",")
	for i := len(parts) - 1; i >= 0; i-- {
		parsed := net.ParseIP(strings.TrimSpace(parts[i]))
		if parsed == nil {
			return "", false
		}
		if i == 0 || !matchesCompiledRules(parsed, proxies) {
			return parsed.String(), true
		}
	}
	return "", false
}

// GetTrustedClientIP 使用 Gin 配置的可信代理链，未受信任的直连请求只取连接地址。
func GetTrustedClientIP(c *gin.Context) string {
	if c == nil {
		return ""
	}
	return normalizeIP(c.ClientIP())
}

// GetSecurityClientIP shares the same trusted address resolution with logs,
// session binding and rate limits. The switch only enables custom headers.
func GetSecurityClientIP(c *gin.Context, trustForwarded bool) string {
	if requestSettings, ok := requestForwardedIPSettings(c); ok {
		trustForwarded = requestSettings.trustForwarded
	}
	if trustForwarded {
		return GetClientIP(c)
	}
	return GetTrustedClientIP(c)
}

// normalizeIP 规范化 IP 地址，去除端口号和空格。
func normalizeIP(ip string) string {
	ip = strings.TrimSpace(ip)
	// 移除端口号（如 "192.168.1.1:8080" -> "192.168.1.1"）
	if host, _, err := net.SplitHostPort(ip); err == nil {
		return host
	}
	return ip
}

// CompiledIPRules 表示预编译的 IP 匹配规则。
// PatternCount 记录原始规则数量，用于保留“规则存在但全无效”时的行为语义。
type CompiledIPRules struct {
	CIDRs        []*net.IPNet
	IPs          []net.IP
	PatternCount int
}

// CompileIPRules 将 IP/CIDR 字符串规则预编译为可复用结构。
// 非法规则会被忽略，但 PatternCount 会保留原始规则条数。
func CompileIPRules(patterns []string) *CompiledIPRules {
	compiled := &CompiledIPRules{
		CIDRs:        make([]*net.IPNet, 0, len(patterns)),
		IPs:          make([]net.IP, 0, len(patterns)),
		PatternCount: len(patterns),
	}
	for _, pattern := range patterns {
		normalized := strings.TrimSpace(pattern)
		if normalized == "" {
			continue
		}
		if strings.Contains(normalized, "/") {
			_, cidr, err := net.ParseCIDR(normalized)
			if err != nil || cidr == nil {
				continue
			}
			compiled.CIDRs = append(compiled.CIDRs, cidr)
			continue
		}
		parsedIP := net.ParseIP(normalized)
		if parsedIP == nil {
			continue
		}
		compiled.IPs = append(compiled.IPs, parsedIP)
	}
	return compiled
}

func matchesCompiledRules(parsedIP net.IP, rules *CompiledIPRules) bool {
	if parsedIP == nil || rules == nil {
		return false
	}
	for _, cidr := range rules.CIDRs {
		if cidr.Contains(parsedIP) {
			return true
		}
	}
	for _, ruleIP := range rules.IPs {
		if parsedIP.Equal(ruleIP) {
			return true
		}
	}
	return false
}

// MatchesPattern 检查 IP 是否匹配指定的模式（支持单个 IP 或 CIDR）。
// pattern 可以是：
// - 单个 IP: "192.168.1.100"
// - CIDR 范围: "192.168.1.0/24"
func MatchesPattern(clientIP, pattern string) bool {
	ip := net.ParseIP(clientIP)
	if ip == nil {
		return false
	}

	// 尝试解析为 CIDR
	if strings.Contains(pattern, "/") {
		_, cidr, err := net.ParseCIDR(pattern)
		if err != nil {
			return false
		}
		return cidr.Contains(ip)
	}

	// 作为单个 IP 处理
	patternIP := net.ParseIP(pattern)
	if patternIP == nil {
		return false
	}
	return ip.Equal(patternIP)
}

// MatchesAnyPattern 检查 IP 是否匹配任意一个模式。
func MatchesAnyPattern(clientIP string, patterns []string) bool {
	for _, pattern := range patterns {
		if MatchesPattern(clientIP, pattern) {
			return true
		}
	}
	return false
}

// CheckIPRestriction 检查 IP 是否被 API Key 的 IP 限制允许。
// 返回值：(是否允许, 拒绝原因)
// 逻辑：
// 1. 先检查黑名单，如果在黑名单中则直接拒绝
// 2. 如果白名单不为空，IP 必须在白名单中
// 3. 如果白名单为空，允许访问（除非被黑名单拒绝）
func CheckIPRestriction(clientIP string, whitelist, blacklist []string) (bool, string) {
	return CheckIPRestrictionWithCompiledRules(
		clientIP,
		CompileIPRules(whitelist),
		CompileIPRules(blacklist),
	)
}

// CheckIPRestrictionWithCompiledRules 使用预编译规则检查 IP 是否允许访问。
func CheckIPRestrictionWithCompiledRules(clientIP string, whitelist, blacklist *CompiledIPRules) (bool, string) {
	// 规范化 IP
	clientIP = normalizeIP(clientIP)
	if clientIP == "" {
		return false, "access denied"
	}
	parsedIP := net.ParseIP(clientIP)
	if parsedIP == nil {
		return false, "access denied"
	}

	// 1. 检查黑名单
	if blacklist != nil && blacklist.PatternCount > 0 && matchesCompiledRules(parsedIP, blacklist) {
		return false, "access denied"
	}

	// 2. 检查白名单（如果设置了白名单，IP 必须在其中）
	if whitelist != nil && whitelist.PatternCount > 0 && !matchesCompiledRules(parsedIP, whitelist) {
		return false, "access denied"
	}

	return true, ""
}

// ValidateIPPattern 验证 IP 或 CIDR 格式是否有效。
func ValidateIPPattern(pattern string) bool {
	if strings.Contains(pattern, "/") {
		_, _, err := net.ParseCIDR(pattern)
		return err == nil
	}
	return net.ParseIP(pattern) != nil
}

// ValidateIPPatterns 验证多个 IP 或 CIDR 格式。
// 返回无效的模式列表。
func ValidateIPPatterns(patterns []string) []string {
	var invalid []string
	for _, p := range patterns {
		if !ValidateIPPattern(p) {
			invalid = append(invalid, p)
		}
	}
	return invalid
}

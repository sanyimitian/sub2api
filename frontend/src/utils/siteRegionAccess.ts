// 只识别服务端明确标记的全站拒绝，避免登录限制误伤其他页面。
export function getSiteRegionBlockReason(data: unknown): 'MAINLAND_ACCESS_DENIED' | 'REGION_LOOKUP_UNAVAILABLE' | null {
  if (!data || typeof data !== 'object') return null
  const value = data as { reason?: unknown; metadata?: { scope?: unknown } }
  if (value.metadata?.scope !== 'site') return null
  return value.reason === 'MAINLAND_ACCESS_DENIED' || value.reason === 'REGION_LOOKUP_UNAVAILABLE'
    ? value.reason
    : null
}

export function notifySiteRegionBlocked(data: unknown): boolean {
  const reason = getSiteRegionBlockReason(data)
  if (!reason) return false
  window.dispatchEvent(new CustomEvent('region-site-blocked', { detail: { reason } }))
  return true
}

import { apiClient } from './client'

export type RegionLoginBlockReason = 'MAINLAND_ACCESS_DENIED' | 'REGION_LOOKUP_UNAVAILABLE'

export interface AuthRegionAccess {
  allowed: boolean
  reason?: RegionLoginBlockReason
}

export async function getAuthRegionAccess(signal?: AbortSignal): Promise<AuthRegionAccess> {
  return getRegionAccess('/auth/region-access', signal)
}

export async function getSiteRegionAccess(signal?: AbortSignal): Promise<AuthRegionAccess> {
  return getRegionAccess('/site/region-access', signal)
}

async function getRegionAccess(path: string, signal?: AbortSignal): Promise<AuthRegionAccess> {
  const { data } = await apiClient.get<unknown>(path, {
    signal,
    headers: { 'Cache-Control': 'no-cache' },
  })
  if (!data || typeof data !== 'object' || !('allowed' in data) || typeof data.allowed !== 'boolean') {
    throw new Error('地区登录校验响应无效')
  }
  const reason = 'reason' in data ? data.reason : undefined
  if (data.allowed && reason !== undefined) {
    throw new Error('地区登录校验响应相互矛盾')
  }
  if (reason !== undefined && reason !== 'MAINLAND_ACCESS_DENIED' && reason !== 'REGION_LOOKUP_UNAVAILABLE') {
    throw new Error('地区登录校验原因无效')
  }
  return {
    allowed: data.allowed,
    ...(data.allowed ? {} : { reason: reason ?? 'REGION_LOOKUP_UNAVAILABLE' }),
  }
}

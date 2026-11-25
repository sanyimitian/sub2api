import { beforeEach, describe, expect, it, vi } from 'vitest'
import { getAuthRegionAccess } from '../authRegion'

const mocks = vi.hoisted(() => ({ get: vi.fn() }))
vi.mock('../client', () => ({ apiClient: mocks }))

beforeEach(() => { vi.clearAllMocks() })

describe('登录地区校验接口', () => {
  it('每次重试都请求服务端，传递取消信号并要求重新验证缓存', async () => {
    mocks.get.mockResolvedValue({ data: { allowed: true } })
    const controller = new AbortController()
    expect(await getAuthRegionAccess(controller.signal)).toEqual({ allowed: true })
    await getAuthRegionAccess()
    expect(mocks.get).toHaveBeenCalledTimes(2)
    expect(mocks.get).toHaveBeenNthCalledWith(1, '/auth/region-access', {
      signal: controller.signal,
      headers: { 'Cache-Control': 'no-cache' },
    })
  })

  it.each([null, 'invalid', {}, { allowed: 'true' }, { allowed: true, reason: 'MAINLAND_ACCESS_DENIED' }, { allowed: false, reason: 'unexpected' }])('响应异常 %j 时不能放行', async (data) => {
    mocks.get.mockResolvedValueOnce({ data })
    await expect(getAuthRegionAccess()).rejects.toThrow()
  })

  it('未附原因的明确拒绝按暂时无法验证处理', async () => {
    mocks.get.mockResolvedValueOnce({ data: { allowed: false } })
    expect(await getAuthRegionAccess()).toEqual({ allowed: false, reason: 'REGION_LOOKUP_UNAVAILABLE' })
  })
})

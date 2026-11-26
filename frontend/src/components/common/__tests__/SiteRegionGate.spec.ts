import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createI18n } from 'vue-i18n'
import SiteRegionGate from '../SiteRegionGate.vue'
import regionAccess from '@/i18n/locales/zh/regionAccess'

const mocks = vi.hoisted(() => ({ check: vi.fn(), setup: vi.fn(), afterEach: vi.fn(), removeRoute: vi.fn() }))
vi.mock('@/api/authRegion', () => ({ getSiteRegionAccess: mocks.check }))
vi.mock('@/api/setup', () => ({ getSetupStatus: mocks.setup }))
vi.mock('vue-router', () => ({ useRouter: () => ({ afterEach: mocks.afterEach }) }))
const wrappers: ReturnType<typeof mount>[] = []
function mountGate() {
  const wrapper = mount(SiteRegionGate, {
    slots: { default: '<div data-test="protected">已登录用户的业务页面</div>' },
    global: { plugins: [createI18n({ legacy: false, locale: 'zh', messages: { zh: { regionAccess } } })] },
  })
  wrappers.push(wrapper)
  return wrapper
}
function block() {
  window.dispatchEvent(new CustomEvent('region-site-blocked', { detail: { reason: 'MAINLAND_ACCESS_DENIED' } }))
}
beforeEach(() => {
  vi.useFakeTimers()
  vi.clearAllMocks()
  mocks.check.mockResolvedValue({ allowed: true })
  mocks.afterEach.mockReturnValue(mocks.removeRoute)
  mocks.setup.mockResolvedValue({ needs_setup: false })
})
afterEach(() => {
  wrappers.splice(0).forEach(wrapper => wrapper.unmount())
  vi.useRealTimers()
})

describe('全站地区访问守卫', () => {
  it('首次校验完成前不挂载业务页面，登录限制模式下可继续访问', async () => {
    let resolve!: (value: { allowed: boolean }) => void
    mocks.check.mockReturnValueOnce(new Promise(done => { resolve = done }))
    const wrapper = mountGate()
    expect(wrapper.find('[data-test="protected"]').exists()).toBe(false)
    resolve({ allowed: true })
    await flushPromises()
    expect(wrapper.find('[data-test="protected"]').exists()).toBe(true)
  })

  it('已登录页面收到全站拒绝后卸载业务内容，重新检测通过后恢复', async () => {
    const wrapper = mountGate()
    await flushPromises()
    block()
    await flushPromises()
    expect(wrapper.find('[data-test="protected"]').exists()).toBe(false)
    expect(wrapper.text()).toContain(regionAccess.siteDenied)
    expect(wrapper.text()).not.toContain(regionAccess.denied)
    await wrapper.get('button').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="protected"]').exists()).toBe(true)
  })

  it('已打开且没有操作的页面也会发现后来启用的全站限制', async () => {
    const wrapper = mountGate()
    await flushPromises()
    mocks.check.mockRejectedValueOnce({ status: 403, reason: 'MAINLAND_ACCESS_DENIED', metadata: { scope: 'site' } })
    await vi.advanceTimersByTimeAsync(15_000)
    await flushPromises()
    expect(wrapper.text()).toContain(regionAccess.siteDenied)
    expect(wrapper.find('[data-test="protected"]').exists()).toBe(false)
  })

  it('切换路由和返回页面时重新检查', async () => {
    mountGate()
    await flushPromises()
    mocks.afterEach.mock.calls[0]![0]()
    await flushPromises()
    window.dispatchEvent(new Event('focus'))
    await flushPromises()
    expect(mocks.check).toHaveBeenCalledTimes(3)
  })

  it('迟到的成功预检不会覆盖业务请求的拒绝', async () => {
    let resolve!: (value: { allowed: boolean }) => void
    mocks.check.mockReturnValueOnce(new Promise(done => { resolve = done }))
    const wrapper = mountGate()
    block()
    resolve({ allowed: true })
    await flushPromises()
    expect(wrapper.text()).toContain(regionAccess.siteDenied)
    expect(wrapper.find('[data-test="protected"]').exists()).toBe(false)
  })

  it('网络失败暂停访问但不误报中国大陆', async () => {
    mocks.check.mockRejectedValueOnce(new Error('offline'))
    const wrapper = mountGate()
    await flushPromises()
    expect(wrapper.text()).toContain(regionAccess.siteUnavailableTitle)
    expect(wrapper.text()).not.toContain(regionAccess.blockedIntro)
  })

  it.each([new Error('offline'), { status: 429 }, { status: 502 }])('后续预检暂时失败不卸载已通过检查的页面：%s', async (error) => {
    const wrapper = mountGate()
    await flushPromises()
    const content = wrapper.get('[data-test="protected"]').element
    mocks.check.mockRejectedValueOnce(error)
    await vi.advanceTimersByTimeAsync(15_000)
    expect(wrapper.get('[data-test="protected"]').element).toBe(content)
  })

  it('明确的全站校验故障仍阻止已通过检查的用户，断网不能解除拒绝', async () => {
    const wrapper = mountGate()
    await flushPromises()
    mocks.check.mockRejectedValueOnce({ status: 503, reason: 'REGION_LOOKUP_UNAVAILABLE', metadata: { scope: 'site' } })
    await vi.advanceTimersByTimeAsync(15_000)
    expect(wrapper.find('[data-test="protected"]').exists()).toBe(false)
    expect(wrapper.text()).toContain(regionAccess.siteUnavailableTitle)
    mocks.check.mockRejectedValueOnce(new Error('offline'))
    await vi.advanceTimersByTimeAsync(15_000)
    expect(wrapper.find('[data-test="protected"]').exists()).toBe(false)
  })

  it.each([true, false])('预检路由不存在时仅确认尚未安装才放行：%s', async (needsSetup) => {
    mocks.check.mockRejectedValueOnce({ status: 404 })
    mocks.setup.mockResolvedValueOnce({ needs_setup: needsSetup })
    const wrapper = mountGate()
    await flushPromises()
    expect(wrapper.find('[data-test="protected"]').exists()).toBe(needsSetup)
  })

  it('卸载取消请求、计时器和事件监听', async () => {
    const wrapper = mountGate()
    await flushPromises()
    const signal = mocks.check.mock.calls[0]![0] as AbortSignal
    wrapper.unmount()
    expect(signal.aborted).toBe(true)
    expect(mocks.removeRoute).toHaveBeenCalledOnce()
    await vi.advanceTimersByTimeAsync(30_000)
    window.dispatchEvent(new Event('focus'))
    expect(mocks.check).toHaveBeenCalledOnce()
  })
})

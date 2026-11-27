import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createI18n } from 'vue-i18n'
import LoginGateView from '../LoginGateView.vue'
import regionAccess from '@/i18n/locales/zh/regionAccess'

const mocks = vi.hoisted(() => ({ getAuthRegionAccess: vi.fn() }))
vi.mock('@/api/authRegion', () => mocks)
vi.mock('../LoginView.vue', () => ({
  __esModule: true,
  default: {
    template: '<form data-test="login-form"><input v-model="password" type="password" /></form>',
    data: () => ({ password: '' }),
  },
}))

const wrappers: VueWrapper[] = []
function mountGate() {
  const wrapper = mount(LoginGateView, {
    global: { plugins: [createI18n({ legacy: false, locale: 'zh', messages: { zh: { regionAccess } } })] },
  })
  wrappers.push(wrapper)
  return wrapper
}
function button(wrapper: VueWrapper, text: string) {
  return wrapper.findAll('button').find(item => item.text() === text)!
}

beforeEach(() => { vi.clearAllMocks() })
afterEach(() => { wrappers.splice(0).forEach(wrapper => wrapper.unmount()) })

describe('登录地区预检', () => {
  it('预检确认放行前不渲染密码表单', async () => {
    let resolve!: (value: { allowed: boolean }) => void
    mocks.getAuthRegionAccess.mockReturnValueOnce(new Promise(done => { resolve = done }))
    const wrapper = mountGate()
    expect(wrapper.get('[role="status"]').text()).toBe(regionAccess.pendingLabel)
    expect(wrapper.find('[data-test="login-form"]').exists()).toBe(false)
    resolve({ allowed: true })
    await flushPromises()
    expect(wrapper.find('[data-test="login-form"]').exists()).toBe(true)
  })

  it('大陆被拒绝时只允许重新检测，管理员也不能转入身份验证表单', async () => {
    mocks.getAuthRegionAccess.mockResolvedValue({ allowed: false, reason: 'MAINLAND_ACCESS_DENIED' })
    const wrapper = mountGate()
    await flushPromises()
    expect(wrapper.text()).toContain(regionAccess.blockedTitle)
    expect(wrapper.find('[data-test="login-form"]').exists()).toBe(false)
    expect(wrapper.findAll('button')).toHaveLength(1)
    expect(wrapper.get('button').text()).toBe(regionAccess.retry)
    await button(wrapper, regionAccess.retry).trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="login-form"]').exists()).toBe(false)
    expect(mocks.getAuthRegionAccess).toHaveBeenCalledTimes(2)
  })

  it.each([
    ['网络故障', () => Promise.reject(new Error('offline'))],
    ['没有放行标记', () => Promise.resolve({})],
    ['未知拒绝原因', () => Promise.resolve({ allowed: false, reason: 'UNKNOWN' })],
  ])('%s 时暂停登录，重新检测发起新请求', async (_name, initial) => {
    mocks.getAuthRegionAccess.mockImplementationOnce(initial).mockResolvedValueOnce({ allowed: true })
    const wrapper = mountGate()
    await flushPromises()
    expect(wrapper.text()).toContain(regionAccess.unavailableTitle)
    expect(wrapper.find('[data-test="login-form"]').exists()).toBe(false)
    await button(wrapper, regionAccess.retry).trigger('click')
    await flushPromises()
    expect(mocks.getAuthRegionAccess).toHaveBeenCalledTimes(2)
    expect(wrapper.find('[data-test="login-form"]').exists()).toBe(true)
  })

  it('服务端登录拒绝事件卸载表单，重新检测放行后不保留密码', async () => {
    mocks.getAuthRegionAccess.mockResolvedValueOnce({ allowed: true }).mockResolvedValueOnce({ allowed: true })
    const wrapper = mountGate()
    await flushPromises()
    await wrapper.get('input[type="password"]').setValue('do-not-retain')
    window.dispatchEvent(new CustomEvent('region-login-blocked', { detail: { reason: 'MAINLAND_ACCESS_DENIED' } }))
    await flushPromises()
    expect(wrapper.find('input[type="password"]').exists()).toBe(false)
    expect(wrapper.text()).toContain(regionAccess.blockedTitle)
    await button(wrapper, regionAccess.retry).trigger('click')
    await flushPromises()
    expect((wrapper.get('input[type="password"]').element as HTMLInputElement).value).toBe('')
  })

  it('迟到的放行响应不能覆盖服务端已经发出的拒绝结果', async () => {
    let resolve!: (value: { allowed: boolean }) => void
    mocks.getAuthRegionAccess.mockReturnValueOnce(new Promise(done => { resolve = done }))
    const wrapper = mountGate()
    const signal = mocks.getAuthRegionAccess.mock.calls[0]![0] as AbortSignal
    window.dispatchEvent(new CustomEvent('region-login-blocked', { detail: { reason: 'MAINLAND_ACCESS_DENIED' } }))
    resolve({ allowed: true })
    await flushPromises()
    expect(signal.aborted).toBe(true)
    expect(wrapper.text()).toContain(regionAccess.blockedTitle)
    expect(wrapper.find('[data-test="login-form"]').exists()).toBe(false)
  })

  it('离开页面时取消请求并移除事件监听', async () => {
    mocks.getAuthRegionAccess.mockReturnValueOnce(new Promise(() => {}))
    const remove = vi.spyOn(window, 'removeEventListener')
    const wrapper = mountGate()
    const signal = mocks.getAuthRegionAccess.mock.calls[0]![0] as AbortSignal
    wrapper.unmount()
    wrappers.splice(wrappers.indexOf(wrapper), 1)
    expect(signal.aborted).toBe(true)
    expect(remove).toHaveBeenCalledWith('region-login-blocked', expect.any(Function))
    remove.mockRestore()
  })
})

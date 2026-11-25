import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createI18n } from 'vue-i18n'
import RegionAccessSettings from '../RegionAccessSettings.vue'
import regionAccess from '@/i18n/locales/zh/regionAccess'

const mocks = vi.hoisted(() => ({
  getRegionAccessSettings: vi.fn(),
  updateRegionAccessSettings: vi.fn(),
  stepUp: vi.fn((action: () => unknown) => action()),
}))
vi.mock('@/api/admin/regionAccess', () => mocks)
vi.mock('@/composables/useStepUp', () => ({
  useStepUp: () => ({ run: mocks.stepUp }),
  isStepUpCancelled: (error: unknown) => (error as { code?: string })?.code === 'STEP_UP_CANCELLED',
  isStepUpBlocked: () => false,
  stepUpBlockReason: () => '',
}))
vi.mock('@/components/auth/TotpStepUpDialog.vue', () => ({ default: { template: '<div />' } }))

function mountSettings() {
  return mount(RegionAccessSettings, {
    global: {
      plugins: [createI18n({ legacy: false, locale: 'zh', messages: { zh: { regionAccess } } })],
      stubs: { teleport: true },
    },
  })
}

beforeEach(() => {
  vi.clearAllMocks()
  mocks.getRegionAccessSettings.mockResolvedValue({ enabled: false })
  mocks.updateRegionAccessSettings.mockImplementation(async (settings) => settings)
  mocks.stepUp.mockImplementation((action: () => unknown) => action())
})

describe('地区登录限制设置', () => {
  it('读取已保存值后才允许修改，初次打开不会写配置', async () => {
    const wrapper = mountSettings()
    expect(wrapper.find('[role="switch"]').exists()).toBe(false)
    expect(wrapper.get('[data-test="region-save"]').attributes('disabled')).toBeDefined()
    await flushPromises()
    expect(wrapper.get('[role="switch"]').attributes('aria-checked')).toBe('false')
    expect(wrapper.text()).toContain('不影响首页、已登录会话及 API 密钥')
    expect(wrapper.text()).toContain('所有用户（含管理员）将被拒绝登录')
    expect(wrapper.text()).toContain('管理员同样受限，来自中国大陆的新登录会被拒绝')
    expect(mocks.updateRegionAccessSettings).not.toHaveBeenCalled()
  })

  it('加载失败后不显示假定关闭状态，重试读取服务器值', async () => {
    mocks.getRegionAccessSettings.mockRejectedValueOnce(new Error('offline'))
    const wrapper = mountSettings()
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toBe(regionAccess.loadFailed)
    expect(wrapper.find('[role="switch"]').exists()).toBe(false)
    expect(wrapper.get('[data-test="region-save"]').attributes('disabled')).toBeDefined()
    mocks.getRegionAccessSettings.mockResolvedValueOnce({ enabled: true })
    await wrapper.findAll('button').find(button => button.text() === regionAccess.reload)!.trigger('click')
    await flushPromises()
    expect(wrapper.get('[role="switch"]').attributes('aria-checked')).toBe('true')
  })

  it('切换保持草稿，保存通过二次验证后才更新状态', async () => {
    let resolve!: (value: { enabled: boolean }) => void
    mocks.updateRegionAccessSettings.mockReturnValueOnce(new Promise(done => { resolve = done }))
    const wrapper = mountSettings()
    await flushPromises()
    await wrapper.get('[role="switch"]').trigger('click')
    expect(mocks.updateRegionAccessSettings).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain(regionAccess.pendingEnabled)
    await wrapper.get('[data-test="region-save"]').trigger('click')
    expect(mocks.stepUp).toHaveBeenCalledOnce()
    expect(mocks.updateRegionAccessSettings).toHaveBeenCalledWith({ enabled: true })
    expect(wrapper.get('[role="switch"]').attributes('disabled')).toBeDefined()
    expect(wrapper.text()).not.toContain(regionAccess.saved)
    resolve({ enabled: true })
    await flushPromises()
    expect(wrapper.text()).toContain(regionAccess.savedEnabled)
    expect(wrapper.text()).toContain(regionAccess.saved)
    expect(wrapper.get('[data-test="region-save"]').attributes('disabled')).toBeDefined()
  })

  it('关闭开关显式提交 false，保存失败保留草稿与重试入口', async () => {
    mocks.getRegionAccessSettings.mockResolvedValueOnce({ enabled: true })
    mocks.updateRegionAccessSettings.mockRejectedValueOnce(new Error('write failed'))
    const wrapper = mountSettings()
    await flushPromises()
    await wrapper.get('[role="switch"]').trigger('click')
    await wrapper.get('[data-test="region-save"]').trigger('click')
    await flushPromises()
    expect(mocks.updateRegionAccessSettings).toHaveBeenCalledWith({ enabled: false })
    expect(wrapper.get('[role="switch"]').attributes('aria-checked')).toBe('false')
    expect(wrapper.text()).toContain(regionAccess.pendingDisabled)
    expect(wrapper.get('[role="alert"]').text()).toBe(regionAccess.saveFailed)
    expect(wrapper.get('[data-test="region-save"]').attributes('disabled')).toBeUndefined()
  })

  it('取消二次验证不显示成功或错误，仍保留待保存修改', async () => {
    mocks.stepUp.mockRejectedValueOnce({ code: 'STEP_UP_CANCELLED' })
    const wrapper = mountSettings()
    await flushPromises()
    await wrapper.get('[role="switch"]').trigger('click')
    await wrapper.get('[data-test="region-save"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
    expect(wrapper.text()).toContain(regionAccess.unsaved)
    expect(wrapper.text()).not.toContain(regionAccess.saved)
  })

  it('预览与重新检测不会写入配置', async () => {
    const wrapper = mountSettings()
    await flushPromises()
    await wrapper.get('[data-test="region-preview"]').trigger('click')
    expect(wrapper.text()).toContain(regionAccess.blockedReason)
    expect(wrapper.text()).not.toContain('管理员登录')
    await wrapper.findAll('button').find(button => button.text() === regionAccess.retry)!.trigger('click')
    expect(wrapper.text()).toContain(regionAccess.previewRetry)
    expect(mocks.updateRegionAccessSettings).not.toHaveBeenCalled()
  })
})

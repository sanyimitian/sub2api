import { flushPromises } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const mocks = vi.hoisted(() => ({
  mount: vi.fn(),
  initI18n: vi.fn(),
  fetchPublicSettings: vi.fn(),
}))
vi.mock('vue', async importOriginal => ({
  ...await importOriginal<typeof import('vue')>(),
  createApp: () => ({ use: vi.fn(), mount: mocks.mount }),
}))
vi.mock('@/App.vue', () => ({ default: {} }))
vi.mock('@/components/common/SiteRegionGate.vue', () => ({ default: {} }))
vi.mock('@/router', () => ({ default: { isReady: async () => {} } }))
vi.mock('@/i18n', () => ({ default: {}, initI18n: mocks.initI18n }))
vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    initFromInjectedConfig: vi.fn(),
    siteLogo: '',
    cachedPublicSettings: { default_language: 'zh' },
    fetchPublicSettings: mocks.fetchPublicSettings,
  }),
}))
vi.mock('@/utils/branding', () => ({ updateFavicon: vi.fn() }))
vi.mock('@/utils/device', () => ({ isIOSDevice: () => false }))

beforeEach(() => {
  vi.resetModules()
  vi.clearAllMocks()
})

describe('启动时的默认语言', () => {
  it('注入配置过期时，在首次渲染前使用服务器最新默认语言', async () => {
    let resolve!: (value: { default_language: string }) => void
    mocks.fetchPublicSettings.mockImplementation((force: boolean) => force
      ? new Promise(done => { resolve = done })
      : Promise.resolve({ default_language: 'zh' }))
    await import('@/main')
    expect(mocks.fetchPublicSettings).toHaveBeenCalledWith(true)
    expect(mocks.mount).not.toHaveBeenCalled()
    resolve({ default_language: 'en' })
    await flushPromises()
    expect(mocks.initI18n).toHaveBeenCalledWith('en')
    expect(mocks.mount).toHaveBeenCalledWith('#app')
  })
})

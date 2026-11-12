import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { reactive } from 'vue'
import App from '@/App.vue'

const mocks = vi.hoisted(() => ({
  auth: { isAuthenticated: true, isAdmin: false, refreshUser: vi.fn() },
  app: { siteName: 'Test site', siteLogo: '', cachedPublicSettings: {}, fetchPublicSettings: vi.fn() },
  announcements: { fetchAnnouncements: vi.fn(), reset: vi.fn() },
  subscriptions: { fetchActiveSubscriptions: vi.fn(), startPolling: vi.fn(), clear: vi.fn() },
  compliance: { fetchStatus: vi.fn(), reset: vi.fn(), requireAcknowledgement: vi.fn() }
}))
let auth: typeof mocks.auth
vi.mock('@/stores', () => ({
  useAppStore: () => mocks.app,
  useAuthStore: () => auth,
  useSubscriptionStore: () => mocks.subscriptions,
  useAnnouncementStore: () => mocks.announcements,
  useAdminComplianceStore: () => mocks.compliance,
  useAdminSettingsStore: () => ({ customMenuItems: [] })
}))
vi.mock('vue-router', () => ({ RouterView: { template: '<div />' }, useRouter: () => ({ afterEach: vi.fn(), replace: vi.fn() }), useRoute: () => ({ path: '/', fullPath: '/', meta: {} }) }))
vi.mock('@/api/setup', () => ({ getSetupStatus: async () => ({ needs_setup: false }) }))
vi.mock('@/router/title', () => ({ resolveRouteDocumentTitle: () => 'Test site' }))
vi.mock('@/utils/branding', () => ({ updateFavicon: vi.fn() }))
vi.mock('@/utils/featureFlags', async importOriginal => ({ ...await importOriginal<typeof import('@/utils/featureFlags')>(), isFeatureFlagEnabled: () => false }))
vi.mock('@/components/common/Toast.vue', () => ({ default: { template: '<div />' } }))
vi.mock('@/components/common/NavigationProgress.vue', () => ({ default: { template: '<div />' } }))
vi.mock('@/components/common/AnnouncementPopup.vue', () => ({ default: { template: '<div />' } }))
vi.mock('@/components/admin/AdminComplianceDialog.vue', () => ({ default: { template: '<div />' } }))

let wrapper: ReturnType<typeof mount> | undefined
let visibility: DocumentVisibilityState
function visible(state: DocumentVisibilityState) {
  visibility = state
  document.dispatchEvent(new Event('visibilitychange'))
}
beforeEach(() => {
  vi.useFakeTimers()
  vi.clearAllMocks()
  visibility = 'visible'
  vi.spyOn(document, 'visibilityState', 'get').mockImplementation(() => visibility)
  mocks.auth.refreshUser.mockResolvedValue(undefined)
  auth = reactive({ ...mocks.auth, isAuthenticated: true })
})
afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
  vi.clearAllTimers()
  vi.useRealTimers()
  vi.restoreAllMocks()
})

describe('refreshing user balance on return', () => {
  it('refreshes on tab return or window focus and coalesces paired browser events', async () => {
    wrapper = mount(App)
    await flushPromises()
    visible('hidden')
    window.dispatchEvent(new Event('focus'))
    expect(mocks.auth.refreshUser).not.toHaveBeenCalled()
    visible('visible')
    window.dispatchEvent(new Event('focus'))
    expect(mocks.auth.refreshUser).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(1000)
    window.dispatchEvent(new Event('focus'))
    expect(mocks.auth.refreshUser).toHaveBeenCalledTimes(2)
  })

  it('stops refreshing after logout or unmount and resumes after login', async () => {
    wrapper = mount(App)
    await flushPromises()
    auth.isAuthenticated = false
    await flushPromises()
    visible('visible')
    window.dispatchEvent(new Event('focus'))
    expect(mocks.auth.refreshUser).not.toHaveBeenCalled()
    auth.isAuthenticated = true
    await flushPromises()
    window.dispatchEvent(new Event('focus'))
    expect(mocks.auth.refreshUser).toHaveBeenCalledTimes(1)
    wrapper.unmount()
    wrapper = undefined
    await vi.advanceTimersByTimeAsync(1000)
    visible('visible')
    window.dispatchEvent(new Event('focus'))
    expect(mocks.auth.refreshUser).toHaveBeenCalledTimes(1)
  })

  it('handles network failures and retries on a later return', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => {})
    mocks.auth.refreshUser.mockRejectedValueOnce(new Error('offline'))
    wrapper = mount(App)
    await flushPromises()
    window.dispatchEvent(new Event('focus'))
    await flushPromises()
    await vi.advanceTimersByTimeAsync(1000)
    visible('hidden')
    visible('visible')
    await flushPromises()
    expect(mocks.auth.refreshUser).toHaveBeenCalledTimes(2)
    expect(auth.isAuthenticated).toBe(true)
  })
})

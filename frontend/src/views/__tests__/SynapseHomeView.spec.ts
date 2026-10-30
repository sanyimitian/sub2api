import { beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import { mount, RouterLinkStub } from '@vue/test-utils'
import SynapseHomeView from '../SynapseHomeView.vue'
import LocaleSwitcher from '@/components/common/LocaleSwitcher.vue'
import AnnouncementBell from '@/components/common/AnnouncementBell.vue'

const { appStore, authStore } = vi.hoisted(() => ({
  appStore: {
    cachedPublicSettings: { doc_url: 'https://docs.example.com', model_plaza_enabled: true, model_plaza_require_auth: false },
    docUrl: '',
    publicSettingsLoaded: true,
    fetchPublicSettings: vi.fn(),
  },
  authStore: {
    isAuthenticated: false,
    isAdmin: false,
    checkAuth: vi.fn(),
  },
}))

vi.mock('@/stores', () => ({ useAppStore: () => appStore, useAuthStore: () => authStore }))
vi.mock('@/utils/featureFlags', () => ({
  FeatureFlags: { modelPlaza: 'modelPlaza' },
  isFeatureFlagEnabled: () => appStore.cachedPublicSettings.model_plaza_enabled,
}))
vi.mock('vue-i18n', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-i18n')>()
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})

function mountHome() {
  return mount(SynapseHomeView, {
    global: {
      stubs: {
        RouterLink: RouterLinkStub,
        LocaleSwitcher: { props: { textOnly: Boolean }, template: '<button>中文</button>' },
        AnnouncementBell: { props: { textOnly: Boolean }, template: '<button>消息</button>' },
      },
    },
  })
}

describe('Synapse home navigation', () => {
  beforeEach(() => {
    authStore.isAuthenticated = false
    authStore.isAdmin = false
    appStore.cachedPublicSettings = { doc_url: 'https://docs.example.com', model_plaza_enabled: true, model_plaza_require_auth: false }
    appStore.publicSettingsLoaded = true
    authStore.checkAuth.mockClear()
  })

  it('shows the navigation links without a toggle or separate brand button', () => {
    const wrapper = mountHome()
    expect(wrapper.findAll('main section')).toHaveLength(2)
    expect(wrapper.get('#metrics').exists()).toBe(true)
    expect(wrapper.get('.synapse-login').findComponent(RouterLinkStub).props('to')).toBe('/login')
    expect(wrapper.find('.synapse-brand').exists()).toBe(false)
    expect(wrapper.find('.synapse-menu-toggle').exists()).toBe(false)
    expect(wrapper.get('.synapse-menu-links').isVisible()).toBe(true)
    expect(wrapper.getComponent(LocaleSwitcher).props('textOnly')).toBe(true)
    expect(wrapper.findComponent(AnnouncementBell).exists()).toBe(false)
    expect(wrapper.get('.synapse-menu-links').text()).toContain('nav.messages')
    expect(wrapper.findAllComponents(RouterLinkStub).some((link) => link.props('to') === '/model-plaza')).toBe(true)
    expect(wrapper.get('.synapse-menu-links a[href="https://docs.example.com/"]').exists()).toBe(true)
    expect(wrapper.get('.synapse-menu-links').text()).toContain('home.hub.guide')
  })

  it('shows supported platforms in place of the old performance figures', () => {
    const wrapper = mountHome()
    expect(wrapper.get('#metrics .synapse-section-label').text()).toBe('Supported AI Platforms')
    expect(wrapper.findAll('#metrics .synapse-platform-name').map((item) => item.text())).toEqual(['Codex', 'Claude', 'Gemini', 'DeepSeek'])
    expect(wrapper.get('#metrics').text()).not.toContain('2.4ms')
  })

  it('keeps the trial action in the hero and moves pricing into navigation', () => {
    const wrapper = mountHome()
    const actions = wrapper.get('.synapse-hero-actions')
    const pricingLink = wrapper.get('.synapse-menu-links').findAllComponents(RouterLinkStub)
      .find((link) => link.text() === 'home.hub.pricing')

    expect(actions.findAllComponents(RouterLinkStub).map((link) => link.props('to'))).toEqual(['/keys'])
    expect(actions.text()).toContain('home.hub.recharge')
    expect(actions.text()).not.toContain('home.hub.console')
    expect(actions.text()).not.toContain('home.hub.pricing')
    expect(pricingLink?.props('to')).toBe('/model-plaza')
  })

  it('routes signed-in admins to their dashboard and respects model plaza visibility', () => {
    authStore.isAuthenticated = true
    authStore.isAdmin = true
    appStore.cachedPublicSettings.model_plaza_enabled = false
    const wrapper = mountHome()
    expect(wrapper.get('.synapse-login').findComponent(RouterLinkStub).props('to')).toBe('/admin/dashboard')
    expect(wrapper.getComponent(AnnouncementBell).props('textOnly')).toBe(true)
    expect(wrapper.findAllComponents(RouterLinkStub).some((link) => link.props('to') === '/model-plaza')).toBe(false)
    expect(authStore.checkAuth).toHaveBeenCalledOnce()
  })

  it('shows the complete navigation while public settings are unavailable', () => {
    appStore.publicSettingsLoaded = false
    appStore.cachedPublicSettings.doc_url = ''
    appStore.cachedPublicSettings.model_plaza_enabled = false
    const wrapper = mountHome()
    expect(wrapper.findAllComponents(RouterLinkStub).some((link) => link.props('to') === '/model-plaza')).toBe(true)
    expect(wrapper.get('.synapse-menu-links a[href="https://github.com/Wei-Shaw/sub2api/tree/main/docs"]').exists()).toBe(true)
  })

  it('keeps the hero poster visible until a usable video frame is ready', async () => {
    const wrapper = mountHome()
    const video = wrapper.get('.synapse-hero-video').element as HTMLVideoElement
    const pause = vi.spyOn(video, 'pause').mockImplementation(() => {})
    Object.defineProperty(video, 'duration', { configurable: true, value: 4.04 })

    expect(video.getAttribute('poster')).toBe('/synapse-hero-poster.jpg')
    expect(video.classList.contains('is-ready')).toBe(false)
    video.dispatchEvent(new Event('loadedmetadata'))
    expect(video.currentTime).toBe(1.75)
    expect(video.classList.contains('is-ready')).toBe(false)

    video.dispatchEvent(new Event('seeked'))
    await nextTick()
    expect(video.classList.contains('is-ready')).toBe(true)

    pause.mockRestore()
    wrapper.unmount()
  })
})

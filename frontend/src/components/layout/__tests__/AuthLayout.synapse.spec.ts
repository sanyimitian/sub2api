import { mount, RouterLinkStub } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import AuthLayout from '../AuthLayout.vue'

const { fetchPublicSettings } = vi.hoisted(() => ({ fetchPublicSettings: vi.fn() }))

vi.mock('@/stores', () => ({
  useAppStore: () => ({
    siteName: 'Custom site name',
    siteLogo: '',
    cachedPublicSettings: null,
    publicSettingsLoaded: true,
    fetchPublicSettings,
  }),
}))

describe('AuthLayout Synapse variant', () => {
  it('keeps the third-page video and CodeBot brand around the form', () => {
    const wrapper = mount(AuthLayout, {
      props: { variant: 'synapse' },
      slots: { default: '<form data-testid="auth-form" />', footer: '<a href="/register">Register</a>' },
      global: { stubs: { RouterLink: RouterLinkStub } },
    })

    expect(wrapper.get('.synapse-auth-media video').attributes('src')).toContain('hf_20260622_080203')
    expect(wrapper.get('.synapse-auth-media video').attributes('poster')).toBe('/auth-scene.jpg')
    expect(wrapper.get('.synapse-auth-brand').text()).toBe('CodeBot')
    expect(wrapper.get('.synapse-auth-brand').findComponent(RouterLinkStub).props('to')).toBe('/')
    expect(wrapper.get('[data-testid="auth-form"]').exists()).toBe(true)
    expect(wrapper.get('.synapse-auth-switch').text()).toBe('Register')
  })
})

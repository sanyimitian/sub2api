import { afterEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, mount } from '@vue/test-utils'
import IdentificationNav from '../IdentificationNav.vue'
import IdentificationTabs from '../IdentificationTabs.vue'

vi.mock('vue-router', () => ({
  useRoute: () => ({ path: '/model-identification/dilution' }),
  RouterLink: { props: ['to'], template: '<a :href="to"><slot /></a>' }
}))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
enableAutoUnmount(afterEach)
const global = { stubs: { Icon: true } }

describe('model identification navigation', () => {
  it('keeps personal navigation in the user area', () => {
    const wrapper = mount(IdentificationNav, { props: { collapsed: false }, global })
    expect(wrapper.get('button').text()).toContain('modelIdentification.title')
    expect(wrapper.findAll('a').map(link => link.attributes('href'))).toEqual(['/model-identification/degradation', '/model-identification/dilution'])
    expect(wrapper.get('a[href="/model-identification/dilution"]').classes()).toContain('sidebar-link-active')
  })
  it('switches between both administrator tools inside management', () => {
    const wrapper = mount(IdentificationTabs, { props: { active: 'dilution', admin: true }, global })
    expect(wrapper.attributes('aria-label')).toBe('modelIdentification.management')
    expect(wrapper.findAll('a').map(link => link.attributes('href'))).toEqual(['/admin/intelligence-status', '/admin/model-benchmarks'])
    expect(wrapper.text()).toContain('modelIdentification.intelligence')
    expect(wrapper.get('a[href="/admin/model-benchmarks"]').attributes('aria-current')).toBe('page')
  })
  it('keeps user tabs on user routes without role-dependent redirects', () => {
    const wrapper = mount(IdentificationTabs, { props: { active: 'dilution' }, global })
    expect(wrapper.findAll('a').map(link => link.attributes('href'))).toEqual(['/model-identification/degradation', '/model-identification/dilution'])
  })
})

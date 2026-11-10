import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import ModelIdentificationView from '../ModelIdentificationView.vue'
import { modelIdentificationAPI, type IdentificationGroup } from '@/api/modelIdentification'

const auth = vi.hoisted(() => ({ isAdmin: false, user: { id: 7 } }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => auth }))
vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError: vi.fn(), showSuccess: vi.fn() })
}))
vi.mock('vue-i18n', async (importOriginal) => ({
  ...await importOriginal<typeof import('vue-i18n')>(), useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/api/modelIdentification', () => ({
  modelIdentificationAPI: { groups: vi.fn(), results: vi.fn(), run: vi.fn(), delete: vi.fn() }
}))
const stubs = {
  AppLayout: { template: '<main><slot /></main>' },
  BaseDialog: true,
  Icon: true,
  IdentificationGallery: true,
  IdentificationPlanForm: true
}
enableAutoUnmount(afterEach)
function group(id: number): IdentificationGroup {
  return {
    group_id: id,
    name: 'Group ' + id,
    platform: 'openai',
    latest_result_id: id,
    result_count: 1,
    plan: {
      id,
      group_id: id,
      account_id: 0,
      model_id: 'model',
      prompt: 'pelican',
      cron_expression: '@every 90m',
      enabled: true,
      max_results: 24,
      last_run_at: null,
      next_run_at: null,
      created_at: '',
      updated_at: ''
    }
  }
}
beforeEach(() => {
  vi.clearAllMocks()
  localStorage.clear()
  auth.isAdmin = false
  vi.mocked(modelIdentificationAPI.groups).mockResolvedValue([group(1), group(2)])
  vi.mocked(modelIdentificationAPI.results).mockResolvedValue([])
})
describe('shared model identification page', () => {
  it('shows the configured effort to ordinary users and updates it when configuration refreshes', async () => {
    const configured = group(1)
    configured.plan.reasoning_effort = 'high'
    vi.mocked(modelIdentificationAPI.groups).mockResolvedValue([configured, group(2)])
    const wrapper = mount(ModelIdentificationView, { global: { stubs } })
    await flushPromises()
    const efforts = () => wrapper.findAll('[data-testid="group-reasoning-effort"]').map((item) => item.text())
    expect(efforts()).toEqual([
      'modelIdentification.effort: modelIdentification.effortHigh',
      'modelIdentification.effort: modelIdentification.defaultEffort'
    ])
    vi.mocked(modelIdentificationAPI.groups).mockResolvedValue([
      { ...configured, plan: { ...configured.plan, reasoning_effort: 'medium' } },
      group(2)
    ])
    await wrapper.get('button[aria-label="modelIdentification.refresh"]').trigger('click')
    await flushPromises()
    expect(efforts()[0]).toBe('modelIdentification.effort: modelIdentification.effortMedium')
  })
  it('shows shared results to ordinary users without mutation controls and persists an empty selection', async () => {
    const wrapper = mount(ModelIdentificationView, { global: { stubs } })
    await flushPromises()
    expect(modelIdentificationAPI.results).toHaveBeenCalledTimes(2)
    expect(wrapper.text()).not.toContain('modelIdentification.configure')
    expect(wrapper.text()).not.toContain('modelIdentification.run')
    await wrapper
      .findAll('button')
      .find((button) => button.text() === 'modelIdentification.showGroups')!
      .trigger('click')
    await wrapper
      .findAll('button')
      .find((button) => button.text() === 'modelIdentification.selectNone')!
      .trigger('click')
    expect(localStorage.getItem('model-identification:targets:v2:7')).toBe('[]')
    expect(wrapper.text()).toContain('modelIdentification.hiddenTitle')
    wrapper.unmount()
    vi.mocked(modelIdentificationAPI.results).mockClear()
    const restored = mount(ModelIdentificationView, { global: { stubs } })
    await flushPromises()
    expect(restored.text()).toContain('modelIdentification.hiddenTitle')
    expect(modelIdentificationAPI.results).not.toHaveBeenCalled()
  })
  it('lets administrators start a background test and reflects its lease', async () => {
    auth.isAdmin = true
    vi.mocked(modelIdentificationAPI.run).mockResolvedValue({
      ...group(1).plan,
      running_until: new Date(Date.now() + 600000).toISOString()
    })
    const wrapper = mount(ModelIdentificationView, { global: { stubs } })
    await flushPromises()
    const run = wrapper
      .findAll('button')
      .find((button) => button.text() === 'modelIdentification.run')!
    await run.trigger('click')
    await flushPromises()
    expect(modelIdentificationAPI.run).toHaveBeenCalledWith(1)
    expect(run.attributes('disabled')).toBeDefined()
    expect(wrapper.text()).toContain('modelIdentification.running')
  })
  it('shows an actionable load error without inventing samples', async () => {
    vi.mocked(modelIdentificationAPI.groups).mockRejectedValue(new Error('offline'))
    const wrapper = mount(ModelIdentificationView, { global: { stubs } })
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toContain('modelIdentification.retry')
    expect(wrapper.find('identification-gallery-stub').exists()).toBe(false)
  })
})

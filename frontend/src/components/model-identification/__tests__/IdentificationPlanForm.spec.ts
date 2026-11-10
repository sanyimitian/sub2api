import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { getAllIncludingInactive } from '@/api/admin/groups'
import type { AdminGroup } from '@/types'
import IdentificationPlanForm from '../IdentificationPlanForm.vue'
import { modelIdentificationAPI, type IdentificationGroup } from '@/api/modelIdentification'

vi.mock('vue-i18n', async (importOriginal) => ({
  ...await importOriginal<typeof import('vue-i18n')>(), useI18n: () => ({ t: (key: string) => key })
}))
vi.mock('@/api/modelIdentification', () => ({ modelIdentificationAPI: { save: vi.fn(), config: vi.fn() } }))
vi.mock('@/api/admin/groups', () => ({ getAllIncludingInactive: vi.fn() }))
enableAutoUnmount(afterEach)
beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(getAllIncludingInactive).mockResolvedValue([
    { id: 42, name: '站内分组 A', status: 'active' },
    { id: 43, name: '站内分组 B', status: 'inactive' }
  ] as AdminGroup[])
})
const existing = {
  group_id: 4, display_group_id: 42, name: 'A', platform: 'responses',
  plan: { id: 9, model_id: 'alias-model', cron_expression: '@every 90m', enabled: true, reasoning_effort: '', timeout_seconds: 600 }
} as IdentificationGroup

describe('independent API key test configuration', () => {
  it('submits the selected site group with independently configured URL and API key', async () => {
    vi.mocked(modelIdentificationAPI.save).mockResolvedValue({ id: 3 } as Awaited<ReturnType<typeof modelIdentificationAPI.save>>)
    const wrapper = mount(IdentificationPlanForm, { props: { plan: null } })
    await flushPromises()
    await wrapper.get('select[name="display_group_id"]').setValue('42')
    await wrapper.get('input[name="endpoint"]').setValue('https://example.com/v1')
    await wrapper.get('input[name="api_key"]').setValue('test-key')
    await wrapper.get('select[name="protocol"]').setValue('responses')
    await wrapper.get('input[name="model_id"]').setValue('alias-model')
    await wrapper.findAll('input[type="number"]')[0].setValue('90')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(modelIdentificationAPI.save).toHaveBeenCalledWith(null, {
      display_group_id: 42, endpoint: 'https://example.com/v1', api_key: 'test-key', protocol: 'responses',
      model_id: 'alias-model', interval_minutes: 90, timeout_seconds: 600, reasoning_effort: '', enabled: true
    })
    expect(modelIdentificationAPI.config).not.toHaveBeenCalled()
    expect(getAllIncludingInactive).toHaveBeenCalledOnce()
    expect(wrapper.emitted('saved')).toHaveLength(1)
  })
  it('loads admin-only credentials and preserves the key when left blank', async () => {
    vi.mocked(modelIdentificationAPI.config).mockResolvedValue({ display_group_id: 42, name: 'A', endpoint: 'https://example.com', api_key: 'stored-secret', protocol: 'responses' })
    const wrapper = mount(IdentificationPlanForm, { props: { plan: existing } })
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeDefined()
    await flushPromises()
    expect(modelIdentificationAPI.config).toHaveBeenCalledWith(9, expect.any(AbortSignal))
    expect((wrapper.get('select[name="display_group_id"]').element as HTMLSelectElement).value).toBe('42')
    await wrapper.get('select[name="display_group_id"]').setValue('43')
    expect((wrapper.get('input[name="api_key"]').element as HTMLInputElement).value).toBe('')
    expect(wrapper.get('input[name="api_key"]').attributes('type')).toBe('password')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(modelIdentificationAPI.save).toHaveBeenCalledWith(9, expect.objectContaining({ display_group_id: 43, api_key: '', endpoint: 'https://example.com' }))
    await wrapper.get('button[aria-pressed]').trigger('click')
    expect((wrapper.get('input[name="api_key"]').element as HTMLInputElement).value).toBe('stored-secret')
    expect(wrapper.get('input[name="api_key"]').attributes('type')).toBe('text')
  })
  it('requires a key after changing the endpoint and never silently clears effort', async () => {
    vi.mocked(modelIdentificationAPI.config).mockResolvedValue({ display_group_id: 42, name: 'A', endpoint: 'https://example.com', api_key: 'stored-secret', protocol: 'responses' })
    const wrapper = mount(IdentificationPlanForm, { props: { plan: existing } })
    await flushPromises()
    await wrapper.get('input[name="endpoint"]').setValue('https://other.example.com')
    expect(wrapper.get('input[name="api_key"]').attributes('required')).toBeDefined()
    expect(wrapper.text()).toContain('modelIdentification.endpointChanged')
    await wrapper.get('select[name="reasoning_effort"]').setValue('high')
    await wrapper.get('select[name="protocol"]').setValue('anthropic')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(modelIdentificationAPI.save).not.toHaveBeenCalled()
    expect(wrapper.get('[role="alert"]').text()).toContain('modelIdentification.effortHint')
    expect((wrapper.get('select[name="reasoning_effort"]').element as HTMLSelectElement).value).toBe('high')
  })
  it('prevents overwriting configuration when admin details fail to load', async () => {
    vi.mocked(modelIdentificationAPI.config).mockRejectedValue(new Error('unavailable'))
    const wrapper = mount(IdentificationPlanForm, { props: { plan: existing } })
    await flushPromises()
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeDefined()
    await wrapper.get('form').trigger('submit')
    expect(modelIdentificationAPI.save).not.toHaveBeenCalled()
  })

  it('requires selecting a group for legacy configurations without an association', async () => {
    vi.mocked(modelIdentificationAPI.config).mockResolvedValue({ display_group_id: null, name: 'Legacy', endpoint: 'https://example.com', api_key: 'stored-secret', protocol: 'responses' })
    const wrapper = mount(IdentificationPlanForm, { props: { plan: existing } })
    await flushPromises()
    expect((wrapper.get('select[name="display_group_id"]').element as HTMLSelectElement).value).toBe('0')
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeDefined()
    await wrapper.get('form').trigger('submit')
    expect(modelIdentificationAPI.save).not.toHaveBeenCalled()
    await wrapper.get('select[name="display_group_id"]').setValue('42')
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeUndefined()
  })
  it.each(['empty', 'failed'])('blocks saving when the group list is %s', async (state) => {
    if (state === 'empty') vi.mocked(getAllIncludingInactive).mockResolvedValue([])
    else vi.mocked(getAllIncludingInactive).mockRejectedValue(new Error('unavailable'))
    const wrapper = mount(IdentificationPlanForm, { props: { plan: null } })
    await flushPromises()
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeDefined()
    await wrapper.get('form').trigger('submit')
    expect(modelIdentificationAPI.save).not.toHaveBeenCalled()
  })
})

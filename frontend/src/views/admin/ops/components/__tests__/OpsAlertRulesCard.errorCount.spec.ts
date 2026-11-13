import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, shallowMount } from '@vue/test-utils'
import { ref } from 'vue'
import OpsAlertRulesCard from '../OpsAlertRulesCard.vue'

const { createAlertRule } = vi.hoisted(() => ({ createAlertRule: vi.fn() }))
vi.mock('vue-i18n', async (importOriginal) => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({ t: (key: string) => key })
}))
vi.mock('@vueuse/core', () => ({ useMediaQuery: () => ref(true) }))
vi.mock('@/api', () => ({ adminAPI: {
  groups: { getAll: vi.fn().mockResolvedValue([]) },
  dashboard: { getModelStats: vi.fn().mockResolvedValue({ models: [{ model: 'model-a' }] }) }
} }))
vi.mock('@/api/admin/ops', () => ({ opsAPI: { listAlertRules: vi.fn().mockResolvedValue([]), createAlertRule } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError: vi.fn(), showSuccess: vi.fn() }) }))
enableAutoUnmount(afterEach)

async function openRule() {
  const wrapper = shallowMount(OpsAlertRulesCard, {
    global: { stubs: { BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /><slot name="footer" /></div>' } } }
  })
  await flushPromises()
  await wrapper.findAll('button').find(b => b.text() === 'admin.ops.errorCountAlert.create')!.trigger('click')
  await flushPromises()
  return wrapper
}

describe('upstream error count rule editor', () => {
  beforeEach(() => vi.clearAllMocks())
  it('saves configurable threshold, window, selected codes and multiple exact model exclusions', async () => {
    const wrapper = await openRule()
    await wrapper.get('[data-testid="alert-threshold"]').setValue('35')
    await wrapper.get('[data-testid="alert-count-window"]').setValue('10')
    await wrapper.get('[data-testid="alert-status-codes"]').setValue('503')
    await wrapper.get('[data-testid="alert-excluded-model"]').setValue('model-a, model-b, model-a')
    // Saving also commits model names that have not yet been added as chips.
    await wrapper.findAll('button').find(b => b.text() === 'common.save')!.trigger('click')
    await flushPromises()
    expect(createAlertRule).toHaveBeenCalledWith(expect.objectContaining({
      metric_type: 'upstream_error_count', operator: '>', threshold: 35,
      window_minutes: 10, sustained_minutes: 0, cooldown_minutes: 10,
      filters: expect.objectContaining({ status_codes: [503], excluded_models: ['model-a', 'model-b'] })
    }))
  })
  it('rejects fractional counts and invalid error codes', async () => {
    const wrapper = await openRule()
    await wrapper.get('[data-testid="alert-threshold"]').setValue('20.5')
    await wrapper.get('[data-testid="alert-status-codes"]').setValue('200')
    await wrapper.findAll('button').find(b => b.text() === 'common.save')!.trigger('click')
    expect(createAlertRule).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('admin.ops.errorCountAlert.invalidThreshold')
    expect(wrapper.text()).toContain('admin.ops.errorCountAlert.invalidCodes')
  })
  it('removes model exclusions and preserves comma input while editing', async () => {
    const wrapper = await openRule()
    const codes = wrapper.get('[data-testid="alert-status-codes"]')
    await codes.setValue('502,')
    expect((codes.element as HTMLInputElement).value).toBe('502,')
    await codes.setValue('502, 503')
    await wrapper.get('[data-testid="alert-excluded-model"]').setValue('model-a')
    await wrapper.get('[data-testid="alert-excluded-model"]').trigger('keydown.enter')
    await wrapper.get('button[aria-label="admin.ops.errorCountAlert.removeModel"]').trigger('click')
    await wrapper.findAll('button').find(b => b.text() === 'common.save')!.trigger('click')
    await flushPromises()
    expect(createAlertRule).toHaveBeenCalledWith(expect.objectContaining({
      threshold: 20, filters: expect.objectContaining({ status_codes: [502, 503], excluded_models: [] })
    }))
  })
})

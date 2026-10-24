import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, shallowMount } from '@vue/test-utils'
import DegradationTestsPanel from '../DegradationTestsPanel.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import DegradationTestPlanForm from '../DegradationTestPlanForm.vue'
import DegradationTestResultsGallery from '../DegradationTestResultsGallery.vue'

vi.mock('vue-i18n', async (importOriginal) => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({ t: (key: string, params?: Record<string, unknown>) => params ? `${key}: ${JSON.stringify(params)}` : key })
}))
const { listByAccount, listResults, run, create, deletePlan, showSuccess, showError } = vi.hoisted(() => ({
  listByAccount: vi.fn(), listResults: vi.fn(), run: vi.fn(), create: vi.fn(), deletePlan: vi.fn(), showSuccess: vi.fn(), showError: vi.fn()
}))
vi.mock('@/api/admin', () => ({ adminAPI: { degradationTests: { listByAccount, listResults, run, create, delete: deletePlan } } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess, showError }) }))
enableAutoUnmount(afterEach)

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>(res => { resolve = res })
  return { promise, resolve }
}

async function openPanel() {
  const wrapper = shallowMount(DegradationTestsPanel, {
    props: { show: true, accountId: 1, accountName: 'Selected account', modelOptions: [] },
    global: { stubs: { BaseDialog: { template: '<div><slot /></div>' } } }
  })
  await flushPromises()
  return wrapper
}

describe('scheduled test immediate runs', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    listByAccount.mockResolvedValue([{ id: 1, model_id: 'model-1', enabled: false, cron_expression: '@every 30m' }])
    listResults.mockResolvedValue([])
    deletePlan.mockResolvedValue(undefined)
  })

  it('identifies the model and plan in deletion confirmation and never deletes on cancel', async () => {
    const wrapper = await openPanel()
    const button = wrapper.get('[data-testid="delete-plan-1"]')
    expect(button.text()).toContain('common.delete')
    await button.trigger('click')
    const confirmation = wrapper.getComponent(ConfirmDialog)
    expect(confirmation.props('show')).toBe(true)
    expect(confirmation.props('message')).toContain('"model":"model-1"')
    expect(confirmation.props('message')).toContain('"id":1')
    expect(deletePlan).not.toHaveBeenCalled()
    confirmation.vm.$emit('cancel')
    await flushPromises()
    expect(confirmation.props('show')).toBe(false)
    expect(deletePlan).not.toHaveBeenCalled()
    expect(wrapper.find('[data-testid="run-plan-1"]').exists()).toBe(true)
  })

  it('deletes only the confirmed plan while keeping another model plan visible', async () => {
    listByAccount.mockResolvedValue([1, 2].map(id => ({ id, model_id: `model-${id}`, enabled: true, cron_expression: '@every 30m' })))
    const wrapper = await openPanel()
    await wrapper.get('[data-testid="delete-plan-2"]').trigger('click')
    wrapper.getComponent(ConfirmDialog).vm.$emit('confirm')
    await flushPromises()
    expect(deletePlan).toHaveBeenCalledTimes(1)
    expect(deletePlan).toHaveBeenCalledWith(2)
    expect(wrapper.find('[data-testid="run-plan-1"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="run-plan-2"]').exists()).toBe(false)
  })

  it('runs a disabled plan once, disables duplicate clicks, and displays the returned failure', async () => {
    const pending = deferred<object>()
    run.mockReturnValueOnce(pending.promise)
    const wrapper = await openPanel()
    const button = wrapper.get('[data-testid="run-plan-1"]')
    await button.trigger('click')
    expect(button.attributes('disabled')).toBeDefined()
    expect(run).toHaveBeenCalledTimes(1)
    expect(run).toHaveBeenCalledWith(1)
    pending.resolve({ id: 10, status: 'failed', error_message: 'account quota exhausted' })
    await flushPromises()
    expect(wrapper.getComponent(DegradationTestResultsGallery).props('results')).toEqual([expect.objectContaining({ id: 10, error_message: 'account quota exhausted' })])
    expect(showError).toHaveBeenCalledWith('account quota exhausted')
    expect(button.attributes('disabled')).toBeUndefined()
  })

  it('saves the selected account, editable prompt and interval from the form', async () => {
    create.mockResolvedValue({ id: 2 })
    const wrapper = await openPanel()
    await wrapper.get('[data-testid="add-plan"]').trigger('click')
    const value = { model_id: 'chosen-model', reasoning_effort: 'xhigh' as const, prompt: 'custom prompt', cron_expression: '@every 2h', max_results: 24, enabled: true }
    wrapper.getComponent(DegradationTestPlanForm).vm.$emit('submit', value)
    await flushPromises()
    expect(create).toHaveBeenCalledTimes(1)
    expect(create).toHaveBeenCalledWith({ account_id: 1, ...value })
  })

  it('ignores a late manual result after changing accounts', async () => {
    const pending = deferred<object>()
    run.mockReturnValueOnce(pending.promise)
    const wrapper = await openPanel()
    await wrapper.get('[data-testid="run-plan-1"]').trigger('click')
    listByAccount.mockResolvedValue([])
    await wrapper.setProps({ accountId: 2 })
    await flushPromises()
    pending.resolve({ id: 10, status: 'success', response_text: 'old account output' })
    await flushPromises()
    expect(wrapper.findComponent(DegradationTestResultsGallery).exists()).toBe(false)
    expect(showSuccess).not.toHaveBeenCalled()
  })

  it('shows HTTP execution errors in the panel', async () => {
    run.mockRejectedValue(new Error('This plan is already running'))
    const wrapper = await openPanel()
    await wrapper.get('[data-testid="run-plan-1"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toBe('This plan is already running')
  })

  it('requests and displays at most ten results even when an old server returns more', async () => {
    listByAccount.mockResolvedValue([{ id: 1, model_id: 'model-1', enabled: false, cron_expression: '@every 30m', max_results: 50 }])
    listResults.mockResolvedValue(Array.from({ length: 20 }, (_, id) => ({ id, status: 'success' })))
    const wrapper = await openPanel()
    await wrapper.get('.cursor-pointer').trigger('click')
    await flushPromises()
    expect(listResults).toHaveBeenLastCalledWith(1, 10)
    expect(wrapper.getComponent(DegradationTestResultsGallery).props('results')).toHaveLength(10)
    expect(wrapper.text()).not.toContain('admin.degradationTests.loadMore')
  })

  it('loads twenty-four results only after opening details and keeps the main list at ten', async () => {
    listResults.mockImplementation((_planId: number, limit: number) => Promise.resolve(Array.from({ length: limit }, (_, id) => ({ id: id + 1, status: 'success' }))))
    const wrapper = await openPanel()
    await wrapper.get('.cursor-pointer').trigger('click')
    await flushPromises()
    expect(listResults).toHaveBeenCalledTimes(1)
    expect(listResults).toHaveBeenLastCalledWith(1, 10)
    expect(wrapper.find('[data-testid="results-details"]').exists()).toBe(false)
    wrapper.getComponent(DegradationTestResultsGallery).vm.$emit('details')
    await flushPromises()
    expect(listResults).toHaveBeenLastCalledWith(1, 24)
    const galleries = wrapper.findAllComponents(DegradationTestResultsGallery)
    expect(galleries[0].props('results')).toHaveLength(10)
    expect(galleries[1].props('results')).toHaveLength(24)
    expect(galleries[1].props('displayLimit')).toBe(24)
    expect(galleries[1].props('showDetailsButton')).toBe(false)
  })

  it('ignores a previous details request after closing and reopening the dialog', async () => {
    const old = deferred<object[]>()
    listResults.mockResolvedValueOnce([{ id: 1 }]).mockReturnValueOnce(old.promise).mockResolvedValueOnce([{ id: 24 }])
    const wrapper = await openPanel()
    await wrapper.get('.cursor-pointer').trigger('click')
    await flushPromises()
    wrapper.getComponent(DegradationTestResultsGallery).vm.$emit('details')
    await flushPromises()
    wrapper.findAllComponents(BaseDialog)[1].vm.$emit('close')
    await flushPromises()
    wrapper.getComponent(DegradationTestResultsGallery).vm.$emit('details')
    await flushPromises()
    old.resolve([{ id: 99 }])
    await flushPromises()
    expect(wrapper.findAllComponents(DegradationTestResultsGallery)[1].props('results')).toEqual([{ id: 24 }])
    expect(showError).not.toHaveBeenCalled()
  })

  it('closes the details dialog when changing accounts and ignores its pending result', async () => {
    const pending = deferred<object[]>()
    listResults.mockResolvedValueOnce([{ id: 1 }]).mockReturnValueOnce(pending.promise)
    const wrapper = await openPanel()
    await wrapper.get('.cursor-pointer').trigger('click')
    await flushPromises()
    wrapper.getComponent(DegradationTestResultsGallery).vm.$emit('details')
    await flushPromises()
    await wrapper.setProps({ accountId: 2 })
    await flushPromises()
    pending.resolve([{ id: 99 }])
    await flushPromises()
    expect(wrapper.find('[data-testid="results-details"]').exists()).toBe(false)
    expect(wrapper.findAllComponents(DegradationTestResultsGallery).every(gallery => gallery.props('results').every((result: { id: number }) => result.id !== 99))).toBe(true)
  })
})

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'
import Toggle from '@/components/common/Toggle.vue'
import CreateDegradationTestPlanDialog from '../CreateDegradationTestPlanDialog.vue'
import DegradationTestPlanForm from '../DegradationTestPlanForm.vue'
import { DEFAULT_DEGRADATION_TEST_PROMPT } from '@/utils/degradationTestPreview'

const { listAccounts, getAvailableModels, create, showSuccess, showError } = vi.hoisted(() => ({
  listAccounts: vi.fn(), getAvailableModels: vi.fn(), create: vi.fn(), showSuccess: vi.fn(), showError: vi.fn()
}))
vi.mock('@/api/admin', () => ({
  adminAPI: { accounts: { list: listAccounts, getAvailableModels }, degradationTests: { create } }
}))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess, showError }) }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

enableAutoUnmount(afterEach)

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (error: unknown) => void
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}

const account = (id: number, platform = 'openai') => ({ id, name: `Account ${id}`, platform })
const page = (items = [account(1), account(2, 'anthropic')]) => ({ items, total: items.length, page: 1, page_size: 50, pages: 1 })
const plan = { id: 99, account_id: 1, model_id: 'chosen-model' }

async function openDialog() {
  const wrapper = mount(CreateDegradationTestPlanDialog, {
    global: { stubs: {
      BaseDialog: { props: ['show', 'title', 'showCloseButton', 'closeOnEscape'], template: '<div><slot /></div>' },
      Select: true
    } }
  })
  await flushPromises()
  return wrapper
}

async function selectAccount(wrapper: Awaited<ReturnType<typeof openDialog>>, id: number) {
  wrapper.getComponent(Select).vm.$emit('update:modelValue', id)
  await flushPromises()
}

describe('create a generation plan from intelligence status', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    listAccounts.mockResolvedValue(page())
    getAvailableModels.mockResolvedValue([{ id: 'chosen-model', display_name: 'Chosen Model' }])
    create.mockResolvedValue(plan)
  })

  it('loads searchable accounts from every platform and opens the existing plan form after selection', async () => {
    const wrapper = await openDialog()
    expect(listAccounts).toHaveBeenCalledWith(1, 50, { search: '', lite: 'true' }, { signal: expect.any(AbortSignal) })
    const selector = wrapper.getComponent(Select)
    expect(selector.props()).toMatchObject({ remote: true, searchable: true, loading: false, modelValue: null })
    expect(selector.props('options')).toEqual([
      { value: 1, label: 'Account 1 · openai · #1' },
      { value: 2, label: 'Account 2 · anthropic · #2' }
    ])
    expect(wrapper.findComponent(DegradationTestPlanForm).exists()).toBe(false)
    expect(getAvailableModels).not.toHaveBeenCalled()
    await selectAccount(wrapper, 2)
    expect(getAvailableModels).toHaveBeenCalledWith(2)
    expect(wrapper.getComponent(DegradationTestPlanForm).props('modelOptions')).toEqual([{ value: 'chosen-model', label: 'Chosen Model' }])
    expect(wrapper.get<HTMLTextAreaElement>('textarea').element.value).toBe(DEFAULT_DEGRADATION_TEST_PROMPT)
    expect(create).not.toHaveBeenCalled()
  })

  it('searches on the server, ignores an older search, and keeps the selected account and draft', async () => {
    const wrapper = await openDialog()
    await selectAccount(wrapper, 1)
    await wrapper.get('input[list]').setValue('draft-model')
    const oldSearch = deferred<ReturnType<typeof page>>()
    listAccounts.mockReturnValueOnce(oldSearch.promise).mockResolvedValueOnce(page([account(150)]))
    const selector = wrapper.getComponent(Select)
    selector.vm.$emit('search', 'old search')
    await flushPromises()
    const oldSignal = listAccounts.mock.calls[1][3].signal as AbortSignal
    selector.vm.$emit('search', '  Account 150  ')
    await flushPromises()
    expect(oldSignal.aborted).toBe(true)
    expect(listAccounts).toHaveBeenLastCalledWith(1, 50, { search: 'Account 150', lite: 'true' }, { signal: expect.any(AbortSignal) })
    oldSearch.resolve(page([account(90)]))
    await flushPromises()
    expect(selector.props('options')).toEqual([
      { value: 1, label: 'Account 1 · openai · #1' },
      { value: 150, label: 'Account 150 · openai · #150' }
    ])
    expect(selector.props('modelValue')).toBe(1)
    await selectAccount(wrapper, 1)
    expect(wrapper.get<HTMLInputElement>('input[list]').element.value).toBe('draft-model')
    expect(getAvailableModels).toHaveBeenCalledTimes(1)
    await selectAccount(wrapper, 150)
    expect(getAvailableModels).toHaveBeenLastCalledWith(150)
    expect(wrapper.get<HTMLInputElement>('input[list]').element.value).toBe('')
  })

  it('shows an account-loading error and retries without creating a plan', async () => {
    listAccounts.mockRejectedValueOnce(new Error('accounts temporarily unavailable'))
    const wrapper = await openDialog()
    expect(wrapper.get('[role="alert"]').text()).toContain('accounts temporarily unavailable')
    expect(wrapper.findComponent(DegradationTestPlanForm).exists()).toBe(false)
    await wrapper.get('[role="alert"] button').trigger('click')
    await flushPromises()
    expect(listAccounts).toHaveBeenCalledTimes(2)
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
    expect(wrapper.getComponent(Select).props('options')).toHaveLength(2)
    expect(create).not.toHaveBeenCalled()
  })

  it('ignores model suggestions returned for the previous account', async () => {
    const oldModels = deferred<{ id: string; display_name?: string }[]>()
    getAvailableModels.mockReturnValueOnce(oldModels.promise).mockResolvedValueOnce([{ id: 'anthropic-model' }])
    const wrapper = await openDialog()
    await selectAccount(wrapper, 1)
    expect(wrapper.get('[role="status"]').text()).toContain('common.loading')
    await selectAccount(wrapper, 2)
    oldModels.resolve([{ id: 'obsolete-model' }])
    await flushPromises()
    expect(wrapper.getComponent(DegradationTestPlanForm).props('modelOptions')).toEqual([{ value: 'anthropic-model', label: 'anthropic-model' }])
    expect(wrapper.getComponent(Select).props('modelValue')).toBe(2)
    expect(wrapper.find('[role="status"]').exists()).toBe(false)
  })

  it('allows a manually entered model when suggestions fail', async () => {
    getAvailableModels.mockRejectedValueOnce(new Error('model catalog unavailable'))
    const wrapper = await openDialog()
    await selectAccount(wrapper, 1)
    expect(wrapper.get('[role="status"]').text()).toContain('admin.intelligenceStatus.modelsLoadFailed')
    expect(wrapper.getComponent(DegradationTestPlanForm).props('modelOptions')).toEqual([])
    await wrapper.get('input[list]').setValue('manual-model')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(create).toHaveBeenCalledWith(expect.objectContaining({ account_id: 1, model_id: 'manual-model' }))
    expect(wrapper.emitted('created')).toEqual([[plan]])
  })

  it('submits edited settings once, keeps its account fixed, and blocks closing while saving', async () => {
    const pending = deferred<typeof plan>()
    create.mockReturnValueOnce(pending.promise)
    const wrapper = await openDialog()
    await selectAccount(wrapper, 1)
    await wrapper.get('input[list]').setValue(' chosen-model ')
    await wrapper.get('textarea').setValue('Draw a custom SVG animation')
    await wrapper.get('[data-testid="reasoning-effort"]').setValue('xhigh')
    await wrapper.get('select[aria-label]').setValue('h')
    await wrapper.get('input[type="number"]').setValue(3)
    await wrapper.get('[data-testid="timeout-minutes"]').setValue(12)
    wrapper.getComponent(Toggle).vm.$emit('update:modelValue', false)
    await wrapper.get('form').trigger('submit')
    await wrapper.get('form').trigger('submit')
    expect(create).toHaveBeenCalledTimes(1)
    expect(create).toHaveBeenCalledWith({
      account_id: 1, model_id: 'chosen-model', reasoning_effort: 'xhigh',
      prompt: 'Draw a custom SVG animation', cron_expression: '@every 3h',
      timeout_seconds: 720, max_results: 24, enabled: false
    })
    expect(wrapper.getComponent(BaseDialog).props()).toMatchObject({ showCloseButton: false, closeOnEscape: false })
    expect(wrapper.getComponent(Select).props('disabled')).toBe(true)
    expect(wrapper.get('fieldset').attributes('disabled')).toBeDefined()
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeDefined()
    wrapper.getComponent(BaseDialog).vm.$emit('close')
    wrapper.getComponent(DegradationTestPlanForm).vm.$emit('cancel')
    await selectAccount(wrapper, 2)
    expect(wrapper.emitted('close')).toBeUndefined()
    expect(wrapper.getComponent(Select).props('modelValue')).toBe(1)
    expect(getAvailableModels).toHaveBeenCalledTimes(1)
    pending.resolve(plan)
    await flushPromises()
    expect(wrapper.emitted('created')).toEqual([[plan]])
    expect(showSuccess).toHaveBeenCalledWith('admin.degradationTests.createSuccess')
    expect(showError).not.toHaveBeenCalled()
  })

  it('retains entered configuration after a failed save and allows retry', async () => {
    create.mockRejectedValueOnce(new Error('plan could not be saved'))
    const wrapper = await openDialog()
    await selectAccount(wrapper, 2)
    await wrapper.get('input[list]').setValue('retry-model')
    await wrapper.get('textarea').setValue('Keep this prompt')
    await wrapper.get('[data-testid="reasoning-effort"]').setValue('high')
    await wrapper.get('select[aria-label]').setValue('h')
    await wrapper.get('input[type="number"]').setValue(6)
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toBe('plan could not be saved')
    expect(showError).toHaveBeenCalledWith('plan could not be saved')
    expect(wrapper.emitted('created')).toBeUndefined()
    expect(wrapper.emitted('close')).toBeUndefined()
    expect(wrapper.get<HTMLInputElement>('input[list]').element.value).toBe('retry-model')
    expect(wrapper.get<HTMLTextAreaElement>('textarea').element.value).toBe('Keep this prompt')
    expect(wrapper.get<HTMLSelectElement>('[data-testid="reasoning-effort"]').element.value).toBe('high')
    expect(wrapper.get<HTMLInputElement>('input[type="number"]').element.value).toBe('6')
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeUndefined()
    expect(wrapper.getComponent(BaseDialog).props()).toMatchObject({ showCloseButton: true, closeOnEscape: true })
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(create).toHaveBeenCalledTimes(2)
    expect(create.mock.calls[1][0]).toEqual(create.mock.calls[0][0])
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
    expect(wrapper.emitted('created')).toEqual([[plan]])
  })

  it('aborts an account request and ignores its failure after cancelling and leaving', async () => {
    const pendingAccounts = deferred<ReturnType<typeof page>>()
    listAccounts.mockReturnValueOnce(pendingAccounts.promise)
    const wrapper = await openDialog()
    const signal = listAccounts.mock.calls[0][3].signal as AbortSignal
    await wrapper.get('button').trigger('click')
    expect(wrapper.emitted('close')).toEqual([[]])
    wrapper.unmount()
    expect(signal.aborted).toBe(true)
    pendingAccounts.reject(new Error('late account failure'))
    await flushPromises()
    expect(showError).not.toHaveBeenCalled()
    expect(getAvailableModels).not.toHaveBeenCalled()
    expect(create).not.toHaveBeenCalled()
  })

  it('ignores pending models after closing and a pending save after navigation', async () => {
    const pendingModels = deferred<{ id: string }[]>()
    getAvailableModels.mockReturnValueOnce(pendingModels.promise)
    const closed = await openDialog()
    await selectAccount(closed, 1)
    closed.getComponent(DegradationTestPlanForm).vm.$emit('cancel')
    expect(closed.emitted('close')).toEqual([[]])
    closed.unmount()
    pendingModels.resolve([{ id: 'late-model' }])
    await flushPromises()
    expect(showError).not.toHaveBeenCalled()
    const pendingSave = deferred<typeof plan>()
    create.mockReturnValueOnce(pendingSave.promise)
    const leaving = await openDialog()
    await selectAccount(leaving, 1)
    await leaving.get('input[list]').setValue('chosen-model')
    await leaving.get('form').trigger('submit')
    leaving.unmount()
    pendingSave.resolve(plan)
    await flushPromises()
    expect(leaving.emitted('created')).toBeUndefined()
    expect(showSuccess).not.toHaveBeenCalled()
    expect(showError).not.toHaveBeenCalled()
  })
})

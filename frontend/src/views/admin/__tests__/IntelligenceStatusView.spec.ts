import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, shallowMount } from '@vue/test-utils'
import IntelligenceStatusView from '../IntelligenceStatusView.vue'
import IntelligenceStatusCard from '@/components/admin/IntelligenceStatusCard.vue'
import DegradationTestsPanel from '@/components/admin/account/DegradationTestsPanel.vue'
import type { DegradationTestPlan, DegradationTestStatus } from '@/types'

const { listStatus, run, update, getAvailableModels, showSuccess, showError } = vi.hoisted(() => ({
  listStatus: vi.fn(), run: vi.fn(), update: vi.fn(), getAvailableModels: vi.fn(), showSuccess: vi.fn(), showError: vi.fn()
}))
vi.mock('@/api/admin', () => ({ adminAPI: { degradationTests: { listStatus, run, update }, accounts: { getAvailableModels } } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess, showError }) }))
vi.mock('vue-i18n', async importOriginal => ({ ...await importOriginal<typeof import('vue-i18n')>(), useI18n: () => ({ t: (key: string) => key }) }))
enableAutoUnmount(afterEach)

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (error: unknown) => void
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}

const item = (id: number, status: string | null = 'success') => ({
  plan: { id, account_id: id + 10, model_id: `model-${id}`, enabled: id !== 2, cron_expression: '@every 30m' },
  account_name: `account-${id}`, account_platform: 'openai',
  latest_result: status ? { id: id * 100, model_id: `model-${id}`, status, error_message: status === 'failed' ? 'latest failure' : '', started_at: '2026-10-25T00:00:00Z' } : null
} as DegradationTestStatus)

async function openView() {
  const wrapper = shallowMount(IntelligenceStatusView, { global: { stubs: { AppLayout: { template: '<main><slot /></main>' } } } })
  await flushPromises()
  return wrapper
}

describe('intelligence status overview', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    listStatus.mockResolvedValue([item(1), item(2, 'failed'), item(3, null)])
    getAvailableModels.mockResolvedValue([])
  })

  it('loads all plans, preserving latest failures, disabled plans, and untested plans', async () => {
    const wrapper = await openView()
    const cards = wrapper.findAllComponents(IntelligenceStatusCard)
    expect(listStatus).toHaveBeenCalledWith(expect.any(AbortSignal))
    expect(cards).toHaveLength(3)
    expect(cards[1].props('item')).toMatchObject({ plan: { enabled: false }, latest_result: { status: 'failed', error_message: 'latest failure' } })
    expect(cards[2].props('item').latest_result).toBeNull()
  })

  it('runs a disabled plan once and refreshes its failed execution without affecting other cards', async () => {
    const pending = deferred<object>()
    run.mockReturnValue(pending.promise)
    const wrapper = await openView()
    const card = wrapper.findAllComponents(IntelligenceStatusCard)[1]
    card.vm.$emit('run')
    card.vm.$emit('run')
    await flushPromises()
    expect(run).toHaveBeenCalledTimes(1)
    expect(run).toHaveBeenCalledWith(2)
    expect(card.props('running')).toBe(true)
    const failed = { ...item(2, 'failed').latest_result!, id: 999, error_message: 'new quota failure' }
    listStatus.mockResolvedValue([item(1), { ...item(2), latest_result: failed }, item(3, null)])
    pending.resolve(failed)
    await flushPromises()
    expect(card.props('running')).toBe(false)
    expect(card.props('item').latest_result).toEqual(failed)
    expect(wrapper.findAllComponents(IntelligenceStatusCard)[0].props('item')).toEqual(item(1))
    expect(showError).toHaveBeenCalledWith('new quota failure')
    expect(listStatus).toHaveBeenCalledTimes(2)
  })

  it('shows execution request errors on their card and reconciles any saved result', async () => {
    run.mockRejectedValue(new Error('execution response lost'))
    const wrapper = await openView()
    const card = wrapper.findAllComponents(IntelligenceStatusCard)[0]
    card.vm.$emit('run')
    await flushPromises()
    expect(card.props('error')).toBe('execution response lost')
    expect(listStatus).toHaveBeenCalledTimes(2)
  })

  it('disables and re-enables a schedule while preserving its latest result and the auto-refresh preference', async () => {
    vi.useFakeTimers()
    try {
      const wrapper = await openView()
      const card = wrapper.findAllComponents(IntelligenceStatusCard)[0]
      const latestResult = card.props('item').latest_result
      await wrapper.get('[data-testid="auto-refresh"]').setValue(false)

      for (const enabled of [false, true]) {
        const refreshed = deferred<DegradationTestStatus[]>()
        const savedPlan = { ...item(1).plan, enabled, next_run_at: '2026-10-25T01:00:00Z' }
        update.mockResolvedValueOnce(savedPlan)
        listStatus.mockReturnValueOnce(refreshed.promise)
        card.vm.$emit('toggleEnabled', enabled)
        await flushPromises()
        expect(update).toHaveBeenLastCalledWith(1, { enabled })
        expect(card.props('item').plan).toEqual(savedPlan)
        expect(card.props('item').latest_result).toEqual(latestResult)
        refreshed.resolve([{ ...item(1), plan: savedPlan }, item(2, 'failed'), item(3, null)])
        await flushPromises()
        expect(card.props('toggling')).toBe(false)
      }

      expect(update).toHaveBeenCalledTimes(2)
      expect(listStatus).toHaveBeenCalledTimes(3)
      expect((wrapper.get('[data-testid="auto-refresh"]').element as HTMLInputElement).checked).toBe(false)
      await vi.advanceTimersByTimeAsync(30000)
      expect(listStatus).toHaveBeenCalledTimes(3)
    } finally { vi.useRealTimers() }
  })

  it('deduplicates pending changes per card without locking other schedules', async () => {
    const first = deferred<DegradationTestPlan>()
    const second = deferred<DegradationTestPlan>()
    update.mockImplementation((id: number) => id === 1 ? first.promise : second.promise)
    const wrapper = await openView()
    const cards = wrapper.findAllComponents(IntelligenceStatusCard)
    cards[0].vm.$emit('toggleEnabled', false)
    cards[0].vm.$emit('toggleEnabled', false)
    cards[1].vm.$emit('toggleEnabled', true)
    await flushPromises()
    expect(update.mock.calls).toEqual([[1, { enabled: false }], [2, { enabled: true }]])
    expect(cards[0].props('toggling')).toBe(true)
    expect(cards[1].props('toggling')).toBe(true)
    expect(cards[2].props('toggling')).toBe(false)

    const disabled = { ...item(1), plan: { ...item(1).plan, enabled: false } }
    const enabled = { ...item(2, 'failed'), plan: { ...item(2).plan, enabled: true } }
    listStatus.mockResolvedValue([disabled, enabled, item(3, null)])
    first.resolve(disabled.plan)
    second.resolve(enabled.plan)
    await flushPromises()
    expect(cards[0].props('item').plan.enabled).toBe(false)
    expect(cards[1].props('item').plan.enabled).toBe(true)
    expect(cards[0].props('toggling')).toBe(false)
    expect(cards[1].props('toggling')).toBe(false)
    expect(cards[2].props('item')).toEqual(item(3, null))
  })

  it('keeps the saved schedule when a change fails, shows its error, and allows retry', async () => {
    update.mockRejectedValueOnce(new Error('schedule update rejected'))
    const wrapper = await openView()
    const cards = wrapper.findAllComponents(IntelligenceStatusCard)
    cards[0].vm.$emit('toggleEnabled', false)
    await flushPromises()
    expect(cards[0].props('item')).toEqual(item(1))
    expect(cards[0].props('toggleError')).toBe('schedule update rejected')
    expect(cards[0].props('toggling')).toBe(false)
    expect(cards[1].props('toggleError')).toBeFalsy()
    expect(showError).toHaveBeenCalledWith('schedule update rejected')

    const disabled = { ...item(1), plan: { ...item(1).plan, enabled: false } }
    update.mockResolvedValueOnce(disabled.plan)
    listStatus.mockResolvedValue([disabled, item(2, 'failed'), item(3, null)])
    cards[0].vm.$emit('toggleEnabled', false)
    await flushPromises()
    expect(cards[0].props('item').plan.enabled).toBe(false)
    expect(cards[0].props('toggleError')).toBeFalsy()
  })

  it.each(['pending', 'completed'] as const)('ignores an older status response when a schedule change is %s', async phase => {
    const stale = deferred<DegradationTestStatus[]>()
    const changing = deferred<DegradationTestPlan>()
    const wrapper = await openView()
    const card = wrapper.findAllComponents(IntelligenceStatusCard)[0]
    listStatus.mockReturnValueOnce(stale.promise)
    await wrapper.get('[data-testid="refresh-status"]').trigger('click')
    update.mockReturnValueOnce(changing.promise)
    card.vm.$emit('toggleEnabled', false)
    await flushPromises()

    if (phase === 'pending') {
      stale.resolve([{ ...item(1), plan: { ...item(1).plan, enabled: false } }])
      await flushPromises()
      expect(card.props('item').plan.enabled).toBe(true)
      expect(wrapper.findAllComponents(IntelligenceStatusCard)).toHaveLength(3)
    }

    const disabled = { ...item(1), plan: { ...item(1).plan, enabled: false } }
    listStatus.mockResolvedValue([disabled, item(2, 'failed'), item(3, null)])
    changing.resolve(disabled.plan)
    await flushPromises()
    if (phase === 'completed') {
      stale.resolve([item(1)])
      await flushPromises()
    }
    expect(card.props('item').plan.enabled).toBe(false)
    expect(wrapper.findAllComponents(IntelligenceStatusCard)).toHaveLength(3)
  })

  it('pauses automatic polling during updates and rejects an intervening manual refresh after the save completes', async () => {
    vi.useFakeTimers()
    try {
      const changing = deferred<DegradationTestPlan>()
      const staleRefresh = deferred<DegradationTestStatus[]>()
      const reconciliation = deferred<DegradationTestStatus[]>()
      update.mockReturnValueOnce(changing.promise)
      const wrapper = await openView()
      const card = wrapper.findAllComponents(IntelligenceStatusCard)[0]
      card.vm.$emit('toggleEnabled', false)
      await flushPromises()
      await vi.advanceTimersByTimeAsync(15000)
      expect(listStatus).toHaveBeenCalledTimes(1)

      listStatus.mockReturnValueOnce(staleRefresh.promise).mockReturnValueOnce(reconciliation.promise)
      await wrapper.get('[data-testid="refresh-status"]').trigger('click')
      const disabled = { ...item(1), plan: { ...item(1).plan, enabled: false } }
      changing.resolve(disabled.plan)
      await flushPromises()
      staleRefresh.resolve([item(1)])
      await flushPromises()
      expect(card.props('item').plan.enabled).toBe(false)
      expect(wrapper.findAllComponents(IntelligenceStatusCard)).toHaveLength(3)

      const saved = [disabled, item(2, 'failed'), item(3, null)]
      listStatus.mockResolvedValue(saved)
      reconciliation.resolve(saved)
      await flushPromises()
      expect((wrapper.get('[data-testid="auto-refresh"]').element as HTMLInputElement).checked).toBe(true)
      await vi.advanceTimersByTimeAsync(15000)
      expect(listStatus).toHaveBeenCalledTimes(4)
      expect(card.props('item').plan.enabled).toBe(false)
    } finally { vi.useRealTimers() }
  })

  it('opens the requested plan history and ignores models from a previously selected account', async () => {
    const oldModels = deferred<{ id: string }[]>()
    getAvailableModels.mockReturnValueOnce(oldModels.promise).mockResolvedValueOnce([{ id: 'new-model', display_name: 'New Model' }])
    const wrapper = await openView()
    const cards = wrapper.findAllComponents(IntelligenceStatusCard)
    cards[0].vm.$emit('history')
    cards[1].vm.$emit('history')
    await flushPromises()
    oldModels.resolve([{ id: 'old-model' }])
    await flushPromises()
    const history = wrapper.getComponent(DegradationTestsPanel)
    expect(history.props()).toMatchObject({ show: true, accountId: 12, accountName: 'account-2', initialPlanId: 2, modelOptions: [{ value: 'new-model', label: 'New Model' }] })
    history.vm.$emit('close')
    await flushPromises()
    expect(history.props()).toMatchObject({ show: false, accountId: null, modelOptions: [] })
    expect(listStatus).toHaveBeenCalledTimes(2)
  })

  it('ignores stale status responses after a newer refresh', async () => {
    const old = deferred<DegradationTestStatus[]>()
    const wrapper = await openView()
    listStatus.mockReturnValueOnce(old.promise).mockResolvedValueOnce([item(7, 'failed')])
    await wrapper.get('[data-testid="refresh-status"]').trigger('click')
    run.mockResolvedValueOnce(item(1).latest_result)
    wrapper.findAllComponents(IntelligenceStatusCard)[0].vm.$emit('run')
    await flushPromises()
    old.resolve([item(1)])
    await flushPromises()
    expect(wrapper.findAllComponents(IntelligenceStatusCard).map(card => card.props('item').plan.id)).toEqual([7])
  })

  it('retains existing cards when a refresh fails and shows a retryable error', async () => {
    const wrapper = await openView()
    listStatus.mockRejectedValueOnce(new Error('status fetch failed'))
    await wrapper.get('[data-testid="refresh-status"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toBe('status fetch failed')
    expect(wrapper.findAllComponents(IntelligenceStatusCard)).toHaveLength(3)
  })

  it('polls automatically and stops polling and late UI updates after leaving the page', async () => {
    vi.useFakeTimers()
    try {
      const wrapper = await openView()
      await vi.advanceTimersByTimeAsync(15000)
      expect(listStatus).toHaveBeenCalledTimes(2)
      const pending = deferred<object>()
      run.mockReturnValue(pending.promise)
      wrapper.findAllComponents(IntelligenceStatusCard)[0].vm.$emit('run')
      await flushPromises()
      wrapper.unmount()
      await vi.advanceTimersByTimeAsync(45000)
      pending.resolve({ status: 'success' })
      await flushPromises()
      expect(listStatus).toHaveBeenCalledTimes(2)
      expect(showSuccess).not.toHaveBeenCalled()
      expect(showError).not.toHaveBeenCalled()
    } finally { vi.useRealTimers() }
  })
})

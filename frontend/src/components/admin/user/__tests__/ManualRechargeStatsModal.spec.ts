import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import ManualRechargeStatsModal from '../ManualRechargeStatsModal.vue'

const mocks = vi.hoisted(() => ({ getManualRechargeStats: vi.fn(), deductManualRecharge: vi.fn(), getManualRechargeScreenshot: vi.fn(), showError: vi.fn(), showSuccess: vi.fn() }))
vi.mock('@/api/admin', () => ({ adminAPI: { users: mocks } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => mocks }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

const response = {
  items: [], total: 0, amount: 0, deducted: 0, breakdown: [], deductions: [], deduction_total: 0,
  page: 1, page_size: 20, deduction_page: 1,
  accounts: [{ recipient: '鹏', balance: 1000 }]
}

beforeEach(() => {
  vi.clearAllMocks()
  mocks.getManualRechargeStats.mockResolvedValue(response)
  mocks.deductManualRecharge.mockResolvedValue(undefined)
  vi.stubGlobal('crypto', { randomUUID: () => 'deduction-key' })
})
afterEach(() => vi.unstubAllGlobals())

describe('manual recharge deduction', () => {
  it('deducts from the recipient account with required notes and an optional screenshot', async () => {
    const wrapper = mount(ManualRechargeStatsModal, {
      props: { show: false },
      global: { stubs: { BaseDialog: { template: '<div><slot /></div>' } } }
    })
    await wrapper.setProps({ show: true })
    await flushPromises()
    expect(wrapper.text()).toContain('$1,000.00')
    await wrapper.get('section button').trigger('click')
    await wrapper.get('#deduction-amount').setValue('200')
    await wrapper.get('#deduction-notes').setValue('手续费')
    await wrapper.get('form:has(#deduction-amount)').trigger('submit')
    await flushPromises()
    expect(mocks.deductManualRecharge).toHaveBeenCalledWith({ recipient: '鹏', amount: 200, notes: '手续费', screenshot: null, idempotencyKey: 'deduction-key' })
    expect(mocks.getManualRechargeStats).toHaveBeenCalledTimes(2)
  })
})

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'

import AccountsView from '../AccountsView.vue'
import AccountActionMenu from '@/components/admin/account/AccountActionMenu.vue'
import ScheduledTestsPanel from '@/components/admin/account/ScheduledTestsPanel.vue'
import DegradationTestsPanel from '@/components/admin/account/DegradationTestsPanel.vue'

const {
  listAccounts,
  listWithEtag,
  getBatchTodayStats,
  getAllProxies,
  getAllGroups,
  duplicateAccount,
  createSparkShadow,
  showSuccess,
  showError,
  getAvailableModels
} = vi.hoisted(() => ({
  listAccounts: vi.fn(),
  listWithEtag: vi.fn(),
  getBatchTodayStats: vi.fn(),
  getAllProxies: vi.fn(),
  getAllGroups: vi.fn(),
  duplicateAccount: vi.fn(),
  createSparkShadow: vi.fn(),
  showSuccess: vi.fn(),
  showError: vi.fn(),
  getAvailableModels: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      list: listAccounts,
      getAvailableModels,
      listWithEtag,
      getBatchTodayStats,
      duplicate: duplicateAccount,
      getUpstreamBillingProbeSettings: vi.fn().mockResolvedValue({ enabled: true, interval_minutes: 30 }),
      createSparkShadow,
      delete: vi.fn(),
      batchClearError: vi.fn(),
      batchRefresh: vi.fn(),
      toggleSchedulable: vi.fn()
    },
    proxies: { getAll: getAllProxies },
    groups: { getAll: getAllGroups }
  }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError, showSuccess, showInfo: vi.fn() })
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ token: 'test-token' })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key })
  }
})

const mountView = () =>
  mount(AccountsView, {
    global: {
      stubs: {
        AppLayout: { template: '<div><slot /></div>' },
        TablePageLayout: {
          template: '<div><slot name="filters" /><slot name="table" /><slot name="pagination" /></div>'
        },
        DataTable: true,
        Pagination: true,
        ConfirmDialog: true,
        AccountTableActions: { template: '<div><slot name="beforeCreate" /><slot name="after" /></div>' },
        AccountTableFilters: { template: '<div></div>' },
        AccountBulkActionsBar: true,
        AccountActionMenu: true,
        ImportDataModal: true,
        ReAuthAccountModal: true,
        AccountTestModal: true,
        AccountStatsModal: true,
        ScheduledTestsPanel: true,
        DegradationTestsPanel: true,
        SyncFromCrsModal: true,
        TempUnschedStatusModal: true,
        ErrorPassthroughRulesModal: true,
        TLSFingerprintProfilesModal: true,
        CreateAccountModal: true,
        EditAccountModal: true,
        BulkEditAccountModal: true,
        PlatformTypeBadge: true,
        AccountCapacityCell: true,
        AccountStatusIndicator: true,
        AccountTodayStatsCell: true,
        AccountGroupsCell: true,
        AccountUsageCell: true,
        Icon: true
      }
    }
  })


enableAutoUnmount(afterEach)

describe('account test entry isolation', () => {
  beforeEach(() => {
    localStorage.clear()
    vi.clearAllMocks()
    listAccounts.mockResolvedValue({ items: [], total: 0, page: 1, page_size: 20, pages: 0 })
    listWithEtag.mockResolvedValue({ notModified: true, etag: null, data: null })
    getBatchTodayStats.mockResolvedValue({ stats: {} })
    getAllProxies.mockResolvedValue([])
    getAllGroups.mockResolvedValue([])
    getAvailableModels.mockResolvedValue([])
  })

  it('opens the original scheduled tests and new model quality tests independently', async () => {
    const wrapper = mountView()
    await flushPromises()
    const menu = wrapper.getComponent(AccountActionMenu)
    const scheduled = wrapper.getComponent(ScheduledTestsPanel)
    const degradation = wrapper.getComponent(DegradationTestsPanel)
    menu.vm.$emit('schedule', { id: 1, name: 'Connectivity account' })
    await flushPromises()
    expect(scheduled.props()).toMatchObject({ show: true, accountId: 1 })
    expect(degradation.props('show')).toBe(false)
    menu.vm.$emit('degradation', { id: 2, name: 'Quality account' })
    await flushPromises()
    expect(degradation.props()).toMatchObject({ show: true, accountId: 2, accountName: 'Quality account' })
    expect(scheduled.props()).toMatchObject({ show: true, accountId: 1 })
    degradation.vm.$emit('close')
    await flushPromises()
    expect(degradation.props()).toMatchObject({ show: false, accountId: null })
    expect(scheduled.props()).toMatchObject({ show: true, accountId: 1 })
  })

  it('keeps model options isolated and ignores obsolete model requests', async () => {
    let resolveOld!: (models: { id: string }[]) => void
    getAvailableModels.mockImplementationOnce(() => new Promise(resolve => { resolveOld = resolve }))
      .mockResolvedValueOnce([{ id: 'quality-model', display_name: 'Quality Model' }])
      .mockResolvedValueOnce([{ id: 'health-model', display_name: 'Health Model' }])
    const wrapper = mountView()
    await flushPromises()
    const menu = wrapper.getComponent(AccountActionMenu)
    menu.vm.$emit('degradation', { id: 1, name: 'Old account' })
    await flushPromises()
    menu.vm.$emit('degradation', { id: 2, name: 'New account' })
    menu.vm.$emit('schedule', { id: 3, name: 'Health account' })
    await flushPromises()
    resolveOld([{ id: 'obsolete-model' }])
    await flushPromises()
    expect(wrapper.getComponent(DegradationTestsPanel).props()).toMatchObject({
      accountId: 2, modelOptions: [{ value: 'quality-model', label: 'Quality Model' }]
    })
    expect(wrapper.getComponent(ScheduledTestsPanel).props()).toMatchObject({
      accountId: 3, modelOptions: [{ value: 'health-model', label: 'Health Model' }]
    })
  })
})

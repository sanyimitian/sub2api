import { describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import AccountSwitchRecordsView from '../AccountSwitchRecordsView.vue'

const { listSystemLogs } = vi.hoisted(() => ({ listSystemLogs: vi.fn() }))

vi.mock('@/api/admin/ops', () => ({ opsAPI: { listSystemLogs } }))
vi.mock('@/components/layout/AppLayout.vue', () => ({
  default: { template: '<div><slot /></div>' },
}))
vi.mock('@/stores', () => ({ useAppStore: () => ({ showError: vi.fn() }) }))
vi.mock('vue-i18n', async (importOriginal) => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({ t: (key: string) => key }),
}))

describe('AccountSwitchRecordsView triggering user', () => {
  it('defaults to the most recent hour and requests no wider history by default', async () => {
    listSystemLogs.mockResolvedValue({ items: [], total: 0 })

    const wrapper = mount(AccountSwitchRecordsView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          Pagination: true,
          Select: true,
        },
      },
    })
    await flushPromises()

    expect(listSystemLogs).toHaveBeenCalledWith(expect.objectContaining({ time_range: '1h' }))
    wrapper.unmount()
  })

  it('shows caller identity and handles records with partial or missing user information', async () => {
    const callers = [
      { user_id: 42, extra: { username: 'caller', user_email: 'caller@example.com' } },
      { user_id: 43, extra: { user_email: 'email-only@example.com' } },
      { user_id: 44 },
      { user_id: null },
    ]
    listSystemLogs.mockResolvedValue({
      items: callers.map((caller, index) => ({
        id: index + 1,
        created_at: '2026-10-19T00:00:00Z',
        message: 'openai.ws_latency_switch_completed',
        ...caller,
      })),
      total: callers.length,
    })

    const wrapper = mount(AccountSwitchRecordsView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          Pagination: true,
          Select: true,
        },
      },
    })
    await flushPromises()

    const users = wrapper.findAll('[data-testid="triggering-user"]')
    expect(users).toHaveLength(4)
    expect(users[0]!.text()).toContain('caller')
    expect(users[0]!.text()).toContain('caller@example.com')
    expect(users[0]!.text()).toContain('ID: 42')
    expect(users[1]!.text()).toContain('email-only@example.com')
    expect(users[1]!.text()).toContain('ID: 43')
    expect(users[2]!.text()).toBe('#44')
    expect(users[3]!.text()).toBe('-')
    wrapper.unmount()
  })

  it('groups events from the same conversation, starts collapsed, and shows the latest status', async () => {
    listSystemLogs.mockResolvedValue({
      items: [
        { id: 4, created_at: '2026-10-19T00:04:00Z', message: 'openai.ws_latency_switch_completed', model: 'gpt-5', user_id: 42, extra: { conversation_id: 'opaque-session', target_account_id: 22, username: 'caller' } },
        { id: 3, created_at: '2026-10-19T00:03:00Z', message: 'openai.ws_latency_switch_reconnect_requested', model: 'gpt-5', user_id: 42, extra: { conversation_id: 'opaque-session', target_account_id: 22, username: 'caller' } },
        { id: 2, created_at: '2026-10-19T00:02:00Z', message: 'openai.ws_latency_switch_pending', model: 'gpt-5', user_id: 42, extra: { conversation_id: 'opaque-session', target_account_id: 22, username: 'caller' } },
        { id: 1, created_at: '2026-10-19T00:01:00Z', message: 'openai.ws_latency_switch_probe_completed', model: 'gpt-5', user_id: 42, extra: { conversation_id: 'opaque-session', from_account_id: 11, probe_results: [] } },
      ],
      total: 4,
    })

    const wrapper = mount(AccountSwitchRecordsView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          Pagination: true,
          Select: true,
        },
      },
    })
    await flushPromises()

    expect(wrapper.findAll('[data-testid="triggering-user"]')).toHaveLength(1)
    expect(wrapper.text()).toContain('admin.ops.switchRecords.completed')
    expect(wrapper.findAll('tbody > tr')).toHaveLength(1)
    await wrapper.get('[data-testid="toggle-switch-group"]').trigger('click')
    expect(wrapper.findAll('tbody > tr')).toHaveLength(2)
    expect(wrapper.text()).toContain('admin.ops.switchRecords.pending')
    expect(wrapper.text()).toContain('admin.ops.switchRecords.reconnectRequested')
    wrapper.unmount()
  })
})

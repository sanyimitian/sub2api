import { beforeEach, describe, expect, it, vi } from 'vitest'
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

describe('AccountSwitchRecordsView', () => {
  beforeEach(() => { listSystemLogs.mockReset() })

  it('shows claim lifecycle and probe latency without including background diagnostics', async () => {
    listSystemLogs.mockImplementation(({ q }) => Promise.resolve(q === 'openai.session_pool_' ? {
      items: [
        { id: 3, created_at: '2026-10-19T00:03:00Z', message: 'openai.session_pool_assignment_completed', model: 'gpt-5', user_id: 42, extra: { conversation_id: 'claim-1', target_account_id: 22, username: 'claim-user' } },
        { id: 2, created_at: '2026-10-19T00:02:00Z', message: 'openai.session_pool_assigned', model: 'gpt-5', user_id: 42, extra: { conversation_id: 'claim-1', target_account_id: 22, probe_first_token_ms: 123 } },
        { id: 1, created_at: '2026-10-19T00:01:00Z', message: 'openai.session_pool_probe_completed' },
      ], total: 3,
    } : { items: [], total: 0 }))
    const wrapper = mount(AccountSwitchRecordsView, { global: { stubs: { Pagination: true, Select: true } } })
    await flushPromises()
    await wrapper.get('[data-testid="toggle-claim-section"]').trigger('click')
    expect(wrapper.findAll('[data-testid="triggering-user"]')).toHaveLength(1)
    expect(wrapper.text()).toContain('claim-user')
    expect(wrapper.text()).toContain('123 ms')
    expect(wrapper.text()).toContain('admin.ops.switchRecords.claimed')
    expect(wrapper.text()).not.toContain('admin.ops.switchRecords.assigned')
    await wrapper.get('[data-testid="toggle-switch-group"]').trigger('click')
    expect(wrapper.text()).toContain('admin.ops.switchRecords.assigned')
    expect(wrapper.findAll('tbody > tr')).toHaveLength(2)
    expect(wrapper.get('[data-testid="toggle-latency-section"]').attributes('aria-expanded')).toBe('false')
    wrapper.unmount()
  })

  it('shows the newest 20 conversations per section and paginates independently', async () => {
    listSystemLogs.mockImplementation(({ q }) => Promise.resolve({
      items: Array.from({ length: 25 }, (_, index) => ({
        id: index + 1, created_at: new Date(Date.UTC(2026, 9, 19, 0, index)).toISOString(),
        message: q === 'openai.session_pool_' ? 'openai.session_pool_assigned' : 'openai.ws_latency_switch_completed',
        extra: { conversation_id: `conversation-${index}`, username: `caller-${index}` },
      })), total: 25,
    }))
    const wrapper = mount(AccountSwitchRecordsView, { global: { stubs: { Pagination: true, Select: true } } })
    await flushPromises()
    await wrapper.get('[data-testid="toggle-latency-section"]').trigger('click')
    await wrapper.get('[data-testid="toggle-claim-section"]').trigger('click')
    const tables = wrapper.findAll('table')
    for (const table of tables) {
      expect(table.findAll('tbody > tr')).toHaveLength(20)
      expect(table.findAll('[data-testid="triggering-user"]')[0]!.text()).toBe('caller-24')
    }
    await tables[0]!.findAll('[data-testid="toggle-switch-group"]')[0]!.trigger('click')
    expect(tables[0]!.findAll('tbody > tr')).toHaveLength(21)
    expect(tables[1]!.findAll('tbody > tr')).toHaveLength(20)
    await tables[0]!.findAll('[data-testid="toggle-switch-group"]')[0]!.trigger('click')
    const pagination = wrapper.findAllComponents({ name: 'Pagination' })
    pagination[0]!.vm.$emit('update:page', 2)
    await wrapper.vm.$nextTick()
    expect(tables[0]!.findAll('tbody > tr')).toHaveLength(5)
    expect(tables[1]!.findAll('tbody > tr')).toHaveLength(20)
    wrapper.unmount()
  })

  it('applies time and model filters to both categories', async () => {
    listSystemLogs.mockResolvedValue({ items: [], total: 0 })
    const wrapper = mount(AccountSwitchRecordsView, { global: { stubs: { Pagination: true, Select: true } } })
    await flushPromises()
    wrapper.getComponent({ name: 'Select' }).vm.$emit('update:modelValue', '24h')
    await wrapper.get('input').setValue(' gpt-5 ')
    await wrapper.get('input').trigger('keyup.enter')
    await flushPromises()
    expect(listSystemLogs).toHaveBeenLastCalledWith(expect.objectContaining({ time_range: '24h', model: 'gpt-5', q: 'openai.session_pool_' }))
    expect(listSystemLogs).toHaveBeenCalledWith(expect.objectContaining({ time_range: '24h', model: 'gpt-5', q: 'openai.ws_latency_switch_' }))
    wrapper.unmount()
  })
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

    expect(listSystemLogs).toHaveBeenCalledTimes(2)
    expect(listSystemLogs).toHaveBeenCalledWith(expect.objectContaining({ time_range: '1h', q: 'openai.ws_latency_switch_' }))
    expect(listSystemLogs).toHaveBeenCalledWith(expect.objectContaining({ time_range: '1h', q: 'openai.session_pool_' }))
    expect(wrapper.get('[data-testid="toggle-latency-section"]').attributes('aria-expanded')).toBe('false')
    expect(wrapper.get('[data-testid="toggle-claim-section"]').attributes('aria-expanded')).toBe('false')
    expect(wrapper.find('table').exists()).toBe(false)
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

    await wrapper.get('[data-testid="toggle-latency-section"]').trigger('click')
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

    await wrapper.get('[data-testid="toggle-latency-section"]').trigger('click')
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

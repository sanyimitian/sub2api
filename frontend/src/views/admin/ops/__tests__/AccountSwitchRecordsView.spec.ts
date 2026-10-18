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
})

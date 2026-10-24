import { afterEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, mount } from '@vue/test-utils'
import IntelligenceStatusCard from '../IntelligenceStatusCard.vue'
import type { DegradationTestStatus } from '@/types'

vi.mock('vue-i18n', async importOriginal => ({ ...await importOriginal<typeof import('vue-i18n')>(), useI18n: () => ({ t: (key: string) => key }) }))
enableAutoUnmount(afterEach)

const html = '<html><body><svg><circle r="8" /></svg><script>window.animate()</script></body></html>'
const statusItem = (): DegradationTestStatus => ({
  plan: { id: 1, account_id: 3, model_id: 'current-model', reasoning_effort: 'high', enabled: false, cron_expression: '@every 30m' },
  account_name: 'Account A', account_platform: 'openai',
  latest_result: { id: 7, model_id: 'tested-model', reasoning_effort: 'xhigh', status: 'success', response_text: html, started_at: '2026-10-25T00:00:00Z', latency_ms: 123 }
} as DegradationTestStatus)

describe('intelligence status cards', () => {
  it('shows execution snapshots and a lazy sandboxed preview that remains stable across polling', async () => {
    const item = statusItem()
    const wrapper = mount(IntelligenceStatusCard, { props: { item } })
    expect(wrapper.get('[data-testid="tested-model"]').text()).toBe('tested-model')
    expect(wrapper.text()).toContain('current-model')
    expect(wrapper.text()).toContain('admin.degradationTests.effort.xhigh')
    const iframe = wrapper.get('iframe')
    const element = iframe.element
    expect(iframe.attributes()).toMatchObject({ sandbox: 'allow-scripts', referrerpolicy: 'no-referrer', loading: 'lazy', tabindex: '-1' })
    const document = iframe.attributes('srcdoc')
    expect(document).toContain('Content-Security-Policy')
    expect(document).toContain("connect-src 'none'")
    expect(wrapper.find('svg circle, script').exists()).toBe(false)
    await wrapper.setProps({ item: structuredClone(item) })
    expect(wrapper.get('iframe').element).toBe(element)
    expect(wrapper.get('iframe').attributes('srcdoc')).toBe(document)
  })

  it('shows the latest failure and never renders HTML from its partial response', () => {
    const item = statusItem()
    item.latest_result!.status = 'failed'
    item.latest_result!.error_message = 'upstream quota exhausted'
    const wrapper = mount(IntelligenceStatusCard, { props: { item } })
    expect(wrapper.get('[data-testid="latest-status"]').text()).toBe('admin.intelligenceStatus.failed')
    expect(wrapper.get('[data-testid="latest-error"]').text()).toBe('upstream quota exhausted')
    expect(wrapper.find('iframe').exists()).toBe(false)
  })

  it('does not relabel an older result with the plan reasoning effort', () => {
    const item = statusItem()
    delete item.latest_result!.reasoning_effort
    const wrapper = mount(IntelligenceStatusCard, { props: { item } })
    expect(wrapper.text()).toContain('admin.degradationTests.effort.default')
    expect(wrapper.text()).not.toContain('admin.degradationTests.effort.high')
  })

  it('keeps untested disabled plans visible and allows a manual run or history', async () => {
    const item = statusItem()
    item.latest_result = null
    const wrapper = mount(IntelligenceStatusCard, { props: { item } })
    expect(wrapper.text()).toContain('admin.intelligenceStatus.notTested')
    expect(wrapper.text()).toContain('admin.intelligenceStatus.disabled')
    expect(wrapper.get('[data-testid="run-status-1"]').attributes('disabled')).toBeUndefined()
    await wrapper.get('[data-testid="run-status-1"]').trigger('click')
    await wrapper.get('[data-testid="history-status-1"]').trigger('click')
    expect(wrapper.emitted('run')).toHaveLength(1)
    expect(wrapper.emitted('history')).toHaveLength(1)
  })

  it('exposes an accessible schedule switch and emits both requested states', async () => {
    const item = statusItem()
    const wrapper = mount(IntelligenceStatusCard, { props: { item } })
    const toggle = wrapper.get('[data-testid="toggle-schedule-1"]')
    expect(toggle.attributes('role')).toBe('switch')
    expect(toggle.attributes('aria-checked')).toBe('false')
    expect(toggle.attributes('aria-label')).toBeTruthy()
    await toggle.trigger('click')
    expect(wrapper.emitted('toggleEnabled')).toEqual([[true]])
    expect(toggle.attributes('aria-checked')).toBe('false')

    await wrapper.setProps({ item: { ...item, plan: { ...item.plan, enabled: true } } })
    expect(toggle.attributes('aria-checked')).toBe('true')
    await toggle.trigger('click')
    expect(wrapper.emitted('toggleEnabled')).toEqual([[true], [false]])
  })

  it('blocks the schedule switch while saving, displays failures, and keeps manual execution available', async () => {
    const wrapper = mount(IntelligenceStatusCard, { props: { item: statusItem(), toggling: true } })
    const toggle = wrapper.get('[data-testid="toggle-schedule-1"]')
    expect(toggle.attributes('disabled')).toBeDefined()
    await toggle.trigger('click')
    expect(wrapper.emitted('toggleEnabled')).toBeUndefined()
    expect(wrapper.get('[data-testid="run-status-1"]').attributes('disabled')).toBeUndefined()
    await wrapper.get('[data-testid="run-status-1"]').trigger('click')
    expect(wrapper.emitted('run')).toHaveLength(1)

    await wrapper.setProps({ toggling: false, toggleError: 'schedule update rejected' })
    expect(toggle.attributes('disabled')).toBeUndefined()
    expect(toggle.attributes('aria-checked')).toBe('false')
    expect(wrapper.findAll('[role="alert"]').some(alert => alert.text() === 'schedule update rejected')).toBe(true)
  })
})

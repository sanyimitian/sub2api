import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import OpenAISessionPoolRecords from '../OpenAISessionPoolRecords.vue'

const { listSystemLogs, getSnapshot } = vi.hoisted(() => ({ listSystemLogs: vi.fn(), getSnapshot: vi.fn() }))
vi.mock('@/api/admin/ops', () => ({ opsAPI: { listSystemLogs, getOpenAISessionPoolSnapshot: getSnapshot } }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key, te: () => true }) }))

const currentTime = new Date('2026-10-21T12:00:00Z')
const candidate = {
  result_id: 'candidate-1', account_id: 11, account_name: '账号甲', model: 'gpt-6-sol',
  transport: 'http_sse', first_token_ms: 125, expires_at: '2026-10-21T12:01:00Z'
}
const probeGroups = Array.from({ length: 12 }, (_, index) => ({
  id: index + 1, created_at: new Date(currentTime.getTime() - index * 10000).toISOString(),
  message: 'openai.session_pool_refill_completed',
  extra: { reason: 'target_reached', probe_count: 1, published_count: 1, probe_results: [{
    round: 1, probe_number: 1, account_id: 11, account_name: '账号甲', model: 'gpt-6-sol',
    transport: 'http_sse', first_token_ms: 125, success: true, qualified: true, published: true, reason: 'qualified'
  }] }
}))
const createWrapper = () => mount(OpenAISessionPoolRecords, { global: { stubs: { Pagination: true } } })

beforeEach(() => {
  vi.useFakeTimers()
  vi.setSystemTime(currentTime)
  getSnapshot.mockResolvedValue({ enabled: true, target_size: 6, observed_at: currentTime.toISOString(), candidates: [candidate] })
  listSystemLogs.mockImplementation(({ page, page_size }) => Promise.resolve({ items: probeGroups.slice((page - 1) * page_size, page * page_size), total: probeGroups.length }))
})
afterEach(() => { vi.useRealTimers(); vi.clearAllMocks() })

describe('备用会话池与探测记录', () => {
  it('默认展示会话池及过期时间，最近一组展开为十组并支持翻页', async () => {
    const wrapper = createWrapper()
    await wrapper.vm.refresh({ time_range: '24h', model: 'gpt-6-sol' })
    expect(wrapper.findAll('[data-testid="pool-candidate"]')).toHaveLength(1)
    expect(wrapper.get('[data-testid="pool-candidate"] time').attributes('datetime')).toBe(candidate.expires_at)
    expect(wrapper.findAll('[data-testid="pool-probe-group"]')).toHaveLength(1)
    expect(listSystemLogs).toHaveBeenLastCalledWith(expect.objectContaining({ page_size: 1, time_range: '24h', model: 'gpt-6-sol' }))
    await wrapper.get('[data-testid="toggle-pool-probes"]').trigger('click')
    await flushPromises()
    expect(wrapper.findAll('[data-testid="pool-probe-group"]')).toHaveLength(10)
    const pagination = wrapper.findComponent({ name: 'Pagination' })
    expect(pagination.props('pageSize')).toBe(10)
    pagination.vm.$emit('update:page', 2)
    await flushPromises()
    expect(wrapper.findAll('[data-testid="pool-probe-group"]')).toHaveLength(2)
    await wrapper.get('[data-testid="toggle-pool-probes"]').trigger('click')
    await flushPromises()
    expect(wrapper.findAll('[data-testid="pool-probe-group"]')).toHaveLength(1)
    wrapper.unmount()
  })

  it('即使浏览器时钟偏差也按服务器过期时间移除会话，并定时刷新', async () => {
    vi.setSystemTime(new Date('2026-10-22T12:00:00Z'))
    const wrapper = createWrapper()
    await wrapper.vm.refresh()
    expect(wrapper.findAll('[data-testid="pool-candidate"]')).toHaveLength(1)
    getSnapshot.mockImplementation(() => new Promise(() => {}))
    await vi.advanceTimersByTimeAsync(61000)
    expect(wrapper.findAll('[data-testid="pool-candidate"]')).toHaveLength(0)
    expect(getSnapshot).toHaveBeenCalledTimes(2)
    wrapper.unmount()
  })

  it('缓存读取失败展示错误，不冒充空池；记录仍可正常查看', async () => {
    getSnapshot.mockRejectedValue(new Error('cache unavailable'))
    const wrapper = createWrapper()
    await wrapper.vm.refresh()
    expect(wrapper.get('[role="alert"]').text()).toBe('admin.ops.switchRecords.pool.loadFailed')
    expect(wrapper.findAll('[data-testid="pool-probe-group"]')).toHaveLength(1)
    wrapper.unmount()
  })

  it('刷新失败后可重试恢复，并在卸载时停止请求', async () => {
    listSystemLogs.mockRejectedValueOnce(new Error('temporary'))
    const wrapper = createWrapper()
    await wrapper.vm.refresh()
    expect(wrapper.text()).toContain('admin.ops.switchRecords.pool.probesLoadFailed')
    await wrapper.vm.refresh()
    expect(wrapper.findAll('[data-testid="pool-probe-group"]')).toHaveLength(1)
    const calls = getSnapshot.mock.calls.length
    wrapper.unmount()
    await vi.advanceTimersByTimeAsync(30000)
    expect(getSnapshot).toHaveBeenCalledTimes(calls)
  })
})

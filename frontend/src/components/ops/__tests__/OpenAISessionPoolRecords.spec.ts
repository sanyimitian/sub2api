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
  it('会话池默认折叠，展开后展示过期时间；探测记录支持展开与翻页', async () => {
    const wrapper = createWrapper()
    await wrapper.vm.refresh({ time_range: '24h', model: 'gpt-6-sol' })
    expect(wrapper.get('[data-testid="toggle-session-pool"]').attributes('aria-expanded')).toBe('false')
    expect(wrapper.findAll('[data-testid="pool-candidate"]')).toHaveLength(0)
    await wrapper.get('[data-testid="toggle-session-pool"]').trigger('click')
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
    await wrapper.get('[data-testid="toggle-session-pool"]').trigger('click')
    expect(wrapper.findAll('[data-testid="pool-candidate"]')).toHaveLength(1)
    getSnapshot.mockImplementation(() => new Promise(() => {}))
    await vi.advanceTimersByTimeAsync(61000)
    expect(wrapper.findAll('[data-testid="pool-candidate"]')).toHaveLength(0)
    expect(getSnapshot).toHaveBeenCalledTimes(2)
    wrapper.unmount()
  })

  it('探测历史显示降智判定和可读的失败原因', async () => {
    const group = probeGroups[0]
    listSystemLogs.mockResolvedValueOnce({ items: [{ ...group, extra: { ...group.extra, probe_results: [{
      ...group.extra.probe_results[0], published: false, qualified: false, success: false, reason: 'quality_inconclusive',
      quality_judgment: { verdict: 'unknown', reason: 'judge_request_failed' }
    }] } }], total: 1 })
    const wrapper = createWrapper()
    await wrapper.vm.refresh()
    const judgment = wrapper.get('[data-testid="pool-quality-judgment"]')
    expect(judgment.text()).toContain('admin.accounts.sessionPool.verdicts.unknown')
    expect(judgment.text()).toContain('admin.accounts.sessionPool.judgeReasons.judge_request_failed')
    wrapper.unmount()
  })

  it('按候选编号同步当前可领取状态，并明确本次只测了延迟', async () => {
    const group = probeGroups[0]
    listSystemLogs.mockResolvedValue({ items: [{ ...group, extra: { ...group.extra, probe_results: [{
      ...group.extra.probe_results[0], result_id: candidate.result_id, quality_probe_enabled: false
    }] } }], total: 1 })
    const wrapper = createWrapper()
    await wrapper.vm.refresh()
    expect(wrapper.get('[data-testid="pool-publication-status"]').text()).toBe('admin.ops.switchRecords.pool.availableNow')
    expect(wrapper.get('[data-testid="pool-probe-mode"]').text()).toBe('admin.ops.switchRecords.pool.latencyOnly')
    expect(wrapper.find('[data-testid="pool-observed-at"]').exists()).toBe(true)

    getSnapshot.mockResolvedValue({ enabled: true, target_size: 6, observed_at: currentTime.toISOString(), candidates: [] })
    await wrapper.vm.refresh()
    expect(wrapper.get('[data-testid="pool-publication-status"]').text()).toBe('admin.ops.switchRecords.pool.publishedUnavailable')

    getSnapshot.mockRejectedValue(new Error('cache unavailable'))
    await wrapper.vm.refresh()
    expect(wrapper.get('[data-testid="pool-publication-status"]').text()).toBe('admin.ops.switchRecords.pool.publishedUnknown')
    wrapper.unmount()
  })

  it('旧记录不按账号模型猜测当前状态，缺少判题信息时不宣称已通过', async () => {
    const wrapper = createWrapper()
    await wrapper.vm.refresh()
    expect(wrapper.get('[data-testid="pool-publication-status"]').text()).toBe('admin.ops.switchRecords.pool.published')
    expect(wrapper.get('[data-testid="pool-probe-mode"]').text()).toBe('admin.ops.switchRecords.pool.noQualityResult')
    wrapper.unmount()
  })

  it('历史记录比池快照新时等待同步，不提前断言会话不可领取', async () => {
    const group = probeGroups[0]
    listSystemLogs.mockResolvedValue({ items: [{ ...group,
      created_at: new Date(currentTime.getTime() + 1000).toISOString(),
      extra: { ...group.extra, probe_results: [{ ...group.extra.probe_results[0], result_id: 'new-candidate' }] }
    }], total: 1 })
    const wrapper = createWrapper()
    await wrapper.vm.refresh()
    expect(wrapper.get('[data-testid="pool-publication-status"]').text()).toBe('admin.ops.switchRecords.pool.publishedUnknown')
    getSnapshot.mockResolvedValue({ enabled: true, target_size: 6,
      observed_at: new Date(currentTime.getTime() + 2000).toISOString(), candidates: [] })
    await wrapper.vm.refresh()
    expect(wrapper.get('[data-testid="pool-publication-status"]').text()).toBe('admin.ops.switchRecords.pool.publishedUnavailable')
    wrapper.unmount()
  })

  it('按模型筛选时只展示匹配的探测，不混入同批其他模型的结果', async () => {
    const group = probeGroups[0]
    listSystemLogs.mockResolvedValue({ items: [{ ...group, extra: { ...group.extra, probe_results: [
      group.extra.probe_results[0], { ...group.extra.probe_results[0], model: 'other-model', probe_number: 2 }
    ] } }], total: 1 })
    const wrapper = createWrapper()
    await wrapper.vm.refresh({ time_range: '1h', model: 'gpt-6-sol' })
    expect(wrapper.findAll('[data-testid="pool-publication-status"]')).toHaveLength(1)
    expect(wrapper.text()).not.toContain('other-model')
    wrapper.unmount()
  })

  it('历史请求未返回时，会话池仍独立刷新', async () => {
    const wrapper = createWrapper()
    await wrapper.vm.refresh()
    listSystemLogs.mockImplementation(() => new Promise(() => {}))
    await vi.advanceTimersByTimeAsync(30000)
    expect(getSnapshot).toHaveBeenCalledTimes(3)
    expect(listSystemLogs).toHaveBeenCalledTimes(2)
    wrapper.unmount()
  })

  it('缓存读取失败展示错误，不冒充空池；记录仍可正常查看', async () => {
    getSnapshot.mockRejectedValue(new Error('cache unavailable'))
    const wrapper = createWrapper()
    await wrapper.vm.refresh()
    await wrapper.get('[data-testid="toggle-session-pool"]').trigger('click')
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

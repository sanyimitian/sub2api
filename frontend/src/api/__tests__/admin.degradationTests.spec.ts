import { beforeEach, describe, expect, it, vi } from 'vitest'
import degradationTestsAPI from '../admin/degradationTests'
import scheduledTestsAPI from '../admin/scheduledTests'

const { get, post, put, deleteRequest } = vi.hoisted(() => ({
  get: vi.fn(), post: vi.fn(), put: vi.fn(), deleteRequest: vi.fn()
}))
vi.mock('../client', () => ({ apiClient: { get, post, put, delete: deleteRequest } }))

describe('independent model quality test API', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    for (const request of [get, post, put, deleteRequest]) request.mockResolvedValue({ data: [] })
  })

  it('uses separate account plan and result endpoints from connectivity tests', async () => {
    await scheduledTestsAPI.listByAccount(7)
    expect(get).toHaveBeenLastCalledWith('/admin/accounts/7/scheduled-test-plans')
    await degradationTestsAPI.listByAccount(7)
    expect(get).toHaveBeenLastCalledWith('/admin/accounts/7/degradation-test-plans')
    await degradationTestsAPI.listResults(1, 10)
    expect(get).toHaveBeenLastCalledWith('/admin/degradation-test-plans/1/results', { params: { limit: 10 } })
    await degradationTestsAPI.listResults(1, 24)
    expect(get).toHaveBeenLastCalledWith('/admin/degradation-test-plans/1/results', { params: { limit: 24 } })
  })

  it('saves and executes only generation plans through the new API', async () => {
    const request = { account_id: 7, model_id: 'selected-model', reasoning_effort: 'xhigh' as const, prompt: 'Draw SVG', cron_expression: '@every 2h', max_results: 24 }
    await degradationTestsAPI.create(request)
    expect(post).toHaveBeenLastCalledWith('/admin/degradation-test-plans', request)
    await degradationTestsAPI.update(1, { enabled: false })
    expect(put).toHaveBeenLastCalledWith('/admin/degradation-test-plans/1', { enabled: false })
    await degradationTestsAPI.update(1, { reasoning_effort: '' })
    expect(put).toHaveBeenLastCalledWith('/admin/degradation-test-plans/1', { reasoning_effort: '' })
    await degradationTestsAPI.run(1)
    expect(post).toHaveBeenLastCalledWith('/admin/degradation-test-plans/1/run', undefined, { timeout: 310000 })
    await degradationTestsAPI.delete(1)
    expect(deleteRequest).toHaveBeenLastCalledWith('/admin/degradation-test-plans/1')
    expect(scheduledTestsAPI).not.toHaveProperty('run')
  })
})

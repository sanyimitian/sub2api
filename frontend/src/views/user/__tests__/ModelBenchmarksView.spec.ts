import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import ModelBenchmarksView from '../ModelBenchmarksView.vue'
import { userBenchmarks } from '@/api/model-benchmarks'
import { benchmarks, type BenchmarkRun, type BenchmarkState } from '@/api/admin/model-benchmarks'

const app = vi.hoisted(() => ({ showError: vi.fn(), showSuccess: vi.fn(), apiBaseUrl: 'https://gateway.example.com' }))
vi.mock('@/stores/app', () => ({ useAppStore: () => app }))
vi.mock('vue-i18n', async importOriginal => ({ ...await importOriginal<typeof import('vue-i18n')>(), useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/api/model-benchmarks', () => ({ userBenchmarks: { state: vi.fn(), showcase: vi.fn(), list: vi.fn(), create: vi.fn(), detail: vi.fn(), rerun: vi.fn(), cancel: vi.fn() } }))
vi.mock('@/api/admin/model-benchmarks', () => ({ benchmarks: { state: vi.fn(), list: vi.fn(), schedules: vi.fn(), configure: vi.fn(), createSchedule: vi.fn() } }))
const initial: BenchmarkState = {
  execution: { user_testing_enabled: true, max_concurrency: 5, running: 0, queued: 0 },
  ready: true, message: '', timezone: 'Asia/Shanghai', counts: { coding: 20, math: 5, reasoning: 5 }, capacities: { coding: 132, math: 29, reasoning: 100 },
  suite: { id: 'today', day: '2026-11-11', revision: 1, fingerprint: 'a'.repeat(64), bank_version: 'v1', total: 30, counts: { coding: 20, math: 5, reasoning: 5 }, questions: [] },
  quota: { limit: 3, used: 0, remaining: 3, next_available_at: null }
}
const run: BenchmarkRun = {
  id: 'own-run', suite_id: 'today', suite_day: '2026-11-11', suite_revision: 1, suite_fingerprint: 'a'.repeat(64), rerun_of: null,
  base_url: 'https://own.example.com/v1', model: 'test-model', effort: 'medium', status: 'completed', created_at: '2026-11-11T01:00:00Z',
  total: 30, completed: 30, passed: 27, pass_rate: 90, elapsed_seconds: 60, error: '', api_errors: 0, grading_errors: 0,
  categories: { coding: { total: 20, completed: 20, passed: 18 }, math: { total: 5, completed: 5, passed: 5 }, reasoning: { total: 5, completed: 5, passed: 4 } },
  usage: { input_tokens: 1, output_tokens: 2, total_tokens: 3 }, results: []
}
function render() { return mount(ModelBenchmarksView, { global: { stubs: { AppLayout: { template: '<main><slot /></main>' }, IdentificationTabs: true, BenchmarkKeyPicker: true, BaseDialog: true, ConfirmDialog: true } } }) }
enableAutoUnmount(afterEach)
beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(userBenchmarks.state).mockResolvedValue(structuredClone(initial))
  vi.mocked(userBenchmarks.list).mockResolvedValue({ items: [structuredClone(run)], total: 1, page_size: 20 })
  vi.mocked(userBenchmarks.showcase).mockResolvedValue({ items: [{ id: 'display', name: 'Configured source', source_type: 'local', last_success_at: null, stale: false, pending: false, items: [{ id: 'public-plan', name: 'Shared plan', model: 'test-model', effort: 'medium', results: [{ ...run, id: 'shared-new' }, { ...run, id: 'shared-old' }] }] }] })
  vi.mocked(userBenchmarks.detail).mockResolvedValue(structuredClone(run))
})

describe('user model benchmarks', () => {
  it.each([[59.9, 'text-red-600'], [60, 'text-yellow-600'], [70, 'text-emerald-600']] as const)('colors personal history at %s and never shows administrator tabs', async (score, color) => {
    vi.mocked(userBenchmarks.list).mockResolvedValue({ items: [{ ...run, pass_rate: score }], total: 1, page_size: 20 })
    const wrapper = render(); await flushPromises()
    expect(wrapper.get('[data-testid="pass-rate"]').classes()).toContain(color)
    expect(wrapper.find('[role="tablist"]').exists()).toBe(false)
  })
  it('shows resource protection while preserving results and blocking create and retest', async () => {
    vi.mocked(userBenchmarks.state).mockResolvedValue({ ...initial, resources: { blocked: true, status: 'blocked', message: 'Waiting for resources' } })
    const wrapper = render(); await flushPromises()
    expect(wrapper.get('[data-testid="resource-status"]').text()).toContain('Waiting for resources')
    expect(wrapper.find('[data-testid="environment-status"]').exists()).toBe(false)
    expect(wrapper.findAll('[data-shared-result]')).toHaveLength(2)
    await wrapper.get('[name="base_url"]').setValue('https://example.com/v1')
    await wrapper.get('[name="api_key"]').setValue('test-key')
    await wrapper.get('[name="model"]').setValue('fixture')
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeDefined()
    expect(wrapper.findAll('button').find(b => b.text() === 'modelBenchmark.rerun')!.attributes('disabled')).toBeDefined()
    expect(userBenchmarks.create).not.toHaveBeenCalled()
    expect(userBenchmarks.rerun).not.toHaveBeenCalled()
  })
  it.each([false, undefined])('hides My tests unless the administrator explicitly enables it (%s)', async enabled => {
    vi.mocked(userBenchmarks.state).mockResolvedValue({ ...initial, execution: { ...initial.execution!, user_testing_enabled: enabled } })
    const wrapper = render()
    expect(wrapper.find('[data-testid="my-tests-section"]').exists()).toBe(false)
    await flushPromises()
    expect(wrapper.find('[data-testid="my-tests-section"]').exists()).toBe(false)
    expect(wrapper.find('form').exists()).toBe(false)
    expect(wrapper.findAll('[data-shared-result]')).toHaveLength(2)
    expect(userBenchmarks.list).not.toHaveBeenCalled()
    expect(userBenchmarks.detail).not.toHaveBeenCalled()
  })

  it('hides the form and detail when disabled, then restores history when reenabled', async () => {
    const wrapper = render(); await flushPromises()
    await wrapper.findAll('button').find(b => b.text() === 'modelBenchmark.details')!.trigger('click')
    await flushPromises()
    expect(wrapper.find('section[aria-label="modelBenchmark.questionResults"]').exists()).toBe(true)
    vi.mocked(userBenchmarks.state).mockResolvedValue({ ...initial, execution: { ...initial.execution!, user_testing_enabled: false } })
    await wrapper.findAll('button').find(b => b.text() === 'modelBenchmark.refreshResults')!.trigger('click'); await flushPromises()
    expect(wrapper.find('[data-testid="my-tests-section"]').exists()).toBe(false)
    expect(wrapper.find('section[aria-label="modelBenchmark.questionResults"]').exists()).toBe(false)
    expect(wrapper.findAll('[data-shared-result]')).toHaveLength(2)
    vi.mocked(userBenchmarks.state).mockResolvedValue(initial)
    await wrapper.findAll('button').find(b => b.text() === 'modelBenchmark.refreshResults')!.trigger('click'); await flushPromises()
    expect(wrapper.find('[data-testid="my-tests-section"]').exists()).toBe(true)
    expect(wrapper.find('[data-run-id="own-run"]').exists()).toBe(true)
  })
  it('loads selected history pages and accepts the server page after retention cleanup', async () => {
    vi.mocked(userBenchmarks.list).mockResolvedValue({ items: [run], total: 41, page: 1, page_size: 20 })
    const wrapper = render(); await flushPromises()
    const pagination = wrapper.findComponent({ name: 'Pagination' })
    expect(pagination.props('pageSize')).toBe(20)
    expect(pagination.props('total')).toBe(41)
    vi.mocked(userBenchmarks.list).mockResolvedValue({ items: [{ ...run, id: 'page-two' }], total: 41, page: 2, page_size: 20 })
    pagination.vm.$emit('update:page', 2); await flushPromises()
    expect(userBenchmarks.list).toHaveBeenLastCalledWith(2, expect.any(AbortSignal))
    expect(wrapper.find('[data-run-id="page-two"]').exists()).toBe(true)
    vi.mocked(userBenchmarks.list).mockResolvedValue({ items: [run], total: 1, page: 1, page_size: 20 })
    await wrapper.findAll('button').find(b => b.text() === 'modelBenchmark.refreshResults')!.trigger('click'); await flushPromises()
    expect(pagination.props('page')).toBe(1)
  })
  it('shows setup progress without giving users installation controls', async () => {
    vi.mocked(userBenchmarks.state).mockResolvedValue({ ...initial, ready: false, suite: null, message: 'Preparing sandbox', environment: { status: 'failed', stage: 'image', progress: 70, attempt: 1, message: 'Preparing sandbox', next_retry_at: null } })
    const wrapper = render(); await flushPromises()
    expect(wrapper.get('[data-testid="environment-status"]').text()).toContain('Preparing sandbox')
    expect(wrapper.find('[data-testid="environment-retry"]').exists()).toBe(false)
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeDefined()
  })
  it.each(['queued', 'running', 'cancelling'] as const)('blocks create and rerun for an existing %s test outside the current history page', async status => {
    vi.mocked(userBenchmarks.state).mockResolvedValue({ ...initial, active_run: { id: 'active-on-other-page', status } })
    const wrapper = render()
    await flushPromises()
    await wrapper.get('[name="base_url"]').setValue('https://own.example.com/v1')
    await wrapper.get('[name="api_key"]').setValue('user-secret')
    await wrapper.get('[name="model"]').setValue('test-model')
    expect(wrapper.get('[data-testid="active-test-notice"]').text()).toContain('modelBenchmark.activeTestHint')
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeDefined()
    expect(wrapper.findAll('button').find(b => b.text() === 'modelBenchmark.rerun')!.attributes('disabled')).toBeDefined()
    await wrapper.get('form').trigger('submit')
    expect(userBenchmarks.create).not.toHaveBeenCalled()
    vi.mocked(userBenchmarks.state).mockResolvedValue({ ...initial, active_run: null })
    await wrapper.findAll('button').find(b => b.text() === 'modelBenchmark.refreshResults')!.trigger('click')
    await flushPromises()
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeUndefined()
  })
  it('shows configured external sources with a stale marker and last update time', async () => {
    vi.mocked(userBenchmarks.showcase).mockResolvedValue({ items: [{ id: 'external', name: 'External scores', source_type: 'remote', last_success_at: '2026-11-11T00:00:00Z', stale: true, pending: false, items: [] }] })
    const wrapper = render()
    await flushPromises()
    expect(wrapper.get('[data-display-id="external"]').text()).toContain('modelBenchmark.sourceStale')
    expect(wrapper.text()).toContain('modelBenchmark.lastFetch')
    expect(wrapper.find('[name="max_concurrency"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('modelBenchmark.displaySettings')
  })
  it('shows two shared results above the manual form and never calls administrator APIs', async () => {
    const wrapper = render()
    await flushPromises()
    expect(wrapper.findAll('[data-shared-result]')).toHaveLength(2)
    expect(wrapper.get('[data-testid="benchmark-reference-notice"]').text()).toBe('modelBenchmark.referenceTitle')
    expect(wrapper.html().indexOf('data-shared-schedule')).toBeLessThan(wrapper.html().indexOf('<form'))
    expect(wrapper.find('[name="scheduled_mode"]').exists()).toBe(false)
    expect(wrapper.find('[name="coding"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="bank-update"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="my-tests-section"]').find('form').exists()).toBe(true)
    expect(wrapper.get('[data-testid="site-results-section"]').find('form').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('modelBenchmark.refreshSuite')
    for (const call of Object.values(benchmarks)) expect(call).not.toHaveBeenCalled()
  })

  it('submits only a manual test with today’s suite and clears its key', async () => {
    vi.mocked(userBenchmarks.create).mockResolvedValue({ ...run, status: 'queued', pass_rate: null, queue_position: 1 })
    const wrapper = render()
    await flushPromises()
    await wrapper.get('[name="base_url"]').setValue('https://own.example.com/v1')
    await wrapper.get('[name="api_key"]').setValue('user-secret')
    await wrapper.get('[name="model"]').setValue('test-model')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(userBenchmarks.create).toHaveBeenCalledWith({ base_url: 'https://own.example.com/v1', api_key: 'user-secret', model: 'test-model', effort: 'medium', suite_id: 'today' }, expect.any(AbortSignal))
    expect((wrapper.get('[name="api_key"]').element as HTMLInputElement).value).toBe('')
  })

  it('blocks submissions and reruns when quota is exhausted and shows recovery time', async () => {
    vi.mocked(userBenchmarks.state).mockResolvedValue({ ...initial, quota: { limit: 3, used: 3, remaining: 0, next_available_at: '2026-11-11T02:00:00Z' } })
    const wrapper = render()
    await flushPromises()
    await wrapper.get('[name="base_url"]').setValue('https://own.example.com/v1')
    await wrapper.get('[name="api_key"]').setValue('user-secret')
    await wrapper.get('[name="model"]').setValue('test-model')
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeDefined()
    const rerun = wrapper.findAll('button').find(b => b.text() === 'modelBenchmark.rerun')!
    expect(rerun.attributes('disabled')).toBeDefined()
    expect(wrapper.text()).toContain('modelBenchmark.quotaReset')
    await wrapper.get('form').trigger('submit')
    expect(userBenchmarks.create).not.toHaveBeenCalled()
  })

  it('uses the user rerun endpoint and displays queued positions', async () => {
    vi.mocked(userBenchmarks.rerun).mockResolvedValue({ ...run, id: 'new-run', status: 'queued', queue_position: 2, pass_rate: null })
    const wrapper = render()
    await flushPromises()
    vi.mocked(userBenchmarks.list).mockResolvedValue({ items: [{ ...run, id: 'new-run', status: 'queued', queue_position: 2, pass_rate: null }], total: 1, page_size: 20 })
    await wrapper.findAll('button').find(b => b.text() === 'modelBenchmark.rerun')!.trigger('click')
    await flushPromises()
    expect(userBenchmarks.rerun).toHaveBeenCalledWith('own-run', expect.any(AbortSignal))
    expect(wrapper.get('[data-run-id="new-run"]').text()).toContain('modelBenchmark.queuePosition')
  })

  it('removes a previously shared schedule when visibility is revoked', async () => {
    const wrapper = render()
    await flushPromises()
    vi.mocked(userBenchmarks.showcase).mockResolvedValue({ items: [] })
    await wrapper.findAll('button').find(b => b.text() === 'modelBenchmark.refreshResults')!.trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-shared-schedule]').exists()).toBe(false)
    expect(wrapper.text()).toContain('modelBenchmark.noSharedResults')
  })
})

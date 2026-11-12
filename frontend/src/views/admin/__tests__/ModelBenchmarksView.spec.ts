import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import ModelBenchmarksView from '../ModelBenchmarksView.vue'
import { benchmarks, type BenchmarkRun, type BenchmarkState, type BenchmarkSchedule } from '@/api/admin/model-benchmarks'
import BenchmarkKeyPicker from '@/components/model-identification/BenchmarkKeyPicker.vue'
import BenchmarkSourceSettings from '@/components/model-identification/BenchmarkSourceSettings.vue'
import BenchmarkResults from '@/components/model-identification/BenchmarkResults.vue'
import BenchmarkDeleteDialog from '@/components/model-identification/BenchmarkDeleteDialog.vue'

const app = vi.hoisted(() => ({ showError: vi.fn(), showSuccess: vi.fn(), apiBaseUrl: 'https://gateway.example.com' }))
vi.mock('@/stores/app', () => ({ useAppStore: () => app }))
vi.mock('vue-i18n', async importOriginal => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({ t: (key: string) => key })
}))
vi.mock('@/api/admin/model-benchmarks', () => ({
  benchmarks: { userRuns: vi.fn(), source: vi.fn(), showcase: vi.fn(), results: vi.fn(), setUserTesting: vi.fn(), runSchedule: vi.fn(), updateBank: vi.fn(), activateBank: vi.fn(), retryEnvironment: vi.fn(), state: vi.fn(), setExecution: vi.fn(), displays: vi.fn(), configure: vi.fn(), refresh: vi.fn(), create: vi.fn(), list: vi.fn(), detail: vi.fn(), rerun: vi.fn(), cancel: vi.fn(), schedules: vi.fn(), createSchedule: vi.fn(), updateSchedule: vi.fn(), deleteSchedule: vi.fn() }
}))
const initial: BenchmarkState = {
  execution: { max_concurrency: 5, running: 2, queued: 4 },
  ready: true, message: '', timezone: 'Asia/Shanghai',
  counts: { coding: 20, math: 5, reasoning: 5 }, capacities: { coding: 132, math: 29, reasoning: 100 },
  suite: { id: 'suite-one', day: '2026-11-11', revision: 1, fingerprint: 'a'.repeat(64), bank_version: 'v1', total: 30, counts: { coding: 20, math: 5, reasoning: 5 }, questions: [] }
}
function run(overrides: Partial<BenchmarkRun> = {}): BenchmarkRun {
  return {
    id: 'run-one', suite_id: initial.suite!.id, suite_day: initial.suite!.day, suite_revision: 1, suite_fingerprint: initial.suite!.fingerprint,
    rerun_of: null, base_url: 'https://example.com/v1', model: 'fixture-model', effort: 'medium', status: 'completed',
    created_at: '2026-11-11T00:00:00Z', elapsed_seconds: 60, total: 30, completed: 30, passed: 23, pass_rate: 23 / 30 * 100,
    categories: { coding: { total: 20, completed: 20, passed: 16 }, math: { total: 5, completed: 5, passed: 4 }, reasoning: { total: 5, completed: 5, passed: 3 } },
    usage: { input_tokens: 10, output_tokens: 20, total_tokens: 30 }, error: '', api_errors: 0, grading_errors: 0, results: [], ...overrides
  }
}
function render() {
  return mount(ModelBenchmarksView, { global: { stubs: { AppLayout: { template: '<main><slot /></main>' }, IdentificationTabs: true, BenchmarkSourceSettings: true, BenchmarkKeyPicker: true, BaseDialog: true, ConfirmDialog: true } } })
}
function schedule(): BenchmarkSchedule {
  return { id: 'schedule-one', name: 'Daily check', base_url: 'https://example.com/v1', model: 'fixture-model', effort: 'medium', cron_expression: '0 9 * * *', enabled: true, public_results: false, timezone: 'Asia/Shanghai', next_run_at: '2026-11-12T01:00:00Z', last_attempt_at: null, last_run_id: null, last_error: '', created_at: '', updated_at: '' }
}
enableAutoUnmount(afterEach)
beforeEach(() => {
  vi.resetAllMocks()
  vi.mocked(benchmarks.source).mockResolvedValue({ source_url: '', site_id: '', has_token: false })
  vi.mocked(benchmarks.showcase).mockResolvedValue({ items: [], next_cursor: null })
  vi.mocked(benchmarks.results).mockResolvedValue({ items: [], next_cursor: null })
  vi.mocked(benchmarks.state).mockResolvedValue(structuredClone(initial))
  vi.mocked(benchmarks.list).mockResolvedValue({ items: [], total: 0, page_size: 20 })
  vi.mocked(benchmarks.detail).mockResolvedValue(run())
  vi.mocked(benchmarks.schedules).mockResolvedValue({ items: [] })
})

describe('administrator model benchmarks', () => {
  it('does not restore a deleted shared result from an older in-flight refresh', async () => {
    const feed = { items: [{ id: 'group', name: '', model: 'fixture-model', effort: 'medium', results: [run()] }] }
    vi.mocked(benchmarks.list).mockResolvedValue({ items: [run()], total: 1, page_size: 20 })
    vi.mocked(benchmarks.results).mockResolvedValue(feed)
    const wrapper = render(); await flushPromises()
    expect(wrapper.find('[data-shared-result="run-one"]').exists()).toBe(true)
    let resolveOld!: (value: typeof feed) => void
    vi.mocked(benchmarks.results).mockImplementationOnce(() => new Promise(resolve => { resolveOld = resolve }))
    wrapper.getComponent(BenchmarkResults).vm.$emit('refresh'); await flushPromises()
    vi.mocked(benchmarks.results).mockResolvedValue({ items: [] })
    vi.mocked(benchmarks.list).mockResolvedValue({ items: [], total: 0, page_size: 20 })
    wrapper.getComponent(BenchmarkDeleteDialog).vm.$emit('deleted', 'run-one'); await flushPromises()
    resolveOld(feed); await flushPromises()
    expect(wrapper.find('[data-shared-result="run-one"]').exists()).toBe(false)
  })
  it('closes deleted details when another administrator removes the result', async () => {
    vi.mocked(benchmarks.list).mockResolvedValue({ items: [run()], total: 1, page_size: 20 })
    const wrapper = render(); await flushPromises()
    await wrapper.findAll('button').find(button => button.text() === 'modelBenchmark.details')!.trigger('click'); await flushPromises()
    expect(wrapper.find('section[aria-label="modelBenchmark.questionResults"]').exists()).toBe(true)
    vi.mocked(benchmarks.detail).mockRejectedValue({ response: { status: 404 } })
    vi.mocked(benchmarks.list).mockResolvedValue({ items: [], total: 0, page_size: 20 })
    wrapper.getComponent(BenchmarkResults).vm.$emit('refresh'); await flushPromises()
    expect(wrapper.find('section[aria-label="modelBenchmark.questionResults"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('modelBenchmark.loadError')
  })
  it('removes only local shared results even if refresh fails after deletion', async () => {
    const record = run()
    const source = (id: string, source_type: 'local' | 'remote') => ({ id, name: id, source_type, last_success_at: null, stale: false, pending: false,
      items: [{ id: 'plan', name: '', model: record.model, effort: record.effort, results: [record] }] })
    vi.mocked(benchmarks.showcase).mockResolvedValue({ items: [source('local', 'local'), source('remote', 'remote')] })
    const wrapper = render(); await flushPromises()
    expect(wrapper.findAll('[data-shared-result="run-one"]')).toHaveLength(2)
    vi.mocked(benchmarks.source).mockRejectedValue(new Error('offline'))
    wrapper.getComponent(BenchmarkDeleteDialog).vm.$emit('deleted', 'run-one'); await flushPromises()
    expect(wrapper.find('[data-shared-schedule="local:plan"]').exists()).toBe(false)
    expect(wrapper.find('[data-shared-schedule="remote:plan"]').exists()).toBe(true)
  })
  it('ignores a stale load-more failure after deletion and keeps the next refresh usable', async () => {
    const feed = { items: [{ id: 'group', name: '', model: 'fixture-model', effort: 'medium', results: [run()] }], next_cursor: 'page-two' }
    vi.mocked(benchmarks.results).mockResolvedValue(feed)
    const wrapper = render(); await flushPromises()
    let rejectOld!: (reason: Error) => void
    vi.mocked(benchmarks.results).mockImplementationOnce(() => new Promise((_resolve, reject) => { rejectOld = reject }))
    await wrapper.get('[data-testid="users-load-more"]').trigger('click'); await flushPromises()
    const fresh = { ...run(), id: 'fresh' }
    vi.mocked(benchmarks.results).mockResolvedValue({ items: [{ ...feed.items[0], results: [fresh] }] })
    wrapper.getComponent(BenchmarkDeleteDialog).vm.$emit('deleted', 'run-one'); await flushPromises()
    rejectOld(new Error('stale request failed')); await flushPromises()
    expect(wrapper.find('[data-shared-result="run-one"]').exists()).toBe(false)
    expect(wrapper.find('[data-shared-result="fresh"]').exists()).toBe(true)
    expect(wrapper.get('[data-testid="users-result-row"]').text()).not.toContain('modelBenchmark.loadError')
  })
  it('offers deletion only for ended results and refreshes after deletion', async () => {
    vi.mocked(benchmarks.list).mockResolvedValue({ items: [run(), run({ id: 'running', status: 'running' }), run({ id: 'queued', status: 'queued' }), run({ id: 'stopping', status: 'cancelling' })], total: 4, page_size: 20 })
    const wrapper = render(); await flushPromises()
    expect(wrapper.findAll('[data-delete-run]')).toHaveLength(1)
    await wrapper.findAll('button').find(button => button.text() === 'modelBenchmark.details')!.trigger('click'); await flushPromises()
    await wrapper.get('[data-delete-run="run-one"]').trigger('click')
    expect(wrapper.getComponent(BenchmarkDeleteDialog).props('run')?.id).toBe('run-one')
    vi.mocked(benchmarks.list).mockResolvedValue({ items: [], total: 0, page: 1, page_size: 20 })
    wrapper.getComponent(BenchmarkDeleteDialog).vm.$emit('deleted', 'run-one'); await flushPromises()
    expect(wrapper.find('[data-delete-run="run-one"]').exists()).toBe(false)
    expect(wrapper.find('section[aria-label="modelBenchmark.questionResults"]').exists()).toBe(false)
    expect(app.showSuccess).toHaveBeenCalledWith('modelBenchmark.resultDeleted')
  })
  it('用户记录页在来源变化后清空旧记录并中止旧请求', async () => {
    const records = { items: [run({ model: 'old-user-model', owner_user_id: 7 })], total: 1, page: 1, page_size: 20 }
    vi.mocked(benchmarks.userRuns).mockResolvedValueOnce(records)
    const wrapper = render(); await flushPromises()
    await wrapper.findAll('[role="tab"]').find(tab => tab.text() === 'modelBenchmark.userRecords')!.trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="user-records"]').text()).toContain('old-user-model')
    let resolveOld!: (value: Awaited<ReturnType<typeof benchmarks.userRuns>>) => void
    vi.mocked(benchmarks.userRuns).mockImplementationOnce(() => new Promise(resolve => { resolveOld = resolve }))
    await wrapper.get('[data-testid="user-records"] button').trigger('click')
    const oldSignal = vi.mocked(benchmarks.userRuns).mock.calls[1][2]
    vi.mocked(benchmarks.userRuns).mockRejectedValue(new Error('new site offline'))
    vi.mocked(benchmarks.source).mockResolvedValue({ source_url: 'https://new.example/federation', site_id: 'b'.repeat(32), has_token: true })
    wrapper.getComponent(BenchmarkResults).vm.$emit('refresh')
    await flushPromises()
    resolveOld(records)
    await flushPromises()
    expect(oldSignal?.aborted).toBe(true)
    expect(wrapper.get('[data-testid="user-records"]').text()).not.toContain('old-user-model')
  })
  it('切换主站后即使新站请求失败也不保留旧站结果', async () => {
    const record = run({ id: 'old-run', model: 'old-site-model' })
    vi.mocked(benchmarks.results).mockResolvedValueOnce({ items: [{ id: record.id, name: '', model: record.model, effort: record.effort, results: [record] }] })
    const wrapper = render(); await flushPromises()
    expect(wrapper.text()).toContain('old-site-model')
    vi.mocked(benchmarks.results).mockRejectedValue(new Error('new site offline'))
    vi.mocked(benchmarks.source).mockResolvedValue({ source_url: 'https://new.example/federation', site_id: 'b'.repeat(32), has_token: true })
    await wrapper.findAll('[role="tab"]').find(tab => tab.text() === 'modelBenchmark.sourceSettings')!.trigger('click')
    wrapper.getComponent(BenchmarkSourceSettings).vm.$emit('saved', { source_url: 'https://new.example/federation', site_id: 'b'.repeat(32), has_token: true })
    await flushPromises()
    await flushPromises()
    expect(wrapper.text()).not.toContain('old-site-model')
  })
  it('切换主站后旧站在途响应不能写入新站结果', async () => {
    let resolveOld!: (value: Awaited<ReturnType<typeof benchmarks.results>>) => void
    vi.mocked(benchmarks.results).mockImplementationOnce(() => new Promise(resolve => { resolveOld = resolve }))
    const wrapper = render(); await flushPromises()
    const oldSignal = vi.mocked(benchmarks.results).mock.calls[0][0]
    vi.mocked(benchmarks.source).mockResolvedValue({ source_url: 'https://new.example/federation', site_id: 'b'.repeat(32), has_token: true })
    await wrapper.findAll('[role="tab"]').find(tab => tab.text() === 'modelBenchmark.sourceSettings')!.trigger('click')
    wrapper.getComponent(BenchmarkSourceSettings).vm.$emit('saved', { source_url: 'https://new.example/federation', site_id: 'b'.repeat(32), has_token: true })
    await flushPromises()
    const record = run({ id: 'old-run', model: 'late-old-model' })
    resolveOld({ items: [{ id: record.id, name: '', model: record.model, effort: record.effort, results: [record] }] })
    await flushPromises()
    expect(oldSignal?.aborted).toBe(true)
    expect(wrapper.text()).not.toContain('late-old-model')
  })
  it('刷新能识别其他管理员修改的主站配置', async () => {
    const wrapper = render(); await flushPromises()
    expect(wrapper.find('input[name="base_url"]').exists()).toBe(true)
    vi.mocked(benchmarks.source).mockResolvedValue({ source_url: 'https://new.example/federation', site_id: 'b'.repeat(32), has_token: true })
    wrapper.getComponent(BenchmarkResults).vm.$emit('refresh')
    await flushPromises()
    expect(wrapper.find('[data-testid="remote-benchmark-hint"]').exists()).toBe(true)
    expect(wrapper.find('input[name="base_url"]').exists()).toBe(false)
  })
  it('接入站展示主站结果且不请求本地测试管理数据', async () => {
    vi.mocked(benchmarks.source).mockResolvedValue({ source_url: 'https://main.example/api/v1/model-benchmarks/federation', site_id: 'a'.repeat(32), has_token: true })
    const wrapper = render(); await flushPromises()
    expect(wrapper.find('[data-testid="remote-benchmark-hint"]').exists()).toBe(true)
    expect(wrapper.find('input[name="base_url"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="user-testing-toggle"]').exists()).toBe(false)
    expect(wrapper.findAll('[role="tab"]').some(tab => tab.text() === 'modelBenchmark.displaySettings')).toBe(false)
    expect(benchmarks.state).toHaveBeenCalled()
    expect(benchmarks.showcase).toHaveBeenCalled()
    expect(benchmarks.results).toHaveBeenCalled()
    expect(benchmarks.list).not.toHaveBeenCalled()
    expect(benchmarks.schedules).not.toHaveBeenCalled()
  })
  it('接入配置读取失败时不按本站模式发起测试管理请求', async () => {
    vi.mocked(benchmarks.source).mockRejectedValueOnce(new Error('offline'))
    const wrapper = render(); await flushPromises()
    expect(wrapper.find('input[name="base_url"]').exists()).toBe(false)
    expect(benchmarks.state).not.toHaveBeenCalled()
    expect(benchmarks.list).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('modelBenchmark.loadError')
  })
  it('opens the administrator user-record tab independently of test configuration', async () => {
    const wrapper = mount(ModelBenchmarksView, { global: { stubs: { AppLayout: { template: '<main><slot /></main>' }, IdentificationTabs: true, BenchmarkKeyPicker: true, BenchmarkUserRecords: true, BaseDialog: true, ConfirmDialog: true } } })
    await flushPromises()
    expect(wrapper.findComponent({ name: 'BenchmarkUserRecords' }).exists()).toBe(false)
    const tab = wrapper.findAll('[role="tab"]').find(b => b.text() === 'modelBenchmark.userRecords')!
    await tab.trigger('click'); await flushPromises()
    expect(tab.attributes('aria-selected')).toBe('true')
    expect(wrapper.findComponent({ name: 'BenchmarkUserRecords' }).exists()).toBe(true)
    expect(benchmarks.configure).not.toHaveBeenCalled()
  })
  it.each([[59.9, 'text-red-600'], [60, 'text-yellow-600'], [70, 'text-emerald-600']] as const)('colors administrator history at %s', async (score, color) => {
    vi.mocked(benchmarks.list).mockResolvedValue({ items: [run({ pass_rate: score })], total: 1, page_size: 20 })
    const wrapper = render(); await flushPromises()
    expect(wrapper.get('[data-testid="pass-rate"]').classes()).toContain(color)
  })
  it('blocks new tests, reruns, schedule execution and bank updates during resource pressure, then recovers', async () => {
    vi.mocked(benchmarks.state).mockResolvedValue({ ...initial, resources: { blocked: true, status: 'blocked', message: 'Memory at 90%', memory_percent: 90, disk_free_gb: 12 } })
    vi.mocked(benchmarks.list).mockResolvedValue({ items: [run()], total: 1, page_size: 20 })
    vi.mocked(benchmarks.schedules).mockResolvedValue({ items: [schedule()] })
    const wrapper = render(); await flushPromises()
    await wrapper.get('[name="base_url"]').setValue('https://example.com/v1')
    await wrapper.get('[name="api_key"]').setValue('fixture-secret')
    await wrapper.get('[name="model"]').setValue('fixture-model')
    expect(wrapper.get('[data-testid="resource-status"]').text()).toContain('Memory at 90%')
    expect(wrapper.find('[data-testid="environment-status"]').exists()).toBe(false)
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-testid="bank-update"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-testid="run-schedule-schedule-one"]').attributes('disabled')).toBeDefined()
    expect(wrapper.findAll('button').find(b => b.text() === 'modelBenchmark.rerun')!.attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-testid="user-testing-toggle"]').attributes('disabled')).toBeUndefined()
    vi.mocked(benchmarks.state).mockResolvedValue({ ...initial, resources: { blocked: false, status: 'ok', message: '' } })
    await wrapper.findAll('button').find(b => b.text() === 'modelBenchmark.refreshResults')!.trigger('click'); await flushPromises()
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeUndefined()
    expect(wrapper.get('[data-testid="run-schedule-schedule-one"]').attributes('disabled')).toBeUndefined()
  })
  it('saves the user testing switch without overwriting concurrency or suite settings', async () => {
    const wrapper = render(); await flushPromises()
    const toggle = wrapper.get('[data-testid="user-testing-toggle"]')
    expect(toggle.attributes('aria-checked')).toBe('false')
    await wrapper.get('[name="max_concurrency"]').setValue(8)
    const execution = { ...initial.execution!, user_testing_enabled: true }
    vi.mocked(benchmarks.setUserTesting).mockResolvedValue(execution)
    vi.mocked(benchmarks.state).mockResolvedValue({ ...initial, execution })
    await toggle.trigger('click'); await flushPromises()
    expect(benchmarks.setUserTesting).toHaveBeenCalledWith(true, expect.any(AbortSignal))
    expect(toggle.attributes('aria-checked')).toBe('true')
    expect((wrapper.get('[name="max_concurrency"]').element as HTMLInputElement).value).toBe('8')
    expect(benchmarks.setExecution).not.toHaveBeenCalled()
    expect(benchmarks.configure).not.toHaveBeenCalled()
    vi.mocked(benchmarks.setUserTesting).mockRejectedValue(new Error('Save failed'))
    await toggle.trigger('click'); await flushPromises()
    expect(toggle.attributes('aria-checked')).toBe('true')
    expect(app.showError).toHaveBeenCalledWith('Save failed')
  })
  it('downloads bank updates without activating them and blocks duplicate requests', async () => {
    const wrapper = render(); await flushPromises()
    const bank_update = { status: 'checking' as const, message: 'Checking official sources', checked_at: null, candidate: null }
    vi.mocked(benchmarks.updateBank).mockResolvedValue(bank_update)
    vi.mocked(benchmarks.state).mockResolvedValue({ ...initial, bank_update })
    await wrapper.get('[data-testid="bank-update"]').trigger('click'); await flushPromises()
    expect(benchmarks.updateBank).toHaveBeenCalledWith(expect.any(AbortSignal))
    expect(benchmarks.activateBank).not.toHaveBeenCalled()
    expect(wrapper.get('[data-testid="bank-update"]').attributes('disabled')).toBeDefined()
    expect(wrapper.text()).toContain('Checking official sources')
  })
  it('activates only the reviewed candidate after confirmation', async () => {
    const candidate = { version: 'public-v2-new', counts: { coding: 300, math: 29, reasoning: 100 }, coding_counts: { medium: 100, hard: 200 }, added: 168, changed: 0, removed: 0 }
    vi.mocked(benchmarks.state).mockResolvedValue({ ...initial, bank_update: { status: 'ready', message: '', checked_at: null, candidate } })
    vi.mocked(benchmarks.activateBank).mockResolvedValue(initial.suite!)
    const wrapper = render(); await flushPromises()
    await wrapper.get('[data-testid="bank-activate"]').trigger('click'); await flushPromises()
    expect(benchmarks.activateBank).not.toHaveBeenCalled()
    const bank = wrapper.findComponent({ name: 'BenchmarkBankSettings' })
    bank.findComponent({ name: 'ConfirmDialog' }).vm.$emit('confirm'); await flushPromises()
    expect(benchmarks.activateBank).toHaveBeenCalledWith(candidate.version, expect.any(AbortSignal))
  })
  it('shows automatic setup before the bank exists, retries and enables testing when ready', async () => {
    const environment = { status: 'failed' as const, stage: 'questions', progress: 35, attempt: 1, message: 'Preparing questions', next_retry_at: '2026-11-12T00:01:00Z' }
    vi.mocked(benchmarks.state).mockResolvedValue({ ...initial, ready: false, suite: null, message: environment.message, environment })
    vi.mocked(benchmarks.retryEnvironment).mockResolvedValue(environment)
    const wrapper = render(); await flushPromises()
    expect(wrapper.get('[data-testid="environment-status"]').text()).toContain('Preparing questions')
    expect(wrapper.get('progress').attributes('value')).toBe('35')
    await wrapper.get('[name="base_url"]').setValue('https://example.com/v1')
    await wrapper.get('[name="api_key"]').setValue('test-secret')
    await wrapper.get('[name="model"]').setValue('test-model')
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-testid="environment-retry"]').trigger('click'); await flushPromises()
    expect(benchmarks.retryEnvironment).toHaveBeenCalledTimes(1)
    vi.mocked(benchmarks.state).mockResolvedValue(initial)
    await wrapper.get('[data-testid="environment-retry"]').trigger('click'); await flushPromises()
    expect(wrapper.find('[data-testid="environment-status"]').exists()).toBe(false)
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeUndefined()
  })
  it('saves the global concurrency separately from the question suite', async () => {
    vi.mocked(benchmarks.setExecution).mockResolvedValue({ max_concurrency: 8, running: 2, queued: 4 })
    const wrapper = render()
    await flushPromises()
    await wrapper.get('[name="max_concurrency"]').setValue(8)
    await wrapper.findAll('form').find(form => form.find('[name="max_concurrency"]').exists())!.trigger('submit')
    await flushPromises()
    expect(benchmarks.setExecution).toHaveBeenCalledWith(8, expect.any(AbortSignal))
    expect(benchmarks.configure).not.toHaveBeenCalled()
  })

  it('opens the display settings tab', async () => {
    vi.mocked(benchmarks.displays).mockResolvedValue({ items: [] })
    const wrapper = render()
    await flushPromises()
    await wrapper.findAll('[role="tab"]').find(b => b.text() === 'modelBenchmark.displaySettings')!.trigger('click')
    await flushPromises()
    expect(benchmarks.displays).toHaveBeenCalled()
    expect(wrapper.text()).toContain('modelBenchmark.publicApi')
  })
  it('toggles public visibility independently of the execution schedule', async () => {
    vi.mocked(benchmarks.schedules).mockResolvedValue({ items: [schedule()] })
    vi.mocked(benchmarks.updateSchedule).mockResolvedValue({ ...schedule(), public_results: true })
    const wrapper = render()
    await flushPromises()
    expect(wrapper.get('[role="switch"]').attributes('aria-checked')).toBe('false')
    await wrapper.get('[data-schedule-id="schedule-one"] [role="switch"]').trigger('click')
    await flushPromises()
    expect(benchmarks.updateSchedule).toHaveBeenCalledWith('schedule-one', { public_results: true }, expect.any(AbortSignal))
  })
  it('fills the site URL and selected own key, then clears the key if the URL changes', async () => {
    const wrapper = render()
    await flushPromises()
    await wrapper.findAll('button').find(b => b.text() === 'modelBenchmark.selectVersion')!.trigger('click')
    expect(wrapper.getComponent(BenchmarkKeyPicker).props('show')).toBe(true)
    wrapper.getComponent(BenchmarkKeyPicker).vm.$emit('select', { id: 7, name: 'My key', key: 'own-key-secret' })
    await flushPromises()
    expect((wrapper.get('[name="base_url"]').element as HTMLInputElement).value).toBe('https://gateway.example.com/v1')
    expect((wrapper.get('[name="api_key"]').element as HTMLInputElement).value).toBe('own-key-secret')
    expect(wrapper.getComponent(BenchmarkKeyPicker).props('show')).toBe(false)
    await wrapper.get('[name="base_url"]').setValue('https://another.example/v1')
    expect((wrapper.get('[name="api_key"]').element as HTMLInputElement).value).toBe('')
  })

  it('creates a scheduled test with the chosen API and clears its secret', async () => {
    vi.mocked(benchmarks.createSchedule).mockResolvedValue(schedule())
    const wrapper = render()
    await flushPromises()
    await wrapper.get('[name="base_url"]').setValue('https://example.com/v1')
    await wrapper.get('[name="api_key"]').setValue('schedule-secret')
    await wrapper.get('[name="model"]').setValue('fixture-model')
    await wrapper.get('[name="scheduled_mode"]').setValue(true)
    await wrapper.get('[name="schedule_name"]').setValue('Daily check')
    await wrapper.get('[name="schedule_preset"]').setValue('custom')
    await wrapper.get('[name="cron_expression"]').setValue('0 10 * * *')
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(benchmarks.createSchedule).toHaveBeenCalledWith({ name: 'Daily check', cron_expression: '0 10 * * *', enabled: true, public_results: false, base_url: 'https://example.com/v1', api_key: 'schedule-secret', model: 'fixture-model', effort: 'medium' }, expect.any(AbortSignal))
    expect(benchmarks.create).not.toHaveBeenCalled()
    expect((wrapper.get('[name="api_key"]').element as HTMLInputElement).value).toBe('')
  })

  it('pauses a persistent plan from the schedule list', async () => {
    vi.mocked(benchmarks.schedules).mockResolvedValue({ items: [schedule()] })
    vi.mocked(benchmarks.updateSchedule).mockResolvedValue({ ...schedule(), enabled: false })
    const wrapper = render()
    await flushPromises()
    await wrapper.findAll('button').find(b => b.text() === 'modelBenchmark.pauseSchedule')!.trigger('click')
    await flushPromises()
    expect(benchmarks.updateSchedule).toHaveBeenCalledWith('schedule-one', { enabled: false }, expect.any(AbortSignal))
  })

  it.each(['m', 'h'])('creates an interval schedule in %s', async unit => {
    vi.mocked(benchmarks.createSchedule).mockResolvedValue(schedule())
    const wrapper = render()
    await flushPromises()
    await wrapper.get('[name="base_url"]').setValue('https://example.com/v1')
    await wrapper.get('[name="api_key"]').setValue('schedule-secret')
    await wrapper.get('[name="model"]').setValue('fixture-model')
    await wrapper.get('[name="scheduled_mode"]').setValue(true)
    expect((wrapper.get('[name="schedule_interval"]').element as HTMLInputElement).value).toBe('30')
    await wrapper.get('[name="schedule_name"]').setValue('Interval check')
    await wrapper.get('[name="schedule_interval"]').setValue(2)
    await wrapper.get('[name="schedule_interval_unit"]').setValue(unit)
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(benchmarks.createSchedule).toHaveBeenCalledWith(expect.objectContaining({ cron_expression: `@every 2${unit}` }), expect.any(AbortSignal))
  })

  it('runs a paused schedule once and blocks duplicate clicks', async () => {
    const plan = { ...schedule(), enabled: false }
    vi.mocked(benchmarks.schedules).mockResolvedValue({ items: [plan] })
    vi.mocked(benchmarks.runSchedule).mockResolvedValue(run({ status: 'queued', schedule_id: plan.id }))
    const wrapper = render()
    await flushPromises()
    const button = wrapper.get('[data-testid="run-schedule-schedule-one"]')
    await button.trigger('click')
    await button.trigger('click')
    await flushPromises()
    expect(benchmarks.runSchedule).toHaveBeenCalledTimes(1)
    expect(benchmarks.runSchedule).toHaveBeenCalledWith(plan.id, expect.any(AbortSignal))
    expect(benchmarks.updateSchedule).not.toHaveBeenCalled()
  })

  it('disables run now when the schedule has an active run outside the records page', async () => {
    vi.mocked(benchmarks.schedules).mockResolvedValue({ items: [{ ...schedule(), active_run_id: 'older-active-run' }] })
    const wrapper = render()
    await flushPromises()
    expect(wrapper.get('[data-testid="run-schedule-schedule-one"]').attributes('disabled')).toBeDefined()
  })

  it('submits the current suite and medium effort, then clears the API key', async () => {
    vi.mocked(benchmarks.create).mockResolvedValue(run({ status: 'queued', pass_rate: null }))
    const wrapper = render()
    await flushPromises()
    expect((wrapper.get('[name="effort"]').element as HTMLSelectElement).value).toBe('medium')
    for (const [category, count] of Object.entries(initial.counts)) {
      expect((wrapper.get(`[name="${category}"]`).element as HTMLInputElement).value).toBe(String(count))
    }
    await wrapper.get('[name="base_url"]').setValue('https://example.com/v1')
    await wrapper.get('[name="api_key"]').setValue('fixture-secret')
    await wrapper.get('[name="model"]').setValue('fixture-model')
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(benchmarks.create).toHaveBeenCalledWith({ base_url: 'https://example.com/v1', api_key: 'fixture-secret', model: 'fixture-model', effort: 'medium', suite_id: 'suite-one' }, expect.any(AbortSignal))
    expect((wrapper.get('[name="api_key"]').element as HTMLInputElement).value).toBe('')
    expect(localStorage.getItem('fixture-secret')).toBeNull()
  })

  it('shows the pass rate and displays the current suite returned by retesting', async () => {
    vi.mocked(benchmarks.list).mockResolvedValue({ items: [run()], total: 1, page_size: 20 })
    const retest = run({ id: 'new-run', rerun_of: 'run-one', status: 'queued', pass_rate: null,
      suite_id: 'suite-today', suite_day: '2026-11-12', suite_revision: 2, suite_fingerprint: 'b'.repeat(64) })
    vi.mocked(benchmarks.rerun).mockResolvedValue(retest)
    vi.mocked(benchmarks.detail).mockResolvedValue(retest)
    const wrapper = render()
    await flushPromises()
    expect(wrapper.get('[data-testid="pass-rate"]').text()).toBe('76.7%')
    const rerunButton = wrapper.findAll('button').find(b => b.text() === 'modelBenchmark.rerun')!
    await rerunButton.trigger('click')
    await rerunButton.trigger('click')
    await flushPromises()
    expect(benchmarks.rerun).toHaveBeenCalledTimes(1)
    expect(benchmarks.rerun).toHaveBeenCalledWith('run-one', expect.any(AbortSignal))
    expect(wrapper.get('section[aria-label="modelBenchmark.questionResults"]').text()).toContain('2026-11-12 v2')
  })

  it('keeps unsaved counts during refresh and blocks starting with stale settings', async () => {
    const wrapper = render()
    await flushPromises()
    await wrapper.get('[name="coding_medium"]').setValue(4)
    await wrapper.get('[name="coding_hard"]').setValue(8)
    await wrapper.findAll('button').find(b => b.text() === 'modelBenchmark.refreshResults')!.trigger('click')
    await flushPromises()
    expect((wrapper.get('[name="coding"]').element as HTMLInputElement).value).toBe('12')
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeDefined()
    const suite = { ...initial.suite, id: 'suite-two', revision: 2, total: 22, counts: { coding: 12, math: 5, reasoning: 5 }, coding_counts: { medium: 4, hard: 8 } }
    vi.mocked(benchmarks.configure).mockResolvedValue(suite)
    vi.mocked(benchmarks.state).mockResolvedValue({ ...initial, suite, counts: suite.counts, coding_counts: suite.coding_counts })
    await wrapper.findAll('form')[1].trigger('submit')
    await flushPromises()
    expect(benchmarks.configure).toHaveBeenCalledWith(suite.counts, 'suite-one', expect.any(AbortSignal), { medium: 4, hard: 8 })
    expect(wrapper.text()).toContain('v2')
  })

  it('does not present API errors as a zero capability score and aborts polling on unmount', async () => {
    vi.mocked(benchmarks.list).mockResolvedValue({ items: [run({ status: 'failed', pass_rate: null, api_errors: 1 })], total: 1, page_size: 20 })
    const wrapper = render()
    await flushPromises()
    expect(wrapper.get('[data-testid="pass-rate"]').text()).toBe('—')
    const signal = vi.mocked(benchmarks.state).mock.calls[0][0]!
    wrapper.unmount()
    expect(signal.aborted).toBe(true)
  })
})

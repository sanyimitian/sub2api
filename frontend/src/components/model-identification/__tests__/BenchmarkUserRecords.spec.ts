import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import BenchmarkUserRecords from '../BenchmarkUserRecords.vue'
import BenchmarkDeleteDialog from '../BenchmarkDeleteDialog.vue'
import { benchmarks, type BenchmarkRun } from '@/api/admin/model-benchmarks'
vi.mock('vue-i18n', async importOriginal => ({ ...await importOriginal<typeof import('vue-i18n')>(), useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/api/admin/model-benchmarks', () => ({ benchmarks: { userRuns: vi.fn(), userOptions: vi.fn(), detail: vi.fn() } }))
const run: BenchmarkRun = { id: 'user-run', owner_user_id: 7, owner: { id: 7, email: 'user@example.com', username: 'tester', deleted: false },
  suite_id: 'suite', suite_day: '2026-11-12', suite_revision: 1, suite_fingerprint: 'a'.repeat(64), rerun_of: null, base_url: 'https://example.com/v1', model: '6.1sol', effort: 'medium', status: 'completed', created_at: '2026-11-12T01:00:00Z', elapsed_seconds: 10, total: 30, completed: 30, passed: 18, pass_rate: 60,
  categories: { coding: { total: 20, completed: 20, passed: 10 }, math: { total: 5, completed: 5, passed: 4 }, reasoning: { total: 5, completed: 5, passed: 4 } }, usage: { input_tokens: 1, output_tokens: 1, total_tokens: 2 }, error: '', api_errors: 0, grading_errors: 0 }
function render(canDelete = false) { return mount(BenchmarkUserRecords, { props: { canDelete }, global: { stubs: { BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /></div>' } } } }) }
enableAutoUnmount(afterEach)
beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(benchmarks.userRuns).mockResolvedValue({ items: [run], total: 41, page: 1, page_size: 20 })
  vi.mocked(benchmarks.userOptions).mockResolvedValue({ items: [run.owner!], has_more: false })
  vi.mocked(benchmarks.detail).mockResolvedValue({ ...run, results: [] })
})
afterEach(() => vi.useRealTimers())
describe('administrator user benchmark records', () => {
  it('allows main-site deletion, closes details and reloads the page', async () => {
    const wrapper = render(true); await flushPromises()
    await wrapper.findAll('button').find(button => button.text() === 'modelBenchmark.details')!.trigger('click'); await flushPromises()
    await wrapper.get('[data-delete-run="user-run"]').trigger('click')
    expect(wrapper.getComponent(BenchmarkDeleteDialog).props('run')?.id).toBe('user-run')
    vi.mocked(benchmarks.userRuns).mockResolvedValue({ items: [], total: 0, page: 1, page_size: 20 })
    wrapper.getComponent(BenchmarkDeleteDialog).vm.$emit('deleted', 'user-run'); await flushPromises()
    expect(wrapper.emitted('deleted')).toEqual([['user-run']])
    expect(wrapper.find('[data-user-run="user-run"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="user-record-detail"]').exists()).toBe(false)
  })
  it('does not expose deletion in read-only mode or for active tests', async () => {
    const wrapper = render(); await flushPromises()
    expect(wrapper.find('[data-delete-run]').exists()).toBe(false)
    vi.mocked(benchmarks.userRuns).mockResolvedValue({ items: [{ ...run, status: 'running' }], total: 1, page: 1, page_size: 20 })
    const active = render(true); await flushPromises()
    expect(active.find('[data-delete-run]').exists()).toBe(false)
  })
  it('loads all user records, displays owners and requests pagination from the server', async () => {
    const wrapper = render(); await flushPromises()
    expect(benchmarks.userRuns).toHaveBeenCalledWith(1, {}, expect.any(AbortSignal))
    expect(wrapper.text()).toContain('user@example.com')
    expect(wrapper.get('[data-testid="user-pass-rate"]').classes()).toContain('text-yellow-600')
    vi.mocked(benchmarks.userRuns).mockResolvedValue({ items: [{ ...run, id: 'page-two' }], total: 41, page: 2, page_size: 20 })
    wrapper.findComponent({ name: 'Pagination' }).vm.$emit('update:page', 2); await flushPromises()
    expect(benchmarks.userRuns).toHaveBeenLastCalledWith(2, {}, expect.any(AbortSignal))
    expect(wrapper.find('[data-user-run="page-two"]').exists()).toBe(true)
  })
  it('combines selected account and model and resets pagination when searching', async () => {
    vi.useFakeTimers()
    const wrapper = render(); await flushPromises()
    await wrapper.get('[name="account_search"]').setValue('tester')
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeDefined()
    await vi.advanceTimersByTimeAsync(300); await flushPromises()
    expect(benchmarks.userOptions).toHaveBeenCalledWith('tester', expect.any(AbortSignal))
    await wrapper.get('[name="account_selection"]').setValue('7')
    await wrapper.get('[name="model_search"]').setValue('sol')
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(benchmarks.userRuns).toHaveBeenLastCalledWith(1, { user_id: 7, model: 'sol' }, expect.any(AbortSignal))
    await wrapper.findAll('button').find(b => b.text() === 'modelBenchmark.reset')!.trigger('click'); await flushPromises()
    expect(benchmarks.userRuns).toHaveBeenLastCalledWith(1, { user_id: undefined, model: undefined }, expect.any(AbortSignal))
  })
  it('ignores stale page responses and renders hostile account values as text', async () => {
    let resolveFirst!: (value: { items: BenchmarkRun[]; total: number; page: number; page_size: number }) => void
    vi.mocked(benchmarks.userRuns).mockImplementationOnce(() => new Promise(resolve => { resolveFirst = resolve }))
    const wrapper = render(); await flushPromises()
    const injected = { ...run, owner: { ...run.owner!, email: '<img src=x onerror=alert(1)>' } }
    vi.mocked(benchmarks.userRuns).mockResolvedValue({ items: [injected], total: 1, page: 1, page_size: 20 })
    await wrapper.findAll('button').find(b => b.text() === 'modelBenchmark.reset')!.trigger('click'); await flushPromises()
    resolveFirst({ items: [], total: 0, page: 1, page_size: 20 }); await flushPromises()
    expect(wrapper.text()).toContain('<img src=x onerror=alert(1)>')
    expect(wrapper.find('img').exists()).toBe(false)
  })
  it('loads details without exposing rerun controls and aborts in-flight work on unmount', async () => {
    const wrapper = render(); await flushPromises()
    await wrapper.findAll('button').find(b => b.text() === 'modelBenchmark.details')!.trigger('click'); await flushPromises()
    expect(benchmarks.detail).toHaveBeenCalledWith('user-run', expect.any(AbortSignal))
    expect(wrapper.get('[data-testid="user-record-detail"]').text()).toContain('60.0%')
    expect(wrapper.text()).not.toContain('modelBenchmark.rerun')
    const signal = vi.mocked(benchmarks.userRuns).mock.calls[0][2]!
    wrapper.unmount(); expect(signal.aborted).toBe(true)
  })
  it('shows the remote account, site label and actual test API URL in columns and details', async () => {
    const remote = { ...run, owner_site_id: 'a'.repeat(32), owner_site_name: '甲站',
      owner: { id: 7, email: 'remote@example.com', username: 'remote-user', site_id: 'a'.repeat(32), site_name: '甲站', deleted: false } }
    vi.mocked(benchmarks.userRuns).mockResolvedValue({ items: [remote], total: 1, page: 1, page_size: 20 })
    const wrapper = render(); await flushPromises()
    expect(wrapper.get('[data-testid="record-account"]').text()).toContain('remote@example.com')
    expect(wrapper.get('[data-testid="record-account"]').text()).toContain('remote-user')
    expect(wrapper.get('[data-testid="record-site"]').text()).toContain('甲站')
    expect(wrapper.get('[data-testid="record-api-url"]').text()).toBe('https://example.com/v1')
    await wrapper.findAll('button').find(b => b.text() === 'modelBenchmark.details')!.trigger('click'); await flushPromises()
    expect(wrapper.get('[data-testid="user-record-detail"]').text()).toContain('remote@example.com')
    expect(wrapper.get('[data-testid="user-record-detail"]').text()).toContain('甲站')
    expect(wrapper.get('[data-testid="user-record-detail"]').text()).toContain('https://example.com/v1')
  })
  it('selects identical user IDs on different sites without conflating their record filters', async () => {
    vi.useFakeTimers()
    const site = 'a'.repeat(32)
    vi.mocked(benchmarks.userOptions).mockResolvedValue({ items: [run.owner!, { ...run.owner!, email: 'remote@example.com', site_id: site, site_name: '甲站' }], has_more: false })
    const wrapper = render(); await flushPromises()
    await wrapper.get('[name="account_search"]').setValue('tester')
    await vi.advanceTimersByTimeAsync(300); await flushPromises()
    expect(wrapper.get('[name="account_selection"]').findAll('option').map(option => option.attributes('value'))).toEqual(['', '7', `${site}:7`])
    await wrapper.get('[name="account_selection"]').setValue(`${site}:7`)
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(benchmarks.userRuns).toHaveBeenLastCalledWith(1, { user_id: 7, site_id: site, model: undefined }, expect.any(AbortSignal))
    await wrapper.get('[name="account_selection"]').setValue('7')
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(benchmarks.userRuns).toHaveBeenLastCalledWith(1, { user_id: 7, model: undefined }, expect.any(AbortSignal))
  })
  it('labels old remote accounts as unsynced and renders source labels as plain text', async () => {
    const label = '<img src=x onerror=alert(1)>'
    vi.mocked(benchmarks.userRuns).mockResolvedValue({ items: [{ ...run, owner_site_id: 'a'.repeat(32), owner_site_name: label,
      owner: { id: 7, email: '', username: '', deleted: false, missing: true } }], total: 1, page: 1, page_size: 20 })
    const wrapper = render(); await flushPromises()
    expect(wrapper.get('[data-testid="record-account"]').text()).toContain('modelBenchmark.accountNotSynced')
    expect(wrapper.get('[data-testid="record-account"]').text()).not.toContain('modelBenchmark.deletedAccount')
    expect(wrapper.get('[data-testid="record-site"]').text()).toContain(label)
    expect(wrapper.find('img').exists()).toBe(false)
  })
  it('filters by site alone, keeps that filter on pagination and resets all filters', async () => {
    const site = 'b'.repeat(32)
    vi.mocked(benchmarks.userRuns).mockResolvedValue({ items: [run], total: 41, page: 1, page_size: 20,
      site_options: [{ id: '', name: '' }, { id: site, name: '历史外站' }] })
    const wrapper = render(); await flushPromises()
    expect(wrapper.get('[name="site_selection"]').text()).toContain('历史外站')
    await wrapper.get('[name="site_selection"]').setValue(site)
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(benchmarks.userRuns).toHaveBeenLastCalledWith(1, { user_id: undefined, model: undefined, site_id: site }, expect.any(AbortSignal))
    wrapper.findComponent({ name: 'Pagination' }).vm.$emit('update:page', 2); await flushPromises()
    expect(benchmarks.userRuns).toHaveBeenLastCalledWith(2, { user_id: undefined, model: undefined, site_id: site }, expect.any(AbortSignal))
    await wrapper.get('[name="site_selection"]').setValue('local')
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(benchmarks.userRuns).toHaveBeenLastCalledWith(1, { user_id: undefined, model: undefined, site_id: 'local' }, expect.any(AbortSignal))
    await wrapper.findAll('button').find(b => b.text() === 'modelBenchmark.reset')!.trigger('click'); await flushPromises()
    expect((wrapper.get('[name="site_selection"]').element as HTMLSelectElement).value).toBe('')
    expect(benchmarks.userRuns).toHaveBeenLastCalledWith(1, { user_id: undefined, model: undefined }, expect.any(AbortSignal))
  })
  it('scopes account searches to the selected site and clears stale account selection on site changes', async () => {
    vi.useFakeTimers()
    const site = 'a'.repeat(32)
    vi.mocked(benchmarks.userRuns).mockResolvedValue({ items: [run], total: 1, page: 1, page_size: 20,
      site_options: [{ id: '', name: '' }, { id: site, name: '甲站' }] })
    const wrapper = render(); await flushPromises()
    await wrapper.get('[name="account_search"]').setValue('tester')
    await vi.advanceTimersByTimeAsync(300); await flushPromises()
    await wrapper.get('[name="account_selection"]').setValue('7')
    const oldSignal = vi.mocked(benchmarks.userOptions).mock.calls[0][1]!
    vi.mocked(benchmarks.userOptions).mockResolvedValue({ items: [{ ...run.owner!, site_id: site, site_name: '甲站' }], has_more: false })
    await wrapper.get('[name="site_selection"]').setValue(site)
    expect(oldSignal.aborted).toBe(true)
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeDefined()
    await vi.advanceTimersByTimeAsync(300); await flushPromises()
    expect(benchmarks.userOptions).toHaveBeenLastCalledWith('tester', expect.any(AbortSignal), site)
    await wrapper.get('[name="account_selection"]').setValue(`${site}:7`)
    await wrapper.get('[name="model_search"]').setValue('sol')
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(benchmarks.userRuns).toHaveBeenLastCalledWith(1, { user_id: 7, site_id: site, model: 'sol' }, expect.any(AbortSignal))
  })
})

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import BenchmarkDisplaySettings from '../BenchmarkDisplaySettings.vue'
import { benchmarks, type BenchmarkSchedule, type DisplayConfig } from '@/api/admin/model-benchmarks'

const app = vi.hoisted(() => ({ showError: vi.fn(), showSuccess: vi.fn() }))
vi.mock('@/stores/app', () => ({ useAppStore: () => app }))
vi.mock('vue-i18n', async importOriginal => ({ ...await importOriginal<typeof import('vue-i18n')>(), useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/api/admin/model-benchmarks', () => ({ benchmarks: { displays: vi.fn(), schedules: vi.fn(), createDisplay: vi.fn(), updateDisplay: vi.fn(), deleteDisplay: vi.fn(), refreshDisplay: vi.fn() } }))
const config: DisplayConfig = { id: 'display-1', name: 'Remote scores', source_type: 'remote', schedule_id: null, remote_url: 'https://external.example/api/v1/model-benchmarks/public-results', refresh_minutes: 60, max_results: 20, enabled: true, fetching: false, last_attempt_at: null, last_success_at: null, next_fetch_at: null, last_error: '' }
const plans = [{ id: 'public', name: 'Public plan', model: 'model', public_results: true }, { id: 'private', name: 'Private plan', model: 'model', public_results: false }] as BenchmarkSchedule[]
function render() {
  return mount(BenchmarkDisplaySettings, { global: { stubs: { BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /></div>' }, ConfirmDialog: true } } })
}
enableAutoUnmount(afterEach)
beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(benchmarks.displays).mockResolvedValue({ items: [] })
  vi.mocked(benchmarks.schedules).mockResolvedValue({ items: plans })
  vi.mocked(benchmarks.createDisplay).mockResolvedValue(config)
})

describe('benchmark display configuration', () => {
  it('shows the public endpoint and allows choosing only public local tasks', async () => {
    const wrapper = render()
    await flushPromises()
    expect((wrapper.get('input[readonly]').element as HTMLInputElement).value).toContain('/api/v1/model-benchmarks/public-results')
    await wrapper.findAll('button').find(b => b.text() === 'modelBenchmark.addDisplay')!.trigger('click')
    expect(wrapper.get('[name="display_schedule"]').text()).toContain('Public plan')
    expect(wrapper.get('[name="display_schedule"]').text()).not.toContain('Private plan')
    await wrapper.get('[name="display_name"]').setValue('Featured')
    await wrapper.get('[name="display_schedule"]').setValue('public')
    expect(wrapper.find('[name="max_results"]').exists()).toBe(false)
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(benchmarks.createDisplay).toHaveBeenCalledWith({ name: 'Featured', source_type: 'local', schedule_id: 'public', remote_url: '', refresh_minutes: 60, max_results: 20, enabled: true }, expect.any(AbortSignal))
  })

  it('defaults external feeds to 60 minutes and saves a custom interval', async () => {
    const wrapper = render()
    await flushPromises()
    await wrapper.findAll('button').find(b => b.text() === 'modelBenchmark.addDisplay')!.trigger('click')
    await wrapper.get('[name="source_type"]').setValue('remote')
    expect(wrapper.find('[name="max_results"]').exists()).toBe(false)
    expect((wrapper.get('[name="refresh_minutes"]').element as HTMLInputElement).value).toBe('60')
    await wrapper.get('[name="display_name"]').setValue('Another site')
    await wrapper.get('[name="remote_url"]').setValue(config.remote_url)
    await wrapper.get('[name="refresh_minutes"]').setValue(15)
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(benchmarks.createDisplay).toHaveBeenCalledWith({ name: 'Another site', source_type: 'remote', schedule_id: null, remote_url: config.remote_url, refresh_minutes: 15, max_results: 20, enabled: true }, expect.any(AbortSignal))
  })

  it('edits existing sources and requests an immediate server refresh', async () => {
    vi.mocked(benchmarks.displays).mockResolvedValue({ items: [config] })
    const wrapper = render()
    await flushPromises()
    await wrapper.findAll('button').find(b => b.text() === 'modelBenchmark.refreshSource')!.trigger('click')
    await flushPromises()
    expect(benchmarks.refreshDisplay).toHaveBeenCalledWith(config.id, expect.any(AbortSignal))
    await wrapper.findAll('button').find(b => b.text() === 'common.edit')!.trigger('click')
    await wrapper.get('[name="refresh_minutes"]').setValue(30)
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(benchmarks.updateDisplay).toHaveBeenCalledWith(config.id, expect.objectContaining({ refresh_minutes: 30, remote_url: config.remote_url }), expect.any(AbortSignal))
  })

  it('shows cached result warnings and lets an administrator pause the source', async () => {
    vi.mocked(benchmarks.displays).mockResolvedValue({ items: [{ ...config, last_error: 'Source unavailable' }] })
    const wrapper = render()
    await flushPromises()
    expect(wrapper.text()).toContain('modelBenchmark.cachedResultsHint')
    await wrapper.findAll('button').find(b => b.text() === 'modelBenchmark.pauseSchedule')!.trigger('click')
    await flushPromises()
    expect(benchmarks.updateDisplay).toHaveBeenCalledWith(config.id, { enabled: false }, expect.any(AbortSignal))
  })
})

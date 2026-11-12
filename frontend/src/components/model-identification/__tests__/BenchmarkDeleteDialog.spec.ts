import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import BenchmarkDeleteDialog from '../BenchmarkDeleteDialog.vue'
import { benchmarks, type BenchmarkRun } from '@/api/admin/model-benchmarks'

vi.mock('vue-i18n', async importOriginal => ({ ...await importOriginal<typeof import('vue-i18n')>(), useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/api/admin/model-benchmarks', () => ({ benchmarks: { deleteRun: vi.fn() } }))
const run = { id: 'run-one', model: '<script>unsafe</script>', created_at: '2026-11-12T01:00:00Z' } as BenchmarkRun
function render() {
  return mount(BenchmarkDeleteDialog, { props: { run }, global: { stubs: {
    BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /><slot name="footer" /></div>' }
  } } })
}
enableAutoUnmount(afterEach)
beforeEach(() => vi.resetAllMocks())
describe('benchmark deletion confirmation', () => {
  it('waits for confirmation and cancellation never deletes', async () => {
    const wrapper = render()
    expect(wrapper.text()).toContain(run.model)
    expect(wrapper.find('script').exists()).toBe(false)
    expect(benchmarks.deleteRun).not.toHaveBeenCalled()
    await wrapper.findAll('button').find(button => button.text() === 'common.cancel')!.trigger('click')
    expect(wrapper.emitted('close')).toHaveLength(1)
    expect(benchmarks.deleteRun).not.toHaveBeenCalled()
  })
  it('blocks duplicate submissions until the confirmed deletion completes', async () => {
    let resolve!: (value: { deleted: boolean }) => void
    vi.mocked(benchmarks.deleteRun).mockImplementation(() => new Promise(done => { resolve = done }))
    const wrapper = render()
    await wrapper.get('[data-testid="confirm-delete-result"]').trigger('click')
    await wrapper.get('[data-testid="confirm-delete-result"]').trigger('click')
    expect(benchmarks.deleteRun).toHaveBeenCalledTimes(1)
    expect(benchmarks.deleteRun).toHaveBeenCalledWith('run-one', expect.any(AbortSignal))
    expect(wrapper.findAll('button').every(button => button.attributes('disabled') !== undefined)).toBe(true)
    expect(wrapper.emitted('deleted')).toBeUndefined()
    resolve({ deleted: true }); await flushPromises()
    expect(wrapper.emitted('deleted')).toEqual([['run-one']])
    expect(wrapper.emitted('close')).toHaveLength(1)
  })
  it('keeps errors visible and allows retry without reporting success', async () => {
    vi.mocked(benchmarks.deleteRun).mockRejectedValueOnce({ response: { data: { message: '测试尚未结束' } } }).mockResolvedValueOnce({ deleted: true })
    const wrapper = render()
    await wrapper.get('[data-testid="confirm-delete-result"]').trigger('click'); await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toBe('测试尚未结束')
    expect(wrapper.emitted('deleted')).toBeUndefined()
    expect(wrapper.emitted('close')).toBeUndefined()
    await wrapper.get('[data-testid="confirm-delete-result"]').trigger('click'); await flushPromises()
    expect(wrapper.emitted('deleted')).toEqual([['run-one']])
  })
  it('ignores a stale deletion response after switching sources or closing the dialog', async () => {
    let resolve!: (value: { deleted: boolean }) => void
    vi.mocked(benchmarks.deleteRun).mockImplementation(() => new Promise(done => { resolve = done }))
    const wrapper = render()
    await wrapper.get('[data-testid="confirm-delete-result"]').trigger('click')
    const signal = vi.mocked(benchmarks.deleteRun).mock.calls[0][1]!
    await wrapper.setProps({ run: null })
    expect(signal.aborted).toBe(true)
    resolve({ deleted: true }); await flushPromises()
    expect(wrapper.emitted('deleted')).toBeUndefined()
  })
})

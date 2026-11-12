import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import BenchmarkGroupTarget from '../BenchmarkGroupTarget.vue'
import { benchmarks, type ScheduleTarget } from '@/api/admin/model-benchmarks'

vi.mock('@/api/admin/model-benchmarks', () => ({ benchmarks: { groups: vi.fn(), groupModels: vi.fn() } }))
vi.mock('vue-i18n', async original => ({ ...await original<typeof import('vue-i18n')>(), useI18n: () => ({ t: (key: string) => key }) }))
enableAutoUnmount(afterEach)
beforeEach(() => {
  vi.resetAllMocks()
  vi.mocked(benchmarks.groups).mockResolvedValue({ items: [{ id: 1, name: 'First', platform: 'openai' }, { id: 2, name: 'Second', platform: 'openai' }] })
  vi.mocked(benchmarks.groupModels).mockResolvedValue({ models: ['model-a'] })
})
function render(target: ScheduleTarget = { group_id: 0, model: '', effort: 'medium' }) {
  const wrapper = mount(BenchmarkGroupTarget, { props: { modelValue: target, 'onUpdate:modelValue': value => { void wrapper.setProps({ modelValue: value }) } } })
  return wrapper
}
describe('benchmark group target selection', () => {
  it('clears the model on group change and ignores a stale model list', async () => {
    let resolveOld!: (value: { models: string[] }) => void
    vi.mocked(benchmarks.groupModels).mockImplementationOnce(() => new Promise(resolve => { resolveOld = resolve }))
    const wrapper = render()
    await flushPromises()
    await wrapper.get('[name="benchmark_group_id"]').setValue(1)
    await flushPromises()
    const oldSignal = vi.mocked(benchmarks.groupModels).mock.calls[0][1]
    await wrapper.get('[name="benchmark_group_id"]').setValue(2)
    await flushPromises()
    expect(oldSignal?.aborted).toBe(true)
    await wrapper.get('[name="benchmark_group_model"]').setValue('model-a')
    await flushPromises()
    expect(wrapper.emitted('ready')?.at(-1)).toEqual([true])
    resolveOld({ models: ['old-model'] })
    await flushPromises()
    expect(wrapper.text()).not.toContain('old-model')
    expect(wrapper.props('modelValue')).toEqual({ group_id: 2, model: 'model-a', effort: 'medium' })
  })
  it('validates saved targets and blocks unavailable models', async () => {
    const wrapper = render({ group_id: 1, model: 'removed-model', effort: 'high' })
    await flushPromises()
    expect(wrapper.props('modelValue').model).toBe('')
    expect(wrapper.emitted('ready')?.at(-1)).toEqual([false])
    await wrapper.get('[name="benchmark_group_model"]').setValue('model-a')
    await flushPromises()
    expect(wrapper.emitted('ready')?.at(-1)).toEqual([true])
  })
  it('keeps failures visible and allows a retry', async () => {
    vi.mocked(benchmarks.groups).mockRejectedValueOnce(new Error('offline'))
    const wrapper = render()
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toContain('modelBenchmark.groupLoadError')
    await wrapper.get('[role="alert"] button').trigger('click')
    await flushPromises()
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
    expect(wrapper.text()).toContain('First')
  })
})

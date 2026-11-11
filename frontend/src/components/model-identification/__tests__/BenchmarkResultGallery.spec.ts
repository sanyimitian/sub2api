import { afterEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import BenchmarkResultGallery from '../BenchmarkResultGallery.vue'
import type { SharedResult, SharedSchedule } from '@/api/model-benchmarks'
vi.mock('vue-i18n', async importOriginal => ({ ...await importOriginal<typeof import('vue-i18n')>(), useI18n: () => ({ t: (key: string) => key }) }))
const result: SharedResult = { id: 'old', model: 'example-model', effort: 'high', status: 'completed', created_at: '2026-11-12T01:00:00Z', finished_at: '2026-11-12T01:05:00Z', total: 30, completed: 30, passed: 27, pass_rate: 90, elapsed_seconds: 300, categories: { coding: { total: 20, completed: 20, passed: 18 }, math: { total: 5, completed: 5, passed: 5 }, reasoning: { total: 5, completed: 5, passed: 4 } }, suite_day: '2026-11-12', suite_revision: 2, suite_fingerprint: 'a'.repeat(64), usage: { input_tokens: 100, output_tokens: 200, total_tokens: 300 }, api_errors: 0, grading_errors: 0 }
function groups(): SharedSchedule[] { return [{ id: 'first', name: 'First task', model: 'example-model', effort: 'medium', results: [result] }, { id: 'second', name: 'Second task', model: 'example-model', effort: 'medium', results: [{ ...result, id: 'new', finished_at: '2026-11-12T02:00:00Z' }] }] }
function render(items = groups()) { return mount(BenchmarkResultGallery, { props: { items, displayName: 'Selected source' }, global: { stubs: { BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /></div>' } } } }) }
enableAutoUnmount(afterEach)
describe('benchmark result gallery', () => {
  it('renders external HTML and script payloads as text', async () => {
    const items = groups()
    const payload = '<img src=x onerror="alert(1)"><script>alert(2)</script>'
    items[0].name = payload
    items[0].results[0] = { ...result, model: payload, suite_fingerprint: payload }
    const wrapper = render(items)
    expect(wrapper.text()).toContain(payload)
    expect(wrapper.find('img,script,iframe').exists()).toBe(false)
    await wrapper.get('[data-shared-result="old"]').trigger('click')
    expect(wrapper.get('[data-testid="shared-result-detail"]').text()).toContain(payload)
    expect(wrapper.find('img,script,iframe').exists()).toBe(false)
  })
  it('sorts across all tasks, shows rich cards and opens a result summary', async () => {
    const wrapper = render()
    expect(wrapper.findAll('[data-shared-result]').map(card => card.attributes('data-shared-result'))).toEqual(['new', 'old'])
    expect(wrapper.text()).toContain('90.0%')
    expect(wrapper.text()).toContain('18/20')
    expect(wrapper.text()).toContain('modelBenchmark.efforts.high')
    expect(wrapper.find('input[type="range"]').exists()).toBe(true)
    await wrapper.get('[data-shared-result="new"]').trigger('click')
    expect(wrapper.get('[data-testid="shared-result-detail"]').text()).toContain('100 / 200 / 300')
    expect(wrapper.get('[data-testid="shared-result-detail"]').text()).toContain(result.suite_fingerprint)
    await wrapper.setProps({ items: [] })
    await flushPromises()
    expect(wrapper.find('[data-testid="shared-result-detail"]').exists()).toBe(false)
  })
  it('keeps failure and absent usage separate from a zero score', () => {
    const items = groups()
    items[0].results = [{ ...result, status: 'failed', pass_rate: null, usage: undefined, api_errors: 2 }]
    items.splice(1)
    const wrapper = render(items)
    expect(wrapper.text()).toContain('—')
    expect(wrapper.text()).not.toContain('0.0%')
    expect(wrapper.text()).toContain('modelBenchmark.statuses.failed')
    expect(wrapper.text()).toContain('modelBenchmark.errors')
  })
})

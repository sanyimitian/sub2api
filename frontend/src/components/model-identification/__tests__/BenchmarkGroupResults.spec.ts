import { afterEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, mount } from '@vue/test-utils'
import BenchmarkGroupResults from '../BenchmarkGroupResults.vue'
import type { SharedDisplay, SharedResult, SharedSchedule } from '@/api/model-benchmarks'

vi.mock('vue-i18n', async importOriginal => ({ ...await importOriginal<typeof import('vue-i18n')>(), useI18n: () => ({ t: (key: string, values?: Record<string, unknown>) => values ? `${key} ${JSON.stringify(values)}` : key }) }))
const result: SharedResult = { id: 'recent', model: 'model-a', group_id: 1, group_name: 'Local group', effort: 'medium', status: 'completed', created_at: '2026-11-12T01:00:00Z', finished_at: '2026-11-12T02:00:00Z', total: 30, completed: 30, passed: 27, pass_rate: 90, elapsed_seconds: 300, categories: { coding: { total: 20, completed: 20, passed: 18 }, math: { total: 5, completed: 5, passed: 5 }, reasoning: { total: 5, completed: 5, passed: 4 } }, suite_day: '2026-11-12', suite_revision: 2, suite_fingerprint: 'a'.repeat(64) }
const source = (id: string, source_type: 'local' | 'remote' = 'local'): SharedDisplay => ({ id, name: id, source_type, items: [], last_success_at: null, stale: false, pending: false })
const schedule = (id: string, results: SharedResult[]): SharedSchedule => ({ id, name: id, model: result.model, effort: result.effort, results })
function render(items: SharedSchedule[], sources: SharedDisplay[]) {
  return mount(BenchmarkGroupResults, { props: { items, sources }, global: { stubs: { BenchmarkResultGallery: { props: ['items'], template: '<div><span v-for="item in items" :key="item.id"><span v-for="result in item.results" :key="result.id" :data-result-id="result.id">{{ result.model }}</span></span></div>' } } } })
}
enableAutoUnmount(afterEach)

describe('benchmark group browsing', () => {
  it('combines local group schedules, preserves historical groups and separates external group IDs', async () => {
    const wrapper = render([
      schedule('local-one:task', [result, { ...result, id: 'historical', group_id: 2, group_name: 'Previous group', finished_at: '2026-11-11T02:00:00Z' }]),
      schedule('local-two:task', [{ ...result, id: 'second', model: 'model-b', effort: 'high' }, result]),
      schedule('External site:task', [{ ...result, id: 'remote', group_name: 'Local group' }])
    ], [source('local-one'), source('local-two'), source('External site', 'remote')])
    expect(wrapper.findAll('[data-result-group]')).toHaveLength(3)
    expect(wrapper.findAll('[data-result-id="recent"]')).toHaveLength(1)
    const local = wrapper.findAll('[data-result-group]').find(section => section.get('h4').text() === 'Local group')!
    expect(local.get('[data-testid="result-group-summary"]').text()).toContain('model-a / model-b')
    expect(local.text()).toContain('modelBenchmark.loadedResults {"count":2}')
    const remote = wrapper.findAll('[data-result-group-filter]').find(button => button.text() === 'External site · Local group')!
    await remote.trigger('click')
    expect(wrapper.findAll('[data-result-group]')).toHaveLength(1)
    expect(wrapper.findAll('[data-result-id]').map(item => item.attributes('data-result-id'))).toEqual(['remote'])
    await wrapper.get('[data-testid="all-result-groups"]').trigger('click')
    expect(wrapper.findAll('[data-result-group]')).toHaveLength(3)
  })

  it('keeps a selected group as more history arrives and resets when that group is removed', async () => {
    const items = [schedule('local:task', [result])]
    const wrapper = render(items, [source('local')])
    await wrapper.get('[data-result-group-filter]').trigger('click')
    await wrapper.setProps({ items: [...items, schedule('local:other', [{ ...result, id: 'other', group_id: 2, group_name: 'Other group' }]), schedule('local:task', [{ ...result, id: 'older', finished_at: '2026-11-11T02:00:00Z' }])] })
    expect(wrapper.findAll('[data-result-id]').map(item => item.attributes('data-result-id'))).toEqual(['recent', 'older'])
    expect(wrapper.findAll('[data-result-group-filter]')).toHaveLength(2)
    await wrapper.setProps({ items: [schedule('local:other', [{ ...result, id: 'other', group_id: 2, group_name: 'Other group' }])] })
    expect(wrapper.get('[data-testid="all-result-groups"]').attributes('aria-pressed')).toBe('true')
    expect(wrapper.findAll('[data-result-id]').map(item => item.attributes('data-result-id'))).toEqual(['other'])
  })

  it('keeps legacy external results without inventing groups or dropping IDs shared by different tasks', () => {
    const legacy = { ...result, group_id: undefined, group_name: undefined, base_url: 'https://***t.one', url_masked: true }
    const wrapper = render([schedule('external:task-one', [legacy]), schedule('external:task-two', [legacy])], [source('external', 'remote')])
    expect(wrapper.findAll('[data-result-group]')).toHaveLength(1)
    expect(wrapper.get('h4').text()).toBe('external · modelBenchmark.ungroupedResults')
    expect(wrapper.findAll('[data-result-id]')).toHaveLength(2)
  })
})

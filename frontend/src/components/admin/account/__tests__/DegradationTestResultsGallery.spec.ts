import { afterEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, mount } from '@vue/test-utils'
import type { DegradationTestResult } from '@/types'
import DegradationTestResultsGallery from '../DegradationTestResultsGallery.vue'
import DegradationTestResultCard from '../DegradationTestResultCard.vue'

vi.mock('vue-i18n', async (importOriginal) => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({ t: (key: string) => key })
}))
enableAutoUnmount(afterEach)

function result(id: number, status = 'success'): DegradationTestResult {
  return {
    id, plan_id: 1, model_id: 'selected-model', prompt: 'animate a pelican', status,
    response_text: `<!doctype html><html><body><svg><text>Result ${id}</text></svg></body></html>`,
    error_message: status === 'success' ? '' : `API returned HTTP 403, result ${id}`,
    latency_ms: 12345, started_at: new Date(Date.UTC(2026, 9, 24, id)).toISOString(),
    finished_at: '', created_at: ''
  }
}

describe('compact scheduled test result gallery', () => {
  it('renders only the newest ten tiles with automatic sandboxed animation thumbnails', () => {
    const wrapper = mount(DegradationTestResultsGallery, { props: { results: Array.from({ length: 12 }, (_, i) => result(i + 1)) } })
    const tiles = wrapper.findAll('.result-tile')
    expect(tiles).toHaveLength(10)
    expect(tiles[0].attributes('data-testid')).toBe('result-tile-12')
    expect(tiles[9].attributes('data-testid')).toBe('result-tile-3')
    expect(wrapper.findAll('iframe')).toHaveLength(10)
    expect(wrapper.get('iframe').attributes('sandbox')).toBe('allow-scripts')
    expect(wrapper.get('iframe').attributes('srcdoc')).toContain('Content-Security-Policy')
    expect(wrapper.get('iframe').attributes('srcdoc')).toContain('Result 12')
    expect(wrapper.find('[data-testid="result-detail"]').exists()).toBe(false)
  })

  it('uses orange tiles with visible errors and green tiles for successful output', async () => {
    const failed = result(12, 'failed')
    const wrapper = mount(DegradationTestResultsGallery, { props: { results: [result(11), failed] } })
    const failedTile = wrapper.get('[data-testid="result-tile-12"]')
    expect(failedTile.classes()).toContain('border-amber-500')
    expect(failedTile.text()).toContain('API returned HTTP 403')
    expect(failedTile.find('iframe').exists()).toBe(false)
    expect(wrapper.get('[data-testid="result-tile-11"]').classes()).toContain('border-emerald-500')
    await failedTile.trigger('click')
    expect(wrapper.getComponent(DegradationTestResultCard).props('result')).toMatchObject(failed)
    expect(wrapper.get('[role="alert"]').text()).toBe(failed.error_message)
    await failedTile.trigger('click')
    expect(wrapper.find('[data-testid="result-detail"]').exists()).toBe(false)
  })

  it('keeps playing iframe elements when polling returns the same results and clears pruned details', async () => {
    const wrapper = mount(DegradationTestResultsGallery, { props: { results: [result(12)] } })
    const frame = wrapper.get('iframe').element
    const srcdoc = wrapper.get('iframe').attributes('srcdoc')
    await wrapper.get('[data-testid="result-tile-12"]').trigger('click')
    await wrapper.setProps({ results: [result(12)] })
    expect(wrapper.get('iframe').element).toBe(frame)
    expect(wrapper.get('iframe').attributes('srcdoc')).toBe(srcdoc)
    await wrapper.setProps({ results: [result(13)] })
    expect(wrapper.find('[data-testid="result-detail"]').exists()).toBe(false)
  })

  it('shows text output without attempting an HTML preview', () => {
    const wrapper = mount(DegradationTestResultsGallery, { props: { results: [{ ...result(1), response_text: 'Model answered with plain text' }] } })
    expect(wrapper.get('.result-tile').text()).toContain('Model answered with plain text')
    expect(wrapper.find('iframe').exists()).toBe(false)
  })

  it('opens detailed history without expanding the ten thumbnails on the main page', async () => {
    const wrapper = mount(DegradationTestResultsGallery, { props: { results: Array.from({ length: 24 }, (_, i) => result(i + 1)) } })
    await wrapper.get('[data-testid="details"]').trigger('click')
    expect(wrapper.emitted('details')).toHaveLength(1)
    expect(wrapper.findAll('.result-tile')).toHaveLength(10)
  })

  it('shows at most twenty-four results in the detailed view with no nested details entry', () => {
    const wrapper = mount(DegradationTestResultsGallery, { props: { results: Array.from({ length: 30 }, (_, i) => result(i + 1)), displayLimit: 24, showDetailsButton: false, wrap: true } })
    expect(wrapper.findAll('.result-tile')).toHaveLength(24)
    expect(wrapper.find('[data-testid="details"]').exists()).toBe(false)
    expect(wrapper.get('[aria-label="admin.degradationTests.allResults"]').classes()).toContain('flex-wrap')
  })
})

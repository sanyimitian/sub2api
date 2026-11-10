import { afterEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, mount } from '@vue/test-utils'
import IdentificationGallery from '../IdentificationGallery.vue'
import type { DegradationTestResult } from '@/types'

vi.mock('vue-i18n', async (importOriginal) => ({
  ...await importOriginal<typeof import('vue-i18n')>(), useI18n: () => ({ t: (key: string) => key }) }))
enableAutoUnmount(afterEach)
function result(id: number): DegradationTestResult {
  const date = new Date(Date.UTC(2026, 10, 10, 0, id)).toISOString()
  return {
    id,
    plan_id: 1,
    model_id: 'test-model',
    prompt: 'pelican',
    response_text: '<svg><text>Result ' + id + '</text></svg>',
    status: 'success',
    error_message: '',
    latency_ms: 1500,
    started_at: date,
    finished_at: date,
    created_at: date
  }
}
const stubs = {
  BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /></div>' },
  DegradationTestResultCard: {
    props: ['result'],
    template: '<div class="detail">{{ result.id }}</div>'
  }
}
describe('shared pelican history', () => {
  it('shows the effort saved with each result, including model default and disabled reasoning', async () => {
    const wrapper = mount(IdentificationGallery, {
      props: {
        results: [
          { ...result(4), reasoning_effort: 'xhigh' },
          { ...result(3), reasoning_effort: 'low' },
          { ...result(2), reasoning_effort: 'none', status: 'failed' },
          result(1)
        ],
        groupName: 'Group'
      },
      global: { stubs }
    })
    const efforts = () => wrapper.findAll('[data-testid="artwork-reasoning-effort"]').map((item) => item.text())
    expect(efforts()).toEqual([
      'modelIdentification.effort: modelIdentification.effortXhigh',
      'modelIdentification.effort: modelIdentification.effortLow',
      'modelIdentification.effort: modelIdentification.effortNone',
      'modelIdentification.effort: modelIdentification.defaultEffort'
    ])
    await wrapper.setProps({ groupName: 'Renamed group' })
    expect(efforts()[1]).toBe('modelIdentification.effort: modelIdentification.effortLow')
  })
  it('keeps the newest 24 results and scrolls to the oldest with the slider', async () => {
    const wrapper = mount(IdentificationGallery, {
      props: { results: Array.from({ length: 27 }, (_, i) => result(i + 1)), groupName: 'Group' },
      global: { stubs }
    })
    const track = wrapper.get('.gallery-track')
    Object.defineProperties(track.element, {
      scrollWidth: { value: 6800 },
      clientWidth: { value: 600 },
      scrollLeft: { value: 0, writable: true }
    })
    await track.trigger('scroll')
    expect(wrapper.findAll('.artwork-card')).toHaveLength(24)
    expect(wrapper.get('iframe').attributes('srcdoc')).toContain('Result 27')
    expect(wrapper.get('iframe').attributes('sandbox')).toBe('allow-scripts')
    expect(wrapper.get('iframe').attributes('srcdoc')).toContain("connect-src 'none'")
    expect(wrapper.findAll('iframe').length).toBeLessThan(10)
    await wrapper.get('input[type="range"]').setValue('6200')
    expect(track.element.scrollLeft).toBe(6200)
    expect(
      wrapper.findAll('iframe').some((frame) => frame.attributes('srcdoc')?.includes('Result 4'))
    ).toBe(true)
    expect(
      wrapper.findAll('iframe').some((frame) => frame.attributes('srcdoc')?.includes('Result 27'))
    ).toBe(false)
    await wrapper.findAll('.artwork-card')[23].trigger('click')
    expect(wrapper.get('.detail').text()).toBe('4')
  })
  it('keeps the same iframe alive after a refresh and shows failures honestly', async () => {
    const wrapper = mount(IdentificationGallery, {
      props: { results: [result(1)], groupName: 'Group' },
      global: { stubs }
    })
    const frame = wrapper.get('iframe').element
    await wrapper.setProps({ results: [{ ...result(1) }] })
    expect(wrapper.get('iframe').element).toBe(frame)
    await wrapper.setProps({
      results: [{ ...result(2), status: 'failed', error_message: 'test failed' }]
    })
    expect(wrapper.find('iframe').exists()).toBe(false)
    expect(wrapper.text()).toContain('test failed')
  })
})

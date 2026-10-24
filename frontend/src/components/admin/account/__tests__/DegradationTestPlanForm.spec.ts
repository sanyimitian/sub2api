import { afterEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, mount } from '@vue/test-utils'
import DegradationTestPlanForm from '../DegradationTestPlanForm.vue'
import { DEFAULT_DEGRADATION_TEST_PROMPT } from '@/utils/degradationTestPreview'
import type { DegradationTestPlan } from '@/types'

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
enableAutoUnmount(afterEach)

describe('scheduled generation plan form', () => {
  it('starts with the pelican prompt and saves edited prompt/model with a daily interval', async () => {
    const wrapper = mount(DegradationTestPlanForm, { props: { modelOptions: [{ value: 'model-a', label: 'Model A' }] } })
    expect(wrapper.get('textarea').element.value).toBe(DEFAULT_DEGRADATION_TEST_PROMPT)
    await wrapper.get('input[list]').setValue('model-a')
    await wrapper.get('textarea').setValue('画一只骑车的鹈鹕，返回完整 HTML')
    await wrapper.get('select[aria-label]').setValue('d')
    await wrapper.get('input[type="number"]').setValue(2)
    await wrapper.get('form').trigger('submit')
    expect(wrapper.emitted('submit')?.[0]?.[0]).toMatchObject({
      model_id: 'model-a', cron_expression: '@every 48h', prompt: '画一只骑车的鹈鹕，返回完整 HTML', max_results: 24, enabled: true
    })
  })

  it('preserves an existing prompt and custom cron schedule when editing', async () => {
    const initial = { model_id: 'old-model', prompt: 'Draw an SVG animation', cron_expression: '0 9 * * 1', max_results: 20, enabled: false } as DegradationTestPlan
    const wrapper = mount(DegradationTestPlanForm, { props: { modelOptions: [], initial } })
    expect(wrapper.get('textarea').element.value).toBe(initial.prompt)
    await wrapper.get('form').trigger('submit')
    expect(wrapper.emitted('submit')?.[0]?.[0]).toMatchObject({ ...initial, max_results: 24 })
    expect(wrapper.get('input[readonly]').element.value).toBe('24')
  })

  it('requires a nonblank generation prompt and excludes account recovery settings', async () => {
    const wrapper = mount(DegradationTestPlanForm, { props: { modelOptions: [] } })
    await wrapper.get('input[list]').setValue('manual-model')
    await wrapper.get('textarea').setValue('   ')
    await wrapper.get('form').trigger('submit')
    expect(wrapper.emitted('submit')).toBeUndefined()
    expect(wrapper.get('[role="alert"]').text()).toContain('invalidPlan')
    await wrapper.get('textarea').setValue('Draw an SVG animation')
    await wrapper.get('form').trigger('submit')
    expect(wrapper.emitted('submit')?.[0]?.[0]).not.toHaveProperty('auto_recover')
    expect(wrapper.text()).not.toContain('autoRecover')
  })

  it('rejects invalid interval lengths instead of silently using a different schedule', async () => {
    const wrapper = mount(DegradationTestPlanForm, { props: { modelOptions: [] } })
    await wrapper.get('input[list]').setValue('manual-model')
    await wrapper.get('input[type="number"]').setValue(0)
    await wrapper.get('form').trigger('submit')
    expect(wrapper.emitted('submit')).toBeUndefined()
    expect(wrapper.get('[role="alert"]').text()).toContain('invalidInterval')
  })
})

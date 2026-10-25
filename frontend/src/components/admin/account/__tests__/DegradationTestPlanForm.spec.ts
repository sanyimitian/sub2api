import { afterEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, mount } from '@vue/test-utils'
import DegradationTestPlanForm from '../DegradationTestPlanForm.vue'
import { DEFAULT_DEGRADATION_TEST_PROMPT } from '@/utils/degradationTestPreview'
import type { DegradationTestPlan } from '@/types'

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
enableAutoUnmount(afterEach)

describe('scheduled generation plan form', () => {
  it('uses a ten minute timeout by default and saves edited minutes as seconds', async () => {
    const wrapper = mount(DegradationTestPlanForm, { props: { modelOptions: [] } })
    expect(wrapper.get<HTMLInputElement>('[data-testid="timeout-minutes"]').element.value).toBe('10')
    await wrapper.get('input[list]').setValue('model')
    await wrapper.get('form').trigger('submit')
    expect(wrapper.emitted('submit')?.[0]?.[0]).toMatchObject({ timeout_seconds: 600 })
    await wrapper.get('[data-testid="timeout-minutes"]').setValue(30)
    await wrapper.get('form').trigger('submit')
    expect(wrapper.emitted('submit')?.[1]?.[0]).toMatchObject({ timeout_seconds: 1800 })
  })

  it.each([0, 31])('rejects an execution timeout of %s minutes', async minutes => {
    const wrapper = mount(DegradationTestPlanForm, { props: { modelOptions: [] } })
    await wrapper.get('input[list]').setValue('model')
    await wrapper.get('[data-testid="timeout-minutes"]').setValue(minutes)
    await wrapper.get('form').trigger('submit')
    expect(wrapper.emitted('submit')).toBeUndefined()
    expect(wrapper.get('[role="alert"]').text()).toContain('invalidTimeout')
  })

  it('preserves an existing timeout when editing', async () => {
    const initial = { model_id: 'model', prompt: 'Draw SVG', cron_expression: '@every 30m', timeout_seconds: 90 } as DegradationTestPlan
    const wrapper = mount(DegradationTestPlanForm, { props: { modelOptions: [], initial } })
    expect(wrapper.get<HTMLInputElement>('[data-testid="timeout-minutes"]').element.value).toBe('1.5')
    await wrapper.get('form').trigger('submit')
    expect(wrapper.emitted('submit')?.[0]?.[0]).toMatchObject({ timeout_seconds: 90 })
  })

  it.each(['high', 'xhigh'])('saves the selected %s reasoning effort with the plan', async effort => {
    const wrapper = mount(DegradationTestPlanForm, { props: { modelOptions: [] } })
    await wrapper.get('input[list]').setValue('reasoning-model')
    await wrapper.get('[data-testid="reasoning-effort"]').setValue(effort)
    await wrapper.get('form').trigger('submit')
    expect(wrapper.emitted('submit')?.[0]?.[0]).toMatchObject({ model_id: 'reasoning-model', reasoning_effort: effort })
  })

  it('preserves the saved effort when editing and can explicitly return to the model default', async () => {
    const initial = { model_id: 'model', prompt: 'Draw SVG', cron_expression: '@every 30m', reasoning_effort: 'xhigh' } as DegradationTestPlan
    const wrapper = mount(DegradationTestPlanForm, { props: { modelOptions: [], initial } })
    expect(wrapper.get<HTMLSelectElement>('[data-testid="reasoning-effort"]').element.value).toBe('xhigh')
    await wrapper.get('form').trigger('submit')
    expect(wrapper.emitted('submit')?.[0]?.[0]).toMatchObject({ reasoning_effort: 'xhigh' })
    await wrapper.get('[data-testid="reasoning-effort"]').setValue('')
    await wrapper.get('form').trigger('submit')
    expect(wrapper.emitted('submit')?.[1]?.[0]).toMatchObject({ reasoning_effort: '' })
  })

  it('keeps older plans without a reasoning effort on the model default', async () => {
    const initial = { model_id: 'old-model', prompt: 'Draw SVG', cron_expression: '@every 30m' } as DegradationTestPlan
    const wrapper = mount(DegradationTestPlanForm, { props: { modelOptions: [], initial } })
    expect(wrapper.get<HTMLSelectElement>('[data-testid="reasoning-effort"]').element.value).toBe('')
    await wrapper.get('form').trigger('submit')
    expect(wrapper.emitted('submit')?.[0]?.[0]).toMatchObject({ reasoning_effort: '' })
  })

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

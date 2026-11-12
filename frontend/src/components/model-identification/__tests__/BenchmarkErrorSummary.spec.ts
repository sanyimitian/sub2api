import { afterEach, describe, expect, it } from 'vitest'
import { enableAutoUnmount, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import BenchmarkErrorSummary from '../BenchmarkErrorSummary.vue'
import type { BenchmarkRun } from '@/api/admin/model-benchmarks'
import zh from '@/i18n/locales/zh/modelBenchmark'
import en from '@/i18n/locales/en/modelBenchmark'

function render(result: Partial<BenchmarkRun>, locale = 'zh') {
  return mount(BenchmarkErrorSummary, { props: { result: { status: 'failed', ...result } },
    global: { plugins: [createI18n({ legacy: false, locale, messages: { zh: { modelBenchmark: zh }, en: { modelBenchmark: en } } })] } })
}
enableAutoUnmount(afterEach)

describe('benchmark error explanations', () => {
  it.each([[524, '超时'], [504, '超时'], [429, '限流'], [401, '认证或权限异常'], [403, '认证或权限异常'], [502, '错误']])('explains HTTP %s and the affected question count', (status, reason) => {
    const wrapper = render({ api_errors: 2, api_error_details: [{ code: 'http_error', http_status: status, count: 2 }] })
    expect(wrapper.text()).toContain(`被测 API 返回 HTTP ${status}`)
    expect(wrapper.text()).toContain(reason)
    expect(wrapper.text()).toContain('影响 2 道题')
    expect(wrapper.text()).toContain('本次不生成最终通过率')
  })
  it('keeps API failures, grading failures and incomplete answers distinct', () => {
    const wrapper = render({ api_errors: 1, grading_errors: 2, incomplete_responses: 3,
      api_error_details: [{ code: 'response_error', count: 1 }] })
    expect(wrapper.text()).toContain('被测 API 返回的响应状态、格式或内容异常，影响 1 道题')
    expect(wrapper.text()).toContain('评分环境异常，影响 2 道题')
    expect(wrapper.text()).toContain('被测 API 未返回完整答案（截断、过滤或无最终答案），影响 3 道题')
  })
  it('handles old feeds without inventing HTTP codes or showing unknown fields', () => {
    const wrapper = render({ api_errors: 2, api_error_details: [{ code: 'http_error', http_status: 524, count: 1,
      message: 'private-secret <script>alert(1)</script>' }] } as Partial<BenchmarkRun>)
    expect(wrapper.text()).toContain('HTTP 524')
    expect(wrapper.text()).toContain('影响 1 道题；此记录未提供具体错误码')
    expect(wrapper.text()).not.toContain('private-secret')
    expect(wrapper.find('script').exists()).toBe(false)
    expect(render({ api_errors: 1 }).text()).not.toContain('HTTP')
  })
  it('does not blame the API for cancellation or add errors to ordinary wrong answers', () => {
    const wrapper = render({ status: 'cancelled', api_errors: 1, api_error_details: [{ code: 'cancelled', count: 1 }] })
    expect(wrapper.text()).toContain('测试已停止')
    expect(wrapper.text()).not.toContain('被测 API')
    expect(render({ status: 'completed', api_errors: 0, grading_errors: 0 }).find('[data-testid="benchmark-error-summary"]').exists()).toBe(false)
  })
  it('provides the same explanation in English', () => {
    expect(render({ api_errors: 2, api_error_details: [{ code: 'http_error', http_status: 524, count: 2 }] }, 'en').text())
      .toContain('The tested API returned HTTP 524 (request or upstream response timeout), affecting 2 question(s).')
  })
})

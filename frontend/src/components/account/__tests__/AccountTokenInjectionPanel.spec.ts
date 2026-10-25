import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import AccountTokenInjectionPanel from '../AccountTokenInjectionPanel.vue'
import type { Account } from '@/types'
import type { TokenInjectionEvent, TokenInjectionResult, TokenInjectionSample } from '@/api/admin/accountTokenInjection'

const { stream, copy } = vi.hoisted(() => ({ stream: vi.fn(), copy: vi.fn() }))
vi.mock('@/api/admin/accountTokenInjection', () => ({ streamTokenInjectionTest: stream }))
vi.mock('@/composables/useClipboard', () => ({ useClipboard: () => ({ copyToClipboard: copy }) }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

const account = { id: 377, type: 'apikey', platform: 'openai' } as Account
const sample: TokenInjectionSample = {
  index: 1, repeat: false, local_tokens: 16, input_tokens: 22438,
  cache_read_tokens: 20000, cache_write_tokens: 0, output_tokens: 1,
  extra_tokens: 22422, first_token_ms: 123, duration_ms: 200,
  response_model: 'gpt-test', raw_usage: { input_tokens: 22438 }
}
const report: TokenInjectionResult = {
  account_id: 377, requested_model: 'alias', upstream_model: 'gpt-test', protocol: 'responses',
  tokenizer: 'o200k_base', status: 'stable', estimate_tokens: 22422, slope: 1,
  max_residual: 0, repeat_delta: 0, warnings: [], samples: [sample]
}

describe('AccountTokenInjectionPanel', () => {
  let wrapper: VueWrapper
  beforeEach(() => {
    stream.mockReset()
    copy.mockReset()
    wrapper = mount(AccountTokenInjectionPanel, {
      props: { account, model: 'alias' },
      global: { stubs: { Select: true } }
    })
  })
  afterEach(() => wrapper.unmount())

  it('renders the estimate, raw usage and copies the complete report', async () => {
    stream.mockImplementation(async (_account, _model, _protocol, event: (value: TokenInjectionEvent) => void) => {
      event({ type: 'injection_start', data: report })
      event({ type: 'injection_progress', data: { index: 1, total: 5 } })
      event({ type: 'injection_sample', data: sample })
      event({ type: 'injection_complete', data: report })
    })
    await wrapper.get('button').trigger('click')
    await flushPromises()
    expect(stream).toHaveBeenCalledWith(377, 'alias', 'auto', expect.any(Function), expect.any(AbortSignal))
    expect(wrapper.text()).toContain('22,422')
    expect(wrapper.get('tbody').text()).toContain('22,438')
    expect(wrapper.get('pre').text()).toContain('"input_tokens": 22438')
    expect(wrapper.emitted('running')).toEqual([[true], [false]])
    await wrapper.findAll('button')[1].trigger('click')
    expect(copy).toHaveBeenCalledWith(JSON.stringify(report, null, 2), expect.any(String))
  })

  it('does not display an estimate when the samples are inconclusive', async () => {
    stream.mockImplementation(async (_a, _m, _p, event: (value: TokenInjectionEvent) => void) => {
      event({ type: 'injection_complete', data: { ...report, status: 'inconclusive', estimate_tokens: null, warnings: ['unstable_usage'] } })
    })
    await wrapper.get('button').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('admin.accounts.tokenInjection.inconclusive')
    expect(wrapper.text()).toContain('admin.accounts.tokenInjection.unstableUsage')
    expect(wrapper.find('.text-3xl').exists()).toBe(false)
  })

  it('stops and ignores late events or errors from a cancelled request', async () => {
    let callback!: (event: TokenInjectionEvent) => void
    let reject!: (error: Error) => void
    let signal!: AbortSignal
    stream.mockImplementation((_a, _m, _p, event, abortSignal) => {
      callback = event
      signal = abortSignal
      return new Promise((_resolve, rejectPromise) => { reject = rejectPromise })
    })
    await wrapper.get('button').trigger('click')
    expect(wrapper.get('[role="status"]').exists()).toBe(true)
    await wrapper.get('button').trigger('click')
    expect(signal.aborted).toBe(true)
    callback({ type: 'injection_complete', data: report })
    reject(new Error('late failure'))
    await flushPromises()
    expect(wrapper.text()).toContain('admin.accounts.tokenInjection.cancelled')
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
    expect(wrapper.find('table').exists()).toBe(false)
    expect(wrapper.emitted('running')).toEqual([[true], [false]])
  })

  it('aborts when model changes and discards the previous model results', async () => {
    let signal!: AbortSignal
    stream.mockImplementation((_a, _m, _p, event, abortSignal) => {
      signal = abortSignal
      event({ type: 'injection_sample', data: sample })
      return new Promise(() => {})
    })
    await wrapper.get('button').trigger('click')
    expect(wrapper.find('table').exists()).toBe(true)
    await wrapper.setProps({ model: 'different-model' })
    expect(signal.aborted).toBe(true)
    expect(wrapper.find('table').exists()).toBe(false)
    expect(wrapper.find('[role="status"]').exists()).toBe(false)
  })

  it('aborts the request when the panel closes', async () => {
    let signal!: AbortSignal
    stream.mockImplementation((_a, _m, _p, _event, abortSignal) => {
      signal = abortSignal
      return new Promise(() => {})
    })
    await wrapper.get('button').trigger('click')
    wrapper.unmount()
    expect(signal.aborted).toBe(true)
  })

  it('shows a failed request without a successful estimate', async () => {
    stream.mockRejectedValue(new Error('Upstream returned HTTP 503; test stopped'))
    await wrapper.get('button').trigger('click')
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toContain('HTTP 503')
    expect(wrapper.find('.text-3xl').exists()).toBe(false)
  })
})

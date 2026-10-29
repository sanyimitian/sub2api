import { describe, expect, it } from 'vitest'
import {
  buildModelMaxEffortSupport,
  normalizeModelMaxEffortSupport
} from '../openaiModelMaxEffortSupport'

describe('OpenAI model max-effort support', () => {
  it('defaults missing and malformed values to supported', () => {
    expect(normalizeModelMaxEffortSupport(undefined)).toEqual({})
    expect(normalizeModelMaxEffortSupport(['gpt-6-astra'])).toEqual({})
    expect(normalizeModelMaxEffortSupport({ 'gpt-6-astra': true, invalid: 'false' })).toEqual({})
  })

  it('keeps explicit false values only for configured request-side model IDs', () => {
    expect(
      buildModelMaxEffortSupport(
        { 'gpt-6-astra': false, 'gpt-5.6-sol': true, stale: false },
        ['gpt-6-astra', 'gpt-5.6-sol']
      )
    ).toEqual({ 'gpt-6-astra': false })
  })
})

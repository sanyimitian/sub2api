import { describe, expect, it } from 'vitest'
import { parseDailyTokenThresholdB, formatTokensInBillions } from '../downstreamAssessment'

describe('parseDailyTokenThresholdB', () => {
  it.each([
    ['1', 1000000000], ['0.5', 500000000], ['1.25', 1250000000],
    [' 2.5 ', 2500000000], ['100', 100000000000],
    ['1B', 1000000000], ['0.5b', 500000000], ['1.25 B', 1250000000],
    ['0.000000001', 1], ['1.000000001', 1000000001],
    ['9007199.254740991', Number.MAX_SAFE_INTEGER]
  ])('parses %s as %d tokens exactly', (input, tokens) => {
    expect(parseDailyTokenThresholdB(input)).toBe(tokens)
  })

  it.each(['', ' ', '0', '-1', '-1B', 'NaN', 'Infinity', '1e9', '1TB', '1,000', '500M', '1K', '0.0000000001', '9007199.254740992', '1.00000000001B', '9'.repeat(101)])('rejects invalid threshold %s', input => {
    expect(parseDailyTokenThresholdB(input)).toBeNull()
  })
})

describe('formatTokensInBillions', () => {
  it.each([
    [0, '0.00'], [1, '0.00'], [1000, '0.00'],
    [1000000, '0.00'], [500000000, '0.50'], [1000000000, '1.00'],
    [1250000000, '1.25'], [6999999999, '7.00'], [7000000000, '7.00'],
    [1234567890, '1.23'], [1235000000, '1.24'], [1000000000000, '1,000.00']
  ])('always formats %d tokens in B as %s', (tokens, expected) => {
    expect(formatTokensInBillions(tokens, 'en')).toBe(expected)
  })
})

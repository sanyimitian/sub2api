import { describe, expect, it } from 'vitest'
import { benchmarkScoreClass, benchmarkCategoryScore, benchmarkScoreBarClass } from '../benchmarkScore'

describe('benchmark score thresholds', () => {
  it.each([[0, 'red'], [59.99, 'red'], [60, 'yellow'], [69.99, 'yellow'], [70, 'emerald'], [100, 'emerald']] as const)('colors %s using unrounded thresholds', (score, color) => {
    expect(benchmarkScoreClass(score)).toContain(`text-${color}-600`)
    expect(benchmarkScoreBarClass(score)).toContain(`bg-${color}-500`)
  })
  it('keeps errors and unavailable results neutral', () => {
    for (const score of [null, undefined, NaN]) expect(benchmarkScoreClass(score)).toContain('text-gray-500')
    expect(benchmarkScoreClass(0, 'failed')).toContain('text-gray-500')
    expect(benchmarkScoreClass(0, 'running')).toContain('text-gray-500')
    expect(benchmarkCategoryScore(0, 0)).toBeNull()
    expect(benchmarkCategoryScore(3, 5)).toBe(60)
  })
})

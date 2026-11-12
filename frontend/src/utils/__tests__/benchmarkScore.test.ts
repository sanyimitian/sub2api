import { describe, expect, it } from 'vitest'
import { benchmarkScoreClass, benchmarkCategoryScore, benchmarkScoreBarClass, formatBenchmarkPassRate } from '../benchmarkScore'

describe('benchmark score thresholds', () => {
  it('formats final scores consistently without treating unavailable results as zero', () => {
    expect(formatBenchmarkPassRate(0, 'completed')).toBe('0.0%')
    expect(formatBenchmarkPassRate(59.99, 'completed')).toBe('60.0%')
    for (const status of ['queued', 'running', 'cancelling', 'cancelled', 'failed', 'interrupted']) {
      expect(formatBenchmarkPassRate(70, status)).toBe('—')
    }
    for (const score of [null, undefined, NaN, Infinity]) {
      expect(formatBenchmarkPassRate(score, 'completed')).toBe('—')
    }
  })
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

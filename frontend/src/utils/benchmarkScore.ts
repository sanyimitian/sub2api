export function formatBenchmarkPassRate(score: number | null | undefined, status: string): string {
  return status === 'completed' && typeof score === 'number' && Number.isFinite(score)
    ? `${score.toFixed(1)}%`
    : '—'
}

/** Apply thresholds to the original score, never to its rounded display value. */
export function benchmarkScoreClass(score: number | null | undefined, status = 'completed'): string {
  if (status !== 'completed' || typeof score !== 'number' || !Number.isFinite(score)) return 'text-gray-500 dark:text-gray-400'
  if (score < 60) return 'text-red-600 dark:text-red-400'
  if (score < 70) return 'text-yellow-600 dark:text-yellow-400'
  return 'text-emerald-600 dark:text-emerald-400'
}

export function benchmarkCategoryScore(passed: number, total: number): number | null {
  return total > 0 ? passed / total * 100 : null
}

export function benchmarkScoreBarClass(score: number | null, status = 'completed'): string {
  if (status !== 'completed' || score === null || !Number.isFinite(score)) return 'bg-gray-400 dark:bg-gray-600'
  if (score < 60) return 'bg-red-500'
  if (score < 70) return 'bg-yellow-500'
  return 'bg-emerald-500'
}

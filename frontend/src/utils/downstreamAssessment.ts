export const TOKENS_PER_BILLION = 1_000_000_000

// The input is always in B. Convert to whole tokens exactly for the API without
// rounding, so 1 means 1B and 0.5 means 500 million tokens. An explicit B is optional.
export function parseDailyTokenThresholdB(value: string): number | null {
  if (value.length > 100) return null
  const match = value.trim().match(/^(\d+)(?:\.(\d+))?\s*B?$/i)
  if (!match) return null
  const fraction = match[2] ?? ''
  const numerator = BigInt(match[1] + fraction) * BigInt(TOKENS_PER_BILLION)
  const denominator = 10n ** BigInt(fraction.length)
  if (numerator % denominator !== 0n) return null
  const tokens = numerator / denominator
  return tokens > 0n && tokens <= BigInt(Number.MAX_SAFE_INTEGER) ? Number(tokens) : null
}

// Display usage in B with two decimals. Qualification still uses raw token counts.
export function formatTokensInBillions(tokens: number, locale: string): string {
  return new Intl.NumberFormat(locale, { minimumFractionDigits: 2, maximumFractionDigits: 2 }).format(tokens / TOKENS_PER_BILLION)
}

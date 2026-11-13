// Ten 500-second bands: green → yellow → orange → red.
// Keep complete Tailwind classes so both theme palettes are included in production builds.
const durationClasses = [
  'text-[hsl(120_100%_27%)] dark:text-[hsl(120_85%_65%)]',
  'text-[hsl(100_100%_28%)] dark:text-[hsl(100_85%_65%)]',
  'text-[hsl(80_100%_29%)] dark:text-[hsl(80_85%_65%)]',
  'text-[hsl(60_100%_30%)] dark:text-[hsl(60_85%_65%)]',
  'text-[hsl(50_100%_36%)] dark:text-[hsl(50_85%_65%)]',
  'text-[hsl(40_100%_40%)] dark:text-[hsl(40_85%_65%)]',
  'text-[hsl(30_100%_44%)] dark:text-[hsl(30_85%_65%)]',
  'text-[hsl(20_100%_46%)] dark:text-[hsl(20_85%_65%)]',
  'text-[hsl(10_100%_46%)] dark:text-[hsl(10_85%_65%)]',
  'text-[hsl(0_100%_45%)] dark:text-[hsl(0_85%_65%)]',
] as const

/** Use unrounded seconds; 4500 seconds and above stay in the final red band. */
export function benchmarkDurationClass(seconds: number | null | undefined): string {
  if (typeof seconds !== 'number' || !Number.isFinite(seconds) || seconds < 0) {
    return 'text-gray-500 dark:text-gray-400'
  }
  return durationClasses[Math.min(Math.floor(seconds / 500), durationClasses.length - 1)]
}

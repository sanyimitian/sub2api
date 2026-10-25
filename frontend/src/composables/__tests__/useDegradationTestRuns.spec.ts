import { describe, expect, it } from 'vitest'
import { useDegradationTestRuns } from '../useDegradationTestRuns'
import type { DegradationTestPlan } from '@/types'

const plan = (id: number, running_until: string | null = null) => ({ id, running_until } as DegradationTestPlan)

describe('shared degradation execution locks', () => {
  it('prevents a second entry point from submitting and rejects stale snapshots across a new claim', () => {
    const status = useDegradationTestRuns()
    const panel = useDegradationTestRuns()
    const current = plan(901)
    const staleFence = status.fence()
    expect(status.begin(current)).toBe(true)
    expect(panel.begin(current)).toBe(false)
    panel.observe([current], staleFence)
    expect(panel.isRunning(current)).toBe(true)
    const claimed = plan(901, new Date(Date.now() + 600000).toISOString())
    status.accepted(claimed)
    panel.observe([current], staleFence)
    expect(panel.isRunning(current)).toBe(true)
    panel.observe([current], panel.fence())
    expect(status.isRunning(claimed)).toBe(false)
  })

  it('keeps ambiguous submissions locked until a successful snapshot and lets expired leases recover', () => {
    const runs = useDegradationTestRuns()
    const current = plan(902)
    runs.begin(current)
    runs.uncertain(current.id)
    expect(runs.isRunning(current)).toBe(true)
    runs.observe([plan(902, new Date(Date.now() + 600000).toISOString())], runs.fence())
    expect(runs.isRunning(current)).toBe(true)
    expect(runs.isRunning(current, Date.now() + 700000)).toBe(false)
  })
})

import { reactive } from 'vue'
import type { DegradationTestPlan } from '@/types'

interface RunState {
  revision: number
  submitting: boolean
  // Undefined means the submit response was lost and a fresh snapshot is needed.
  runningUntil: string | null | undefined
}

// Both test entry points share claims, so opening history cannot submit twice.
const states = reactive(new Map<number, RunState>())
let revision = 0

export function useDegradationTestRuns() {
  function isRunning(plan: DegradationTestPlan, now = Date.now()): boolean {
    const state = states.get(plan.id)
    if (state?.submitting || (state && state.runningUntil === undefined)) return true
    const lease = state ? state.runningUntil : plan.running_until
    return !!lease && new Date(lease).getTime() > now
  }

  function begin(plan: DegradationTestPlan): boolean {
    if (isRunning(plan)) return false
    states.set(plan.id, { revision: ++revision, submitting: true, runningUntil: undefined })
    return true
  }

  function accepted(plan: DegradationTestPlan) {
    states.set(plan.id, { revision: ++revision, submitting: false, runningUntil: plan.running_until ?? null })
  }

  function uncertain(planId: number) {
    states.set(planId, { revision: ++revision, submitting: false, runningUntil: undefined })
  }

  function observe(plans: DegradationTestPlan[], fence: number) {
    for (const plan of plans) {
      const previous = states.get(plan.id)
      if (previous && (previous.submitting || previous.revision > fence)) continue
      states.set(plan.id, { revision: ++revision, submitting: false, runningUntil: plan.running_until ?? null })
    }
  }

  return { isRunning, begin, accepted, uncertain, observe, fence: () => revision }
}

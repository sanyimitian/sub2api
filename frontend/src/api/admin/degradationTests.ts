/**
 * Admin Model Quality Tests API endpoints
 * Handles scheduled test plan management for scheduled model generation quality testing
 */

import { apiClient } from '../client'
import type {
  DegradationTestPlan,
  DegradationTestResult,
  CreateDegradationTestPlanRequest,
  UpdateDegradationTestPlanRequest
} from '@/types'

/**
 * List all scheduled test plans for an account
 * @param accountId - Account ID
 * @returns List of scheduled test plans
 */
export async function listByAccount(accountId: number): Promise<DegradationTestPlan[]> {
  const { data } = await apiClient.get<DegradationTestPlan[]>(
    `/admin/accounts/${accountId}/degradation-test-plans`
  )
  return data ?? []
}

/**
 * Create a new scheduled test plan
 * @param req - Plan creation request
 * @returns Created plan
 */
export async function create(req: CreateDegradationTestPlanRequest): Promise<DegradationTestPlan> {
  const { data } = await apiClient.post<DegradationTestPlan>(
    '/admin/degradation-test-plans',
    req
  )
  return data
}

/**
 * Update an existing scheduled test plan
 * @param id - Plan ID
 * @param req - Fields to update
 * @returns Updated plan
 */
export async function update(id: number, req: UpdateDegradationTestPlanRequest): Promise<DegradationTestPlan> {
  const { data } = await apiClient.put<DegradationTestPlan>(
    `/admin/degradation-test-plans/${id}`,
    req
  )
  return data
}

/**
 * Delete a scheduled test plan
 * @param id - Plan ID
 */
export async function deletePlan(id: number): Promise<void> {
  await apiClient.delete(`/admin/degradation-test-plans/${id}`)
}

/**
 * List test results for a plan
 * @param planId - Plan ID
 * @param limit - Optional max number of results to return
 * @returns List of test results
 */
export async function listResults(planId: number, limit?: number): Promise<DegradationTestResult[]> {
  const { data } = await apiClient.get<DegradationTestResult[]>(
    `/admin/degradation-test-plans/${planId}/results`,
    {
      params: limit ? { limit } : undefined
    }
  )
  return data ?? []
}

/** Execute a saved plan immediately and return the recorded result. */
export async function run(planId: number): Promise<DegradationTestResult> {
  const { data } = await apiClient.post<DegradationTestResult>(
    `/admin/degradation-test-plans/${planId}/run`,
    undefined,
    { timeout: 310000 }
  )
  return data
}

export const degradationTestsAPI = {
  listByAccount,
  create,
  update,
  delete: deletePlan,
  listResults,
  run
}

export default degradationTestsAPI

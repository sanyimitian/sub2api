import { apiClient } from './client'
import type { DegradationTestPlan, DegradationTestResult } from '@/types'

export interface IdentificationGroup {
  display_group_id?: number | null
  group_id: number
  name: string
  platform: string
  plan: DegradationTestPlan & { group_id: number }
  latest_result_id: number
  result_count: number
}
export interface IdentificationInput {
  display_group_id: number
  endpoint: string
  api_key: string
  protocol: string
  model_id: string
  interval_minutes: number
  timeout_seconds: number
  reasoning_effort: string
  enabled: boolean
}
export interface IdentificationAdminConfig {
  display_group_id: number | null
  name: string
  endpoint: string
  api_key: string
  protocol: string
}
export const modelIdentificationAPI = {
  async config(id: number, signal?: AbortSignal): Promise<IdentificationAdminConfig> {
    const { data } = await apiClient.get('/admin/model-identification/plans/' + id, { signal })
    return data
  },
  async groups(signal?: AbortSignal): Promise<IdentificationGroup[]> {
    const { data } = await apiClient.get('/model-identification/groups', { signal })
    return data ?? []
  },
  async results(id: number, signal?: AbortSignal): Promise<DegradationTestResult[]> {
    const { data } = await apiClient.get('/model-identification/plans/' + id + '/results', {
      signal
    })
    return data ?? []
  },
  async save(id: number | null, input: IdentificationInput): Promise<DegradationTestPlan> {
    const url = '/admin/model-identification/plans'
    const { data } = id
      ? await apiClient.put(url + '/' + id, input)
      : await apiClient.post(url, input)
    return data
  },
  async run(id: number): Promise<DegradationTestPlan> {
    const { data } = await apiClient.post('/admin/model-identification/plans/' + id + '/run')
    return data
  },
  async delete(id: number): Promise<void> {
    await apiClient.delete('/admin/model-identification/plans/' + id)
  }
}

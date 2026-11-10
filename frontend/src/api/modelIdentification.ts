import axios from 'axios'
import { apiClient } from './client'
import { getAPIBaseURL } from './url'
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
export interface IdentificationSource {
  source_url: string
  read_only: boolean
}
// 公开读取单独使用无认证客户端，不附带本站认证令牌。
const publicClient = axios.create({ baseURL: getAPIBaseURL(), withCredentials: false, timeout: 35000 })
export const modelIdentificationAPI = {
  async source(signal?: AbortSignal): Promise<IdentificationSource> {
    const { data } = await publicClient.get('/model-identification/source', { signal })
    if (data.code !== 0 || !data.data) throw new Error('无法读取鉴定来源配置')
    return data.data
  },
  async saveSource(source_url: string): Promise<IdentificationSource> {
    const { data } = await apiClient.put('/admin/model-identification/source', { source_url }, { timeout: 40000 })
    return data
  },
  async config(id: number, signal?: AbortSignal): Promise<IdentificationAdminConfig> {
    const { data } = await apiClient.get('/admin/model-identification/plans/' + id, { signal })
    return data
  },
  async groups(signal?: AbortSignal): Promise<IdentificationGroup[]> {
    const { data } = await publicClient.get('/model-identification/groups', { signal })
    if (data.code !== 0 || !Array.isArray(data.data)) throw new Error('无法读取鉴定结果')
    return data.data
  },
  async results(id: number, signal?: AbortSignal, admin = false): Promise<DegradationTestResult[]> {
    if (admin) {
      const { data } = await apiClient.get('/admin/model-identification/plans/' + id + '/results', { signal })
      return data ?? []
    }
    const { data } = await publicClient.get('/model-identification/plans/' + id + '/results', { signal })
    if (data.code !== 0 || !Array.isArray(data.data)) throw new Error('无法读取鉴定结果')
    return data.data
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

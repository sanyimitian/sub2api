import { apiClient } from '../client'

export type DownstreamAssessmentSortBy = 'total_tokens' | 'daily_average'

export interface DownstreamAutoRateTier {
  min_tokens: number
  rate_multiplier: number
}

export interface DownstreamAutoRateConfig {
  enabled: boolean
  mode: 'daily' | 'weekly'
  group_ids: number[]
  tiers: DownstreamAutoRateTier[]
  last_run_date?: string
}

export async function getDownstreamAutoRateConfig(): Promise<DownstreamAutoRateConfig> {
  const { data } = await apiClient.get<DownstreamAutoRateConfig>('/admin/usage/downstream-assessment/auto-rate')
  return data
}

export async function saveDownstreamAutoRateConfig(config: DownstreamAutoRateConfig): Promise<void> {
  await apiClient.put('/admin/usage/downstream-assessment/auto-rate', config)
}

export interface DownstreamAssessmentParams {
  // Selects users only; their usage is aggregated across all groups.
  group_id: number
  start_date: string
  end_date: string
  daily_threshold: number // Whole tokens; the page converts its B input exactly.
  page: number
  page_size: number
  sort_by?: DownstreamAssessmentSortBy
  sort_order?: 'asc' | 'desc'
}

export interface DownstreamAssessmentUser {
  user_id: number
  email: string
  username: string
  total_requests: number
  total_tokens: number
  daily_average: number
  qualified: boolean
  last_used_at: string | null // Latest usage across all groups and dates.
}

export interface DownstreamAssessmentResult {
  items: DownstreamAssessmentUser[]
  total: number
  qualified_count: number
  unqualified_count: number
  total_tokens: number
  days: number
  daily_threshold: number
  page: number
  page_size: number
  pages: number
}

export async function getDownstreamAssessment(
  params: DownstreamAssessmentParams,
  options?: { signal?: AbortSignal }
): Promise<DownstreamAssessmentResult> {
  const { data } = await apiClient.get<DownstreamAssessmentResult>('/admin/usage/downstream-assessment', {
    params,
    signal: options?.signal
  })
  return data
}

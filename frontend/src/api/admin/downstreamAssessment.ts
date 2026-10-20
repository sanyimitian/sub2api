import { apiClient } from '../client'

export interface DownstreamAssessmentParams {
  // Selects users only; their usage is aggregated across all groups.
  group_id: number
  start_date: string
  end_date: string
  daily_threshold: number // Whole tokens; the page converts its B input exactly.
  page: number
  page_size: number
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

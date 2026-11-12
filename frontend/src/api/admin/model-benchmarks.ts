import { apiClient } from '../client'
import type { ResultPage, SharedDisplay, SharedSchedule } from '../model-benchmarks'

export type Category = 'coding' | 'math' | 'reasoning'
export type Counts = Record<Category, number>
export type CodingDifficulty = 'medium' | 'hard'
export type CodingCounts = Record<CodingDifficulty, number>
export type Effort = 'low' | 'medium' | 'high' | 'xhigh'
export type RunStatus = 'queued' | 'running' | 'cancelling' | 'cancelled' | 'completed' | 'failed' | 'interrupted'
export interface Suite {
  coding_counts?: CodingCounts
  id: string
  day: string
  revision: number
  fingerprint: string
  bank_version: string
  counts: Counts
  total: number
  questions: { id: string; category: Category; title: string; difficulty: string }[]
}
export interface BenchmarkState {
  resources?: {
    blocked: boolean
    status: 'ok' | 'warning' | 'blocked' | 'unavailable'
    message: string
    host_memory_percent?: number
    worker_memory_percent?: number | null
    memory_percent?: number
    disk_free_gb?: number
    thresholds?: { memory_stop_percent: number; memory_resume_percent: number; disk_stop_gb: number; disk_resume_gb: number; disk_warn_gb: number }
  }
  bank_version?: string
  bank_update?: BankUpdate
  coding_counts?: CodingCounts
  coding_capacities?: CodingCounts
  retention_days?: number
  environment?: { status: 'preparing' | 'failed' | 'ready'; stage: string; progress: number; attempt: number; message: string; next_retry_at: string | null }
  active_run?: { id: string; status: RunStatus } | null
  execution?: ExecutionSettings
  quota?: { limit: number; used: number; remaining: number; next_available_at: string | null } | null
  ready: boolean
  message: string
  counts: Counts
  capacities: Counts
  timezone: string
  suite: Suite | null
}
export interface BankUpdate {
  status: 'idle' | 'checking' | 'ready' | 'current' | 'failed' | 'activated'
  message: string
  checked_at: string | null
  candidate: { version: string; counts: Counts; coding_counts: CodingCounts; added: number; changed: number; removed: number; skipped?: number } | null
}
export interface QuestionResult {
  http_status?: number
  api_error_code?: BenchmarkAPIErrorDetail['code']
  difficulty?: string
  index: number
  id: string
  category: Category
  title: string
  status: 'passed' | 'failed' | 'api_error' | 'grading_error' | 'response_incomplete'
  incomplete_reason?: 'max_output_tokens' | 'content_filter' | 'no_answer' | 'unknown'
  passed: boolean
  score: number
  message: string
  elapsed_seconds: number
  answer?: string
  answer_truncated?: boolean
  tests_passed?: number
  test_count?: number
}
export interface BenchmarkAPIErrorDetail {
  code: 'http_error' | 'request_error' | 'response_error' | 'cancelled' | 'unknown'
  http_status?: number
  count: number
}
export interface BenchmarkRun {
  group_id?: number | null
  group_name?: string
  owner_site_name?: string
  owner_site_id?: string
  owner_user_id?: number
  owner?: BenchmarkOwner
  coding_difficulties?: Record<CodingDifficulty, { total: number; passed: number; completed: number }>
  queue_position?: number | null
  finished_at?: string | null
  id: string
  suite_id: string
  suite_day: string
  suite_revision: number
  suite_fingerprint: string
  rerun_of: string | null
  schedule_id?: string | null
  scheduled_for?: string | null
  base_url: string
  model: string
  effort: Effort
  status: RunStatus
  created_at: string
  elapsed_seconds: number
  total: number
  completed: number
  passed: number
  pass_rate: number | null
  categories: Record<Category, { total: number; passed: number; completed: number }>
  usage: { input_tokens: number; output_tokens: number; total_tokens: number }
  error: string
  api_errors: number
  api_error_details?: BenchmarkAPIErrorDetail[]
  grading_errors: number
  incomplete_responses?: number
  results?: QuestionResult[]
  suite?: Suite
}
export interface CreateBenchmark {
  suite_id: string
  base_url: string
  api_key: string
  model: string
  effort: Effort
}
export interface BenchmarkSchedule {
  group_id?: number | null
  group_name?: string
  active_run_id?: string | null
  public_results: boolean
  id: string
  name: string
  base_url: string
  model: string
  effort: Effort
  cron_expression: string
  enabled: boolean
  timezone: string
  next_run_at: string | null
  last_attempt_at: string | null
  last_run_id: string | null
  last_error: string
  created_at: string
  updated_at: string
}
export interface ScheduleSettings {
  public_results: boolean
  name: string
  cron_expression: string
  enabled: boolean
}
export interface BenchmarkGroup { id: number; name: string; platform: string }
export interface ScheduleTarget { group_id: number; model: string; effort: Effort }
export type CreateSchedule = ScheduleTarget & ScheduleSettings
export interface ExecutionSettings { max_concurrency: number; running: number; queued: number; user_testing_enabled?: boolean }
export interface DisplayInput {
  max_results: number
  name: string
  source_type: 'local' | 'remote'
  schedule_id: string | null
  remote_url: string
  refresh_minutes: number
  enabled: boolean
}
export interface DisplayConfig extends DisplayInput {
  id: string
  fetching: boolean
  last_attempt_at: string | null
  last_success_at: string | null
  next_fetch_at: string | null
  last_error: string
}
const root = '/admin/model-benchmarks'
export interface BenchmarkOwner { id: number; email: string; username: string; deleted: boolean; missing?: boolean; site_id?: string; site_name?: string }
export interface BenchmarkSource { source_url: string; site_id: string; has_token: boolean }
export interface BenchmarkSite { id: string; name: string; created_at: string }
export interface BenchmarkSiteOption { id: string; name: string }
export const benchmarks = {
  async groups(signal?: AbortSignal) {
    return (await apiClient.get<{ items: BenchmarkGroup[] }>(`${root}/groups`, { signal })).data
  },
  async groupModels(group_id: number, signal?: AbortSignal) {
    return (await apiClient.get<{ models: string[] }>(`${root}/groups/${group_id}/models`, { signal })).data
  },
  async deleteRun(id: string, signal?: AbortSignal) {
    return (await apiClient.delete<{ deleted: boolean }>(`${root}/runs/${id}`, { signal })).data
  },
  async source(signal?: AbortSignal) { return (await apiClient.get<BenchmarkSource>(root + '/source', { signal })).data },
  async saveSource(source_url: string, token: string, signal?: AbortSignal) { return (await apiClient.put<BenchmarkSource>(root + '/source', { source_url, token }, { signal })).data },
  async sites(signal?: AbortSignal) { return (await apiClient.get<{ items: BenchmarkSite[] }>(root + '/sites', { signal })).data },
  async createSite(name: string, signal?: AbortSignal) { return (await apiClient.post<{ site: BenchmarkSite; token: string }>(root + '/sites', { name }, { signal })).data },
  async revokeSite(id: string, signal?: AbortSignal) { return (await apiClient.delete(root + '/sites/' + id, { signal })).data },
  async showcase(signal?: AbortSignal, cursor?: string) {
    return (await apiClient.get<ResultPage<SharedDisplay>>(`${root}/showcase`, { params: { cursor }, signal })).data
  },
  async results(signal?: AbortSignal, cursor?: string) {
    return (await apiClient.get<ResultPage<SharedSchedule>>(`${root}/results`, { params: { cursor }, signal })).data
  },
  async userRuns(page: number, filters: { user_id?: number; site_id?: string; model?: string }, signal?: AbortSignal) {
    return (await apiClient.get<{ items: BenchmarkRun[]; total: number; page: number; page_size: number; site_options?: BenchmarkSiteOption[] }>(root + '/user-runs', { params: { page, ...filters }, signal })).data
  },
  async userOptions(q: string, signal?: AbortSignal, site_id?: string) {
    return (await apiClient.get<{ items: BenchmarkOwner[]; has_more: boolean }>(root + '/user-options', { params: { q, site_id }, signal })).data
  },
  async updateBank(signal?: AbortSignal) {
    return (await apiClient.post<BankUpdate>(root + '/bank/update', {}, { signal })).data
  },
  async activateBank(version: string, signal?: AbortSignal) {
    return (await apiClient.post<Suite>(root + '/bank/activate', { version }, { signal })).data
  },
  async retryEnvironment(signal?: AbortSignal) {
    return (await apiClient.post<NonNullable<BenchmarkState['environment']>>(root + '/environment/retry', {}, { signal })).data
  },
  async setExecution(max_concurrency: number, signal?: AbortSignal) {
    return (await apiClient.put<ExecutionSettings>(`${root}/execution`, { max_concurrency }, { signal })).data
  },
  async setUserTesting(user_testing_enabled: boolean, signal?: AbortSignal) {
    return (await apiClient.put<ExecutionSettings>(`${root}/execution`, { user_testing_enabled }, { signal })).data
  },
  async displays(signal?: AbortSignal) {
    return (await apiClient.get<{ items: DisplayConfig[] }>(`${root}/displays`, { signal })).data
  },
  async createDisplay(input: DisplayInput, signal?: AbortSignal) {
    return (await apiClient.post<DisplayConfig>(`${root}/displays`, input, { signal })).data
  },
  async updateDisplay(id: string, input: Partial<DisplayInput>, signal?: AbortSignal) {
    return (await apiClient.put<DisplayConfig>(`${root}/displays/${id}`, input, { signal })).data
  },
  async deleteDisplay(id: string, signal?: AbortSignal) {
    return (await apiClient.delete<{ deleted: boolean }>(`${root}/displays/${id}`, { signal })).data
  },
  async refreshDisplay(id: string, signal?: AbortSignal) {
    return (await apiClient.post<DisplayConfig>(`${root}/displays/${id}/refresh`, {}, { signal })).data
  },
  async state(signal?: AbortSignal) {
    return (await apiClient.get<BenchmarkState>(`${root}/state`, { signal })).data
  },
  async configure(counts: Counts, suite_id: string, signal?: AbortSignal, coding_counts?: CodingCounts) {
    return (await apiClient.put<Suite>(`${root}/config`, { counts, suite_id, coding_counts }, { signal })).data
  },
  async refresh(suite_id: string, signal?: AbortSignal) {
    return (await apiClient.post<Suite>(`${root}/suites/refresh`, { suite_id }, { signal })).data
  },
  async create(input: CreateBenchmark, signal?: AbortSignal) {
    return (await apiClient.post<BenchmarkRun>(`${root}/runs`, input, { signal })).data
  },
  async list(page: number, signal?: AbortSignal) {
    return (await apiClient.get<{ items: BenchmarkRun[]; total: number; page?: number; page_size: number }>(`${root}/runs`, { params: { page }, signal })).data
  },
  async detail(id: string, signal?: AbortSignal) {
    return (await apiClient.get<BenchmarkRun>(`${root}/runs/${id}`, { signal })).data
  },
  async rerun(id: string, signal?: AbortSignal) {
    return (await apiClient.post<BenchmarkRun>(`${root}/runs/${id}/rerun`, {}, { signal })).data
  },
  async cancel(id: string, signal?: AbortSignal) {
    return (await apiClient.post<BenchmarkRun>(`${root}/runs/${id}/cancel`, {}, { signal })).data
  },
  async schedules(signal?: AbortSignal) {
    return (await apiClient.get<{ items: BenchmarkSchedule[] }>(`${root}/schedules`, { signal })).data
  },
  async createSchedule(input: CreateSchedule, signal?: AbortSignal) {
    return (await apiClient.post<BenchmarkSchedule>(`${root}/schedules`, input, { signal })).data
  },
  async updateSchedule(id: string, input: Partial<ScheduleSettings> & Partial<ScheduleTarget>, signal?: AbortSignal) {
    return (await apiClient.put<BenchmarkSchedule>(`${root}/schedules/${id}`, input, { signal })).data
  },
  async runSchedule(id: string, signal?: AbortSignal) {
    return (await apiClient.post<BenchmarkRun>(`${root}/schedules/${id}/run`, {}, { signal })).data
  },
  async deleteSchedule(id: string, signal?: AbortSignal) {
    return (await apiClient.delete<{ deleted: boolean }>(`${root}/schedules/${id}`, { signal })).data
  }
}

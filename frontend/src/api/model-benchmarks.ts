import { apiClient } from './client'
import type { BenchmarkRun, BenchmarkState, CreateBenchmark, Effort } from './admin/model-benchmarks'

export type SharedResult = Pick<BenchmarkRun, 'id' | 'model' | 'effort' | 'status' | 'created_at' | 'finished_at' | 'total' | 'completed' | 'passed' | 'pass_rate' | 'categories' | 'suite_day' | 'suite_revision' | 'suite_fingerprint' | 'elapsed_seconds'> & { base_url?: string; url_masked?: boolean; test_source?: 'admin' | 'user' } & Partial<Pick<BenchmarkRun, 'usage' | 'api_errors' | 'api_error_details' | 'grading_errors' | 'incomplete_responses' | 'group_id' | 'group_name'>>
export interface SharedSchedule { id: string; name: string; model: string; effort: Effort; results: SharedResult[] }
export interface SharedDisplay { id: string; name: string; source_type: 'local' | 'remote'; last_success_at: string | null; stale: boolean; pending: boolean; items: SharedSchedule[] }
export interface ResultPage<T> { items: T[]; next_cursor?: string | null }
const root = '/model-benchmarks'
export const userBenchmarks = {
  async state(signal?: AbortSignal) { return (await apiClient.get<BenchmarkState>(`${root}/state`, { signal })).data },
  async showcase(signal?: AbortSignal, cursor?: string) { return (await apiClient.get<ResultPage<SharedDisplay>>(`${root}/showcase`, { params: { cursor }, signal })).data },
  async results(signal?: AbortSignal, cursor?: string) { return (await apiClient.get<ResultPage<SharedSchedule>>(`${root}/results`, { params: { cursor }, signal })).data },
  async create(input: CreateBenchmark, signal?: AbortSignal) { return (await apiClient.post<BenchmarkRun>(`${root}/runs`, input, { signal })).data },
  async list(page: number, signal?: AbortSignal) { return (await apiClient.get<{ items: BenchmarkRun[]; total: number; page?: number; page_size: number }>(`${root}/runs`, { params: { page }, signal })).data },
  async detail(id: string, signal?: AbortSignal) { return (await apiClient.get<BenchmarkRun>(`${root}/runs/${id}`, { signal })).data },
  async rerun(id: string, signal?: AbortSignal) { return (await apiClient.post<BenchmarkRun>(`${root}/runs/${id}/rerun`, {}, { signal })).data },
  async cancel(id: string, signal?: AbortSignal) { return (await apiClient.post<BenchmarkRun>(`${root}/runs/${id}/cancel`, {}, { signal })).data }
}

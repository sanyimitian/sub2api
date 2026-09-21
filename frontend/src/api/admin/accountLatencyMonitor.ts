import { apiClient } from '../client'

export interface AccountLatencyMonitorGroup {
  group_id: number
  enabled: boolean
  latency_threshold_seconds: number
  failure_window_seconds: number
  consecutive_failures: number
  probe_interval_seconds: number
  idle_probe_interval_seconds: number
  probe_timeout_seconds: number
  probe_concurrency: number
  active_account_count: number
  backup_count: number
  always_enabled_account_ids: number[]
  probe_model: string
  probe_prompt: string
  probe_reasoning_effort: string
  switch_cooldown_seconds: number
  immediate_switch_threshold_seconds: number
  recent_issue_window_seconds: number
  always_enabled_temporary_disable_seconds: number
}

export interface AccountLatencyMonitorSettings {
  groups: AccountLatencyMonitorGroup[]
}

export interface AccountLatencyMonitorAccountState {
  account_id: number
  account_priority: number
  anomaly_base: number
  consecutive_failures: number
  recent_issue_count: number
  last_latency_ms?: number
  last_success?: boolean
  last_observed_at?: string
  temporary_disabled_until?: string
}

export interface AccountLatencyMonitorSwitchRecord {
  switched_at: string
  previous_account_ids: number[]
  current_account_ids: number[]
  reason_code: string
  reason: string
}

export interface AccountLatencyMonitorGroupState {
  group_id: number
  active_account_ids: number[]
  backup_account_ids: number[]
  probe_in_progress: boolean
  last_probe_error?: string
  last_probe_at?: string
  last_switch_at?: string
  switch_history: AccountLatencyMonitorSwitchRecord[]
  accounts: AccountLatencyMonitorAccountState[]
}

export async function getSettings(): Promise<AccountLatencyMonitorSettings> {
  const { data } = await apiClient.get<AccountLatencyMonitorSettings>('/admin/account-latency-monitor/settings')
  return data
}

export async function updateSettings(settings: AccountLatencyMonitorSettings): Promise<AccountLatencyMonitorSettings> {
  const { data } = await apiClient.put<AccountLatencyMonitorSettings>('/admin/account-latency-monitor/settings', settings)
  return data
}

export async function getRuntime(): Promise<{ groups: AccountLatencyMonitorGroupState[] }> {
  const { data } = await apiClient.get<{ groups: AccountLatencyMonitorGroupState[] }>('/admin/account-latency-monitor/runtime')
  return data
}

export async function probeGroup(groupID: number): Promise<void> {
  await apiClient.post(`/admin/account-latency-monitor/groups/${groupID}/probe`)
}

export async function activateBestAccounts(groupID: number): Promise<void> {
  await apiClient.post(`/admin/account-latency-monitor/groups/${groupID}/activate-best`)
}

export async function updateAccountAnomalyBase(accountID: number, anomalyBase: number): Promise<void> {
  await apiClient.put(`/admin/account-latency-monitor/accounts/${accountID}/anomaly-base`, {
    anomaly_base: anomalyBase
  })
}

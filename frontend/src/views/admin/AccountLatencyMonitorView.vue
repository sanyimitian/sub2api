<template>
  <AppLayout>
    <main class="mx-auto w-full max-w-[100rem] space-y-5 p-4 sm:p-6">
      <header class="flex flex-col gap-3 border-b border-gray-200 pb-5 dark:border-dark-700 sm:flex-row sm:items-end sm:justify-between">
        <div>
          <h1 class="text-xl font-semibold text-gray-900 dark:text-white">账号延迟监控</h1>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">监控分组内账号的首字响应，异常时切换到已探测的备用账号。</p>
        </div>
        <div class="flex gap-2">
          <button class="btn btn-secondary" :disabled="loading" title="刷新" @click="load"><Icon name="refresh" size="md" :class="loading ? 'animate-spin' : ''" /></button>
          <button class="btn btn-primary" :disabled="saving" @click="save">{{ saving ? '保存中' : '保存配置' }}</button>
        </div>
      </header>

      <section class="grid gap-4 lg:grid-cols-[19rem_minmax(0,1fr)]">
        <aside class="border border-gray-200 bg-white p-3 dark:border-dark-700 dark:bg-dark-800">
          <div class="mb-3 flex items-center justify-between">
            <h2 class="text-sm font-medium text-gray-900 dark:text-white">监控分组</h2>
            <button class="btn btn-secondary btn-sm" title="新增分组" @click="addGroup"><Icon name="plus" size="sm" /></button>
          </div>
          <p v-if="!settings.groups.length" class="py-8 text-center text-sm text-gray-500">尚未选择分组</p>
          <button v-for="group in settings.groups" :key="group.group_id" class="mb-1 flex w-full items-center justify-between px-3 py-2 text-left text-sm" :class="selectedGroupID === group.group_id ? 'bg-primary-50 text-primary-700 dark:bg-primary-900/30 dark:text-primary-300' : 'text-gray-700 hover:bg-gray-50 dark:text-gray-200 dark:hover:bg-dark-700'" @click="selectGroup(group.group_id)">
            <span class="truncate">{{ groupName(group.group_id) }}</span>
            <span class="h-2 w-2 rounded-full" :class="group.enabled ? 'bg-emerald-500' : 'bg-gray-400'" />
          </button>
        </aside>

        <section v-if="selectedGroup" class="border border-gray-200 bg-white p-4 dark:border-dark-700 dark:bg-dark-800">
          <div class="mb-5 flex flex-wrap items-center justify-between gap-3">
            <div class="min-w-52">
              <label class="mb-1 block text-xs text-gray-500">分组</label>
              <select :value="selectedGroup.group_id" class="input w-full" @change="changeSelectedGroup">
                <option v-for="group in groups" :key="group.id" :value="group.id">{{ group.name }}</option>
              </select>
            </div>
            <label class="flex items-center gap-2 text-sm text-gray-700 dark:text-gray-200"><input v-model="selectedGroup.enabled" type="checkbox" class="checkbox" />启用监控</label>
            <button class="btn btn-secondary btn-sm text-red-600" @click="removeGroup">移除</button>
          </div>

          <div class="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
            <label class="block text-sm text-gray-700 dark:text-gray-200">首字阈值（秒）<input v-model.number="selectedGroup.latency_threshold_seconds" type="number" min="1" class="input mt-1 w-full" /></label>
            <label class="block text-sm text-gray-700 dark:text-gray-200">异常统计窗口（秒）<input v-model.number="selectedGroup.failure_window_seconds" type="number" min="1" class="input mt-1 w-full" /></label>
            <label class="block text-sm text-gray-700 dark:text-gray-200">窗口内异常次数<input v-model.number="selectedGroup.consecutive_failures" type="number" min="1" class="input mt-1 w-full" /></label>
            <label class="block text-sm text-gray-700 dark:text-gray-200">备用检测周期（秒）<input v-model.number="selectedGroup.probe_interval_seconds" type="number" min="1" class="input mt-1 w-full" /></label>
            <label class="block text-sm text-gray-700 dark:text-gray-200">无用户请求时检测周期（秒）<input v-model.number="selectedGroup.idle_probe_interval_seconds" type="number" min="1" class="input mt-1 w-full" /></label>
            <label class="block text-sm text-gray-700 dark:text-gray-200">单次探测超时（秒）<input v-model.number="selectedGroup.probe_timeout_seconds" type="number" min="1" class="input mt-1 w-full" /></label>
            <label class="block text-sm text-gray-700 dark:text-gray-200">并发探测账号数<input v-model.number="selectedGroup.probe_concurrency" type="number" min="1" class="input mt-1 w-full" /></label>
            <label class="block text-sm text-gray-700 dark:text-gray-200">当前开启调度账号数（不含长期启用）<input v-model.number="selectedGroup.active_account_count" type="number" min="1" class="input mt-1 w-full" /></label>
            <label class="block text-sm text-gray-700 dark:text-gray-200">备用账号数量<input v-model.number="selectedGroup.backup_count" type="number" min="1" class="input mt-1 w-full" /></label>
            <label class="block text-sm text-gray-700 dark:text-gray-200">调度帐号切换周期（秒）<input v-model.number="selectedGroup.switch_cooldown_seconds" type="number" min="1" class="input mt-1 w-full" /></label>
            <label class="block text-sm text-gray-700 dark:text-gray-200">单次对话超时切换阈值（秒）<input v-model.number="selectedGroup.immediate_switch_threshold_seconds" type="number" min="1" class="input mt-1 w-full" /></label>
            <label class="block text-sm text-gray-700 dark:text-gray-200">最近异常统计窗口（秒）<input v-model.number="selectedGroup.recent_issue_window_seconds" type="number" min="1" class="input mt-1 w-full" /></label>
            <label class="block text-sm text-gray-700 dark:text-gray-200">长期启用账号临时关闭（秒）<input v-model.number="selectedGroup.always_enabled_temporary_disable_seconds" type="number" min="1" class="input mt-1 w-full" /></label>
            <label class="block text-sm text-gray-700 dark:text-gray-200">探测模型<input v-model.trim="selectedGroup.probe_model" class="input mt-1 w-full" /></label>
            <label class="block text-sm text-gray-700 dark:text-gray-200">探测内容<input v-model.trim="selectedGroup.probe_prompt" class="input mt-1 w-full" /></label>
            <label class="block text-sm text-gray-700 dark:text-gray-200">推理强度<select v-model="selectedGroup.probe_reasoning_effort" class="input mt-1 w-full"><option value="low">low（低）</option><option value="medium">medium（中）</option><option value="high">high（高）</option></select></label>
          </div>

          <details class="mt-5 border-t border-gray-100 pt-4 dark:border-dark-700">
            <summary class="cursor-pointer text-sm font-medium text-gray-800 marker:text-gray-400 dark:text-gray-100">长期启用账号</summary>
            <div class="mt-3">
              <p class="mb-3 text-xs text-gray-500">选择的账号通常保持开启调度；触发首字异常条件时会按配置时长临时关闭。</p>
              <div v-if="groupAccountsLoading" class="border border-gray-200 px-3 py-8 text-center text-sm text-gray-500 dark:border-dark-700">正在加载账号...</div>
              <div v-else-if="groupAccounts.length" class="overflow-x-auto border border-gray-200 dark:border-dark-700">
                <table class="w-full min-w-[64rem] text-left text-sm">
                  <thead class="bg-gray-50 text-xs text-gray-500 dark:bg-dark-700/50 dark:text-gray-400"><tr>
                    <th v-for="column in groupAccountSortColumns" :key="column.key" scope="col" :class="column.class" :aria-sort="groupAccountSortAria(column.key)"><button type="button" class="inline-flex items-center gap-1 font-medium hover:text-gray-700 dark:hover:text-gray-200" :title="`按${column.label}排序`" @click="cycleGroupAccountSort(column.key)"><span>{{ column.label }}</span><Icon :name="groupAccountSortIcon(column.key)" size="xs" /></button></th>
                  </tr></thead>
                  <tbody>
                    <tr v-for="account in sortedGroupAccounts" :key="account.id" class="border-t border-gray-100 dark:border-dark-700">
                      <td class="p-3"><input :checked="selectedGroup.always_enabled_account_ids.includes(account.id)" type="checkbox" class="checkbox" :aria-label="`长期启用 ${account.name}`" @change="toggleAlwaysEnabled(account.id, ($event.target as HTMLInputElement).checked)" /></td>
                      <td class="p-3"><a v-if="accountHomepageUrl(account.id)" :href="accountHomepageUrl(account.id)" target="_blank" rel="noopener noreferrer" class="inline-flex max-w-full items-center gap-1 font-medium text-primary-700 hover:text-primary-800 dark:text-primary-300 dark:hover:text-primary-200"><span class="truncate">{{ account.name }}</span><Icon name="externalLink" size="xs" /></a><span v-else class="font-medium text-gray-900 dark:text-white">{{ account.name }}</span></td>
                      <td class="p-3 text-gray-700 dark:text-gray-200">{{ formatRateMultiplier(account.rate_multiplier) }}</td>
                      <td class="p-3"><input :value="accountAnomalyBase(account)" type="number" min="0" :max="maxAnomalyBase" step="1" class="input h-8 w-full" :disabled="isAccountUpdating(account.id)" aria-label="异常值基数" @change="updateAnomalyBase(account, $event)" /></td>
                      <td class="p-3"><input v-model.number="account.priority" type="number" min="1" class="input h-8 w-full" :disabled="isAccountUpdating(account.id)" @change="updateAccountPriority(account)" /></td>
                      <td class="p-3"><input v-model.number="account.concurrency" type="number" min="1" class="input h-8 w-full" :disabled="isAccountUpdating(account.id)" @change="updateAccountConcurrency(account)" /></td>
                      <td class="p-3"><AccountTodayStatsCell :stats="groupTodayStatsByAccountId[String(account.id)] ?? null" :loading="groupTodayStatsLoading" :error="groupTodayStatsError" /></td>
                    </tr>
                  </tbody>
                </table>
                <div class="flex flex-wrap items-center justify-between gap-3 border-t border-gray-100 px-3 py-2 text-xs text-gray-500 dark:border-dark-700 dark:text-gray-400">
                  <span>共 {{ groupAccountsTotal }} 个账号</span>
                  <div class="flex items-center gap-2">
                    <label class="flex items-center gap-2">每页<select v-model.number="groupAccountsPageSize" class="input h-8 w-20 py-1 text-xs" @change="changeGroupAccountsPageSize"><option :value="20">20</option><option :value="50">50</option></select>条</label>
                    <button class="btn btn-secondary btn-sm" :disabled="groupAccountsPage <= 1 || groupAccountsLoading" title="上一页" aria-label="上一页" @click="changeGroupAccountsPage(groupAccountsPage - 1)"><Icon name="chevronLeft" size="sm" /></button>
                    <span class="min-w-20 text-center">第 {{ groupAccountsPage }} / {{ groupAccountsTotalPages }} 页</span>
                    <button class="btn btn-secondary btn-sm" :disabled="groupAccountsPage >= groupAccountsTotalPages || groupAccountsLoading" title="下一页" aria-label="下一页" @click="changeGroupAccountsPage(groupAccountsPage + 1)"><Icon name="chevronRight" size="sm" /></button>
                  </div>
                </div>
              </div>
              <p v-else class="text-sm text-gray-500">该分组暂无账号。</p>
            </div>
          </details>

          <div class="mt-5 border-t border-gray-100 pt-4 dark:border-dark-700">
            <div class="mb-3 flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
              <h3 class="text-sm font-medium text-gray-800 dark:text-gray-100">运行状态</h3>
              <div class="flex flex-wrap gap-2">
                <button class="btn btn-secondary btn-sm" :disabled="monitorOperationBusy || !selectedGroup.enabled" @click="runProbe">
                  <Icon name="beaker" size="sm" :class="probing ? 'animate-pulse motion-reduce:animate-none' : ''" />
                  {{ probing ? '探测中' : '探测帐号' }}
                </button>
                <button class="btn btn-secondary btn-sm" :disabled="monitorOperationBusy || !selectedGroup.enabled" @click="replaceWithBestAccounts">
                  <Icon name="swap" size="sm" />
                  {{ replacingBest ? '更换中' : '一键更换最优帐号' }}
                </button>
              </div>
            </div>
            <p v-if="runtimeForSelected?.last_probe_error" class="mb-3 text-sm text-red-600 dark:text-red-400">最近探测失败：{{ runtimeForSelected.last_probe_error }}</p>
            <div class="grid gap-3 sm:grid-cols-2">
              <div class="border border-gray-200 p-3 text-sm dark:border-dark-700"><span class="text-gray-500">当前开启调度账号</span><p class="mt-1 text-gray-900 dark:text-white">{{ activeAccountNames }}</p></div>
              <div class="border border-gray-200 p-3 text-sm dark:border-dark-700"><span class="text-gray-500">备用账号</span><p class="mt-1 text-gray-900 dark:text-white">{{ accountNames(runtimeForSelected?.backup_account_ids, '等待探测') }}</p></div>
              <div class="border border-gray-200 p-3 text-sm dark:border-dark-700"><span class="text-gray-500">最近探测</span><p class="mt-1 text-gray-900 dark:text-white">{{ formatTime(runtimeForSelected?.last_probe_at) }}</p></div>
              <div class="border border-gray-200 p-3 text-sm dark:border-dark-700"><span class="text-gray-500">最近更换账号</span><p class="mt-1 text-gray-900 dark:text-white">{{ formatTime(runtimeForSelected?.last_switch_at) }}</p></div>
            </div>
            <details v-if="sortedRuntimeAccounts.length" :key="selectedGroupID ?? 0" class="mt-3">
              <summary class="cursor-pointer text-sm font-medium text-gray-800 marker:text-gray-400 dark:text-gray-100">帐号探测结果（{{ sortedRuntimeAccounts.length }}）</summary>
              <div class="mt-2 overflow-x-auto"><table class="w-full min-w-[54rem] text-left text-sm"><thead class="text-xs text-gray-500"><tr><th class="p-2">账号</th><th class="p-2">账号优先级</th><th class="p-2">首字</th><th class="p-2">窗口内异常</th><th class="p-2">{{ recentIssueWindowLabel }}</th><th class="p-2">最近结果</th><th class="p-2">调度状态</th></tr></thead><tbody><tr v-for="state in sortedRuntimeAccounts" :key="state.account_id" class="border-t border-gray-100 dark:border-dark-700"><td class="p-2"><a v-if="accountHomepageUrl(state.account_id)" :href="accountHomepageUrl(state.account_id)" target="_blank" rel="noopener noreferrer" class="border-b border-dotted border-gray-300 font-medium text-gray-900 dark:border-dark-600 dark:text-white">{{ accountName(state.account_id) }}</a><span v-else class="font-medium text-gray-900 dark:text-white">{{ accountName(state.account_id) }}</span></td><td class="p-2">{{ state.account_priority }}</td><td class="p-2" :class="latencyClass(state.last_latency_ms)">{{ state.last_latency_ms == null ? '-' : `${state.last_latency_ms} ms` }}</td><td class="p-2">{{ state.consecutive_failures }}</td><td class="p-2">{{ state.recent_issue_count }}</td><td class="p-2" :class="resultClass(state.last_success)">{{ state.last_success === undefined ? '-' : state.last_success ? '成功' : '失败' }}</td><td class="p-2" :class="state.temporary_disabled_until ? 'text-orange-600 dark:text-orange-400' : 'text-gray-500 dark:text-gray-400'">{{ temporaryDisableText(state.temporary_disabled_until) }}</td></tr></tbody></table></div>
            </details>
            <details v-if="runtimeForSelected?.switch_history?.length" class="mt-4">
              <summary class="cursor-pointer text-sm font-medium text-gray-800 marker:text-gray-400 dark:text-gray-100">帐号切换记录（最近 100 条）</summary>
              <div class="mt-2 overflow-x-auto"><table class="w-full min-w-[48rem] text-left text-sm"><thead class="text-xs text-gray-500"><tr><th class="p-2">时间</th><th class="p-2">关闭帐号</th><th class="p-2">开启帐号</th><th class="p-2">原因</th></tr></thead><tbody><tr v-for="(record, index) in runtimeForSelected.switch_history" :key="`${record.switched_at}-${record.reason_code}-${index}`" class="border-t border-gray-100 dark:border-dark-700"><td class="p-2 whitespace-nowrap">{{ formatTime(record.switched_at) }}</td><td class="p-2">{{ accountNames(record.previous_account_ids, '暂无') }}</td><td class="p-2">{{ accountNames(record.current_account_ids, '暂无') }}</td><td class="p-2">{{ record.reason }}</td></tr></tbody></table></div>
            </details>
          </div>
        </section>
        <section v-else class="flex min-h-80 items-center justify-center border border-dashed border-gray-300 text-sm text-gray-500 dark:border-dark-600">从左侧新增并选择一个分组。</section>
      </section>
    </main>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import AccountTodayStatsCell from '@/components/account/AccountTodayStatsCell.vue'
import { useAppStore } from '@/stores/app'
import { getAll } from '@/api/admin/groups'
import { getBatchTodayStats, list as listAccounts, update as updateAccount } from '@/api/admin/accounts'
import { activateBestAccounts, getRuntime, getSettings, probeGroup, updateAccountAnomalyBase, updateSettings, type AccountLatencyMonitorGroup, type AccountLatencyMonitorGroupState, type AccountLatencyMonitorSettings } from '@/api/admin/accountLatencyMonitor'
import type { AdminGroup, AccountListItem, WindowStats } from '@/types'
import { extractApiErrorMessage } from '@/utils/apiError'
import { sanitizeUrl } from '@/utils/url'

const appStore = useAppStore()
const groups = ref<AdminGroup[]>([])
const settings = ref<AccountLatencyMonitorSettings>({ groups: [] })
const runtime = ref<AccountLatencyMonitorGroupState[]>([])
const groupAccounts = ref<AccountListItem[]>([])
const allGroupAccounts = ref<AccountListItem[]>([])
const knownGroupAccounts = ref(new Map<number, AccountListItem>())
const groupAccountsPage = ref(1)
const groupAccountsPageSize = ref<20 | 50>(20)
const groupAccountsTotal = ref(0)
const groupAccountsTotalPages = ref(1)
const groupAccountsLoading = ref(false)
const groupAccountsRequestSeq = ref(0)
const groupTodayStatsByAccountId = ref<Record<string, WindowStats>>({})
const groupTodayStatsLoading = ref(false)
const groupTodayStatsError = ref<string | null>(null)
type GroupAccountSortKey = 'always_enabled' | 'name' | 'rate_multiplier' | 'anomaly_base' | 'priority' | 'concurrency' | 'today_stats'
type GroupAccountSortOrder = 'asc' | 'desc'
const groupAccountSortColumns: Array<{ key: GroupAccountSortKey; label: string; class: string }> = [
  { key: 'always_enabled', label: '长期启用', class: 'w-28 p-3' },
  { key: 'name', label: '账号', class: 'min-w-48 p-3' },
  { key: 'rate_multiplier', label: '账号计费倍率', class: 'w-36 p-3' },
  { key: 'anomaly_base', label: '异常值基数', class: 'w-36 p-3' },
  { key: 'priority', label: '优先级', class: 'w-32 p-3' },
  { key: 'concurrency', label: '并发数', class: 'w-32 p-3' },
  { key: 'today_stats', label: '今日统计', class: 'min-w-36 p-3' }
]
const groupAccountSortKey = ref<GroupAccountSortKey | null>('priority')
const groupAccountSortOrder = ref<GroupAccountSortOrder>('asc')
const selectedGroupID = ref<number | null>(null)
const loading = ref(false)
const saving = ref(false)
const probeStarting = ref(false)
const replacingBest = ref(false)
const updatingAccountIDs = ref(new Set<number>())
const selectedGroup = computed(() => settings.value.groups.find((group) => group.group_id === selectedGroupID.value))
const runtimeForSelected = computed(() => runtime.value.find((group) => group.group_id === selectedGroupID.value))
const probing = computed(() => probeStarting.value || Boolean(runtimeForSelected.value?.probe_in_progress))
const monitorOperationBusy = computed(() => probing.value || replacingBest.value)
const activeAccountNames = computed(() => accountNames(runtimeForSelected.value?.active_account_ids, '暂无'))
const recentIssueWindowLabel = computed(() => {
  const seconds = selectedGroup.value?.recent_issue_window_seconds ?? 600
  return seconds % 60 === 0 ? `${seconds / 60} 分钟异常` : `${seconds} 秒异常`
})
const sortedRuntimeAccounts = computed(() => [...(runtimeForSelected.value?.accounts ?? [])].sort((left, right) => {
  const thresholdMs = (selectedGroup.value?.latency_threshold_seconds ?? 10) * 1000
  const leftFast = left.last_latency_ms != null && left.last_latency_ms < thresholdMs
  const rightFast = right.last_latency_ms != null && right.last_latency_ms < thresholdMs
  if (leftFast !== rightFast) return leftFast ? -1 : 1
  if (left.recent_issue_count !== right.recent_issue_count) return left.recent_issue_count - right.recent_issue_count
  if (left.account_priority !== right.account_priority) return left.account_priority - right.account_priority
  if (left.last_latency_ms == null || right.last_latency_ms == null) {
    if (left.last_latency_ms == null && right.last_latency_ms != null) return 1
    if (left.last_latency_ms != null && right.last_latency_ms == null) return -1
    return left.account_id - right.account_id
  }
  return left.last_latency_ms - right.last_latency_ms || left.account_id - right.account_id
}))
const sortedGroupAccounts = computed(() => {
  const sortKey = groupAccountSortKey.value
  if (!sortKey) return allGroupAccounts.value.slice((groupAccountsPage.value - 1) * groupAccountsPageSize.value, groupAccountsPage.value * groupAccountsPageSize.value)
  const order = groupAccountSortOrder.value === 'asc' ? 1 : -1
  return allGroupAccounts.value
    .map((account, index) => ({ account, index }))
    .sort((left, right) => {
      const compare = compareGroupAccounts(left.account, right.account, sortKey)
      return compare === 0 ? left.index - right.index : compare * order
    })
    .map(({ account }) => account)
    .slice((groupAccountsPage.value - 1) * groupAccountsPageSize.value, groupAccountsPage.value * groupAccountsPageSize.value)
})

function defaults(groupID: number): AccountLatencyMonitorGroup { return { group_id: groupID, enabled: true, latency_threshold_seconds: 10, failure_window_seconds: 30, consecutive_failures: 2, probe_interval_seconds: 60, idle_probe_interval_seconds: 1800, probe_timeout_seconds: 30, probe_concurrency: 4, active_account_count: 1, backup_count: 2, switch_cooldown_seconds: 600, immediate_switch_threshold_seconds: 40, recent_issue_window_seconds: 600, always_enabled_temporary_disable_seconds: 180, always_enabled_account_ids: [], probe_model: 'gpt-5.6-sol', probe_prompt: 'hi', probe_reasoning_effort: 'low' } }
function normalizeSettings(value: AccountLatencyMonitorSettings): AccountLatencyMonitorSettings {
  return {
    ...value,
    groups: (value.groups ?? []).map((group) => ({
      ...group,
      active_account_count: group.active_account_count > 0 ? group.active_account_count : 1,
      recent_issue_window_seconds: group.recent_issue_window_seconds > 0 ? group.recent_issue_window_seconds : 600,
      always_enabled_temporary_disable_seconds: group.always_enabled_temporary_disable_seconds > 0 ? group.always_enabled_temporary_disable_seconds : 180,
      always_enabled_account_ids: group.always_enabled_account_ids ?? []
    }))
  }
}
function groupName(id: number) { return groups.value.find((group) => group.id === id)?.name ?? `分组 #${id}` }
function accountForID(id: number) { return groupAccounts.value.find((account) => account.id === id) ?? knownGroupAccounts.value.get(id) }
function accountName(id: number) { return accountForID(id)?.name ?? `账号 #${id}` }
function accountHomepageUrl(id: number) {
  const account = accountForID(id)
  if (!account || typeof account.credentials?.base_url !== 'string') return ''
  const baseUrl = sanitizeUrl(account.credentials.base_url)
  return baseUrl ? new URL(baseUrl).origin : ''
}
function formatRateMultiplier(value?: number) { return `${value ?? 1}x` }
const anomalyBaseExtraKey = 'account_latency_monitor_anomaly_base'
const maxAnomalyBase = 2_147_483_647
function accountAnomalyBase(account: AccountListItem) {
  const value = Number(account.extra?.[anomalyBaseExtraKey] ?? 0)
  return Number.isSafeInteger(value) && value >= 0 ? value : 0
}
function compareGroupAccounts(left: AccountListItem, right: AccountListItem, key: GroupAccountSortKey) {
  if (key === 'always_enabled') return Number(isAlwaysEnabled(left.id)) - Number(isAlwaysEnabled(right.id))
  if (key === 'name') return left.name.localeCompare(right.name, 'zh-CN')
  if (key === 'rate_multiplier') return (left.rate_multiplier ?? 1) - (right.rate_multiplier ?? 1)
  if (key === 'anomaly_base') return accountAnomalyBase(left) - accountAnomalyBase(right)
  if (key === 'priority') return left.priority - right.priority
  if (key === 'concurrency') return left.concurrency - right.concurrency
  const leftStats = groupTodayStatsByAccountId.value[String(left.id)] ?? buildDefaultTodayStats()
  const rightStats = groupTodayStatsByAccountId.value[String(right.id)] ?? buildDefaultTodayStats()
  return leftStats.requests - rightStats.requests || leftStats.tokens - rightStats.tokens || leftStats.cost - rightStats.cost
}
function isAlwaysEnabled(id: number) { return selectedGroup.value?.always_enabled_account_ids.includes(id) ?? false }
function groupAccountSortIcon(key: GroupAccountSortKey): 'arrowsUpDown' | 'arrowUp' | 'arrowDown' {
  if (groupAccountSortKey.value !== key) return 'arrowsUpDown'
  return groupAccountSortOrder.value === 'asc' ? 'arrowUp' : 'arrowDown'
}
function groupAccountSortAria(key: GroupAccountSortKey) {
  if (groupAccountSortKey.value !== key) return 'none'
  return groupAccountSortOrder.value === 'asc' ? 'ascending' : 'descending'
}
function cycleGroupAccountSort(key: GroupAccountSortKey) {
  if (groupAccountSortKey.value !== key) {
    groupAccountSortKey.value = key
    groupAccountSortOrder.value = 'asc'
  } else if (groupAccountSortOrder.value === 'asc') {
    groupAccountSortOrder.value = 'desc'
  } else {
    groupAccountSortKey.value = null
  }
  groupAccountsPage.value = 1
}
function isAccountUpdating(id: number) { return updatingAccountIDs.value.has(id) }
function normalizePositiveInteger(value: number) { return Math.max(1, Math.trunc(Number(value) || 1)) }
function normalizeAnomalyBase(value: unknown) { return Math.min(maxAnomalyBase, Math.max(0, Math.trunc(Number(value) || 0))) }
async function updateAnomalyBase(account: AccountListItem, event: Event) {
  if (isAccountUpdating(account.id)) return
  const input = event.target as HTMLInputElement
  const anomalyBase = normalizeAnomalyBase(input.value)
  input.value = String(anomalyBase)
  updatingAccountIDs.value = new Set(updatingAccountIDs.value).add(account.id)
  try {
    await updateAccountAnomalyBase(account.id, anomalyBase)
    account.extra = { ...(account.extra ?? {}), [anomalyBaseExtraKey]: anomalyBase }
    await refreshRuntime()
    appStore.showSuccess(`账号 ${account.name} 的异常值基数已更新`)
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, `更新账号 ${account.name} 的异常值基数失败`))
    if (selectedGroupID.value) await loadGroupAccounts(selectedGroupID.value)
  } finally {
    const pending = new Set(updatingAccountIDs.value)
    pending.delete(account.id)
    updatingAccountIDs.value = pending
  }
}
async function updateAccountPriority(account: AccountListItem) {
  const priority = normalizePositiveInteger(account.priority)
  account.priority = priority
  await updateAccountField(account, { priority })
}
async function updateAccountConcurrency(account: AccountListItem) {
  const concurrency = normalizePositiveInteger(account.concurrency)
  account.concurrency = concurrency
  await updateAccountField(account, { concurrency })
}
async function updateAccountField(account: AccountListItem, updates: { priority: number } | { concurrency: number }) {
  if (isAccountUpdating(account.id)) return
  updatingAccountIDs.value = new Set(updatingAccountIDs.value).add(account.id)
  try {
    const updated = await updateAccount(account.id, updates)
    Object.assign(account, updated)
    appStore.showSuccess(`账号 ${account.name} 已更新`)
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, `更新账号 ${account.name} 失败`))
    if (selectedGroupID.value) await loadGroupAccounts(selectedGroupID.value)
  } finally {
    const pending = new Set(updatingAccountIDs.value)
    pending.delete(account.id)
    updatingAccountIDs.value = pending
  }
}
function accountNames(ids: number[] | undefined, fallback: string) { return ids?.length ? ids.map(accountName).join('、') : fallback }
function formatTime(value?: string) { return value ? new Date(value).toLocaleString() : '暂无' }
function temporaryDisableText(value?: string) { return value ? `临时关闭至 ${new Date(value).toLocaleTimeString()}` : '正常' }
function latencyClass(latency?: number) { if (latency == null) return 'text-gray-500 dark:text-gray-400'; if (latency > 25_000) return 'text-red-600 dark:text-red-400'; if (latency >= (selectedGroup.value?.latency_threshold_seconds ?? 10) * 1000) return 'text-orange-600 dark:text-orange-400'; return 'text-emerald-600 dark:text-emerald-400' }
function resultClass(success?: boolean) { if (success === undefined) return 'text-gray-500 dark:text-gray-400'; return success ? 'text-emerald-600 dark:text-emerald-400' : 'text-red-600 dark:text-red-400' }
async function selectGroup(id: number) {
  selectedGroupID.value = id
  groupAccountsPage.value = 1
  allGroupAccounts.value = []
  groupAccounts.value = []
  knownGroupAccounts.value = new Map()
  groupTodayStatsByAccountId.value = {}
  await loadGroupAccounts(id)
}
async function loadGroupAccounts(id: number): Promise<void> {
  const requestSeq = ++groupAccountsRequestSeq.value
  groupAccountsLoading.value = true
  groupTodayStatsLoading.value = true
  groupTodayStatsError.value = null
  try {
    const accounts = await loadAllGroupAccounts(id)
    if (selectedGroupID.value !== id || requestSeq !== groupAccountsRequestSeq.value) return
    allGroupAccounts.value = accounts
    groupAccounts.value = accounts
    groupAccountsTotal.value = accounts.length
    groupAccountsTotalPages.value = Math.max(Math.ceil(accounts.length / groupAccountsPageSize.value), 1)
    for (const account of accounts) knownGroupAccounts.value.set(account.id, account)
    try {
      const statsChunks = await Promise.all(chunkAccountIDs(accounts.map((account) => account.id), 200).map((accountIDs) => getBatchTodayStats(accountIDs)))
      if (selectedGroupID.value !== id || requestSeq !== groupAccountsRequestSeq.value) return
      const nextStats: Record<string, WindowStats> = {}
      for (const stats of statsChunks) {
        for (const [accountID, accountStats] of Object.entries(stats.stats ?? {})) nextStats[accountID] = accountStats
      }
      groupTodayStatsByAccountId.value = nextStats
    } catch (error) {
      if (selectedGroupID.value === id && requestSeq === groupAccountsRequestSeq.value) {
        groupTodayStatsError.value = extractApiErrorMessage(error, '今日统计加载失败')
        appStore.showError(groupTodayStatsError.value)
      }
    }
  } catch (error) {
    if (selectedGroupID.value === id && requestSeq === groupAccountsRequestSeq.value) {
      appStore.showError(extractApiErrorMessage(error, '账号列表加载失败'))
    }
  } finally {
    if (requestSeq === groupAccountsRequestSeq.value) {
      groupAccountsLoading.value = false
      groupTodayStatsLoading.value = false
    }
  }
}
async function loadAllGroupAccounts(id: number): Promise<AccountListItem[]> {
  const firstPage = await listAccounts(1, 1000, { group: String(id) })
  const remainingPages = await Promise.all(Array.from({ length: Math.max((firstPage.pages ?? 1) - 1, 0) }, (_, index) => listAccounts(index + 2, 1000, { group: String(id) })))
  return [...firstPage.items, ...remainingPages.flatMap((page) => page.items)]
}
async function loadAllGroupAccountIDs(id: number): Promise<number[]> {
  return (await loadAllGroupAccounts(id)).map((account) => account.id)
}
function buildDefaultTodayStats(): WindowStats { return { requests: 0, tokens: 0, cost: 0, standard_cost: 0, user_cost: 0 } }
function chunkAccountIDs(accountIDs: number[], chunkSize: number) {
  const chunks: number[][] = []
  for (let index = 0; index < accountIDs.length; index += chunkSize) chunks.push(accountIDs.slice(index, index + chunkSize))
  return chunks
}
function changeGroupAccountsPage(page: number) {
  if (!selectedGroupID.value || page < 1 || page > groupAccountsTotalPages.value) return
  groupAccountsPage.value = page
}
function changeGroupAccountsPageSize() {
  groupAccountsPage.value = 1
  groupAccountsTotalPages.value = Math.max(Math.ceil(groupAccountsTotal.value / groupAccountsPageSize.value), 1)
}
function changeSelectedGroup(event: Event) {
  const previousID = selectedGroupID.value
  const nextID = Number((event.target as HTMLSelectElement).value)
  if (!previousID || !nextID) return
  const group = settings.value.groups.find((item) => item.group_id === previousID)
  if (!group) return
  if (settings.value.groups.some((item) => item !== group && item.group_id === nextID)) {
    selectedGroupID.value = nextID
    void selectGroup(nextID)
    return
  }
  group.group_id = nextID
  selectedGroupID.value = nextID
  void selectGroup(nextID)
}
function addGroup() { const candidate = groups.value.find((group) => !settings.value.groups.some((item) => item.group_id === group.id)); if (!candidate) return; settings.value.groups.push(defaults(candidate.id)); void selectGroup(candidate.id) }
function removeGroup() { if (!selectedGroupID.value) return; settings.value.groups = settings.value.groups.filter((group) => group.group_id !== selectedGroupID.value); selectedGroupID.value = settings.value.groups[0]?.group_id ?? null; if (selectedGroupID.value) void selectGroup(selectedGroupID.value) }
function toggleAlwaysEnabled(id: number, checked: boolean) { if (!selectedGroup.value) return; const ids = selectedGroup.value.always_enabled_account_ids; selectedGroup.value.always_enabled_account_ids = checked ? [...ids, id] : ids.filter((item) => item !== id) }
async function load() { loading.value = true; try { const [allGroups, savedSettings, currentRuntime] = await Promise.all([getAll(), getSettings(), getRuntime()]); groups.value = allGroups ?? []; settings.value = normalizeSettings(savedSettings); runtime.value = currentRuntime.groups ?? []; if (!selectedGroupID.value && settings.value.groups[0]) await selectGroup(settings.value.groups[0].group_id); else if (selectedGroupID.value) await selectGroup(selectedGroupID.value) } finally { loading.value = false } }
async function save() {
  saving.value = true
  try {
    await reconcileAlwaysEnabledAccounts()
    settings.value = normalizeSettings(await updateSettings(settings.value))
    await load()
    appStore.showSuccess('监控配置已保存并生效')
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, '保存监控配置失败'))
  } finally {
    saving.value = false
  }
}
async function refreshRuntime() {
  const currentRuntime = await getRuntime()
  runtime.value = currentRuntime.groups ?? []
}
async function runProbe() {
  if (!selectedGroupID.value || probing.value) return
  const groupID = selectedGroupID.value
  probeStarting.value = true
  try {
    await probeGroup(groupID)
    await refreshRuntime()
    appStore.showSuccess('当前分组所有账号探测已开始')
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, '探测账号失败'))
  } finally {
    probeStarting.value = false
  }
}
async function replaceWithBestAccounts() {
  if (!selectedGroupID.value || replacingBest.value) return
  const groupID = selectedGroupID.value
  replacingBest.value = true
  try {
    await activateBestAccounts(groupID)
    await Promise.all([refreshRuntime(), selectedGroupID.value === groupID ? loadGroupAccounts(groupID) : Promise.resolve()])
    appStore.showSuccess('已按最近一次探测结果更换最优账号')
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, '更换最优账号失败'))
  } finally {
    replacingBest.value = false
  }
}
async function reconcileAlwaysEnabledAccounts() {
  const groupsWithAccounts = await Promise.all(settings.value.groups.map(async (group) => {
    const accountIDs = await loadAllGroupAccountIDs(group.group_id)
    return [group.group_id, new Set(accountIDs)] as const
  }))
  const accountIDsByGroup = new Map(groupsWithAccounts)
  for (const group of settings.value.groups) {
    const accountIDs = accountIDsByGroup.get(group.group_id)
    if (!accountIDs) continue
    group.always_enabled_account_ids = group.always_enabled_account_ids.filter((id) => accountIDs.has(id))
  }
}
let runtimePollTimer: ReturnType<typeof setInterval> | undefined
onMounted(() => {
  void load()
  runtimePollTimer = setInterval(() => {
    if (runtime.value.some((group) => group.probe_in_progress)) void refreshRuntime()
  }, 2000)
})
onBeforeUnmount(() => {
  if (runtimePollTimer) clearInterval(runtimePollTimer)
})
</script>

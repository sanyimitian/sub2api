<template>
  <AppLayout>
    <main class="mx-auto w-full max-w-7xl space-y-5 p-4 sm:p-6">
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

      <section class="grid gap-4 lg:grid-cols-[21rem_minmax(0,1fr)]">
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
              <select v-model.number="selectedGroup.group_id" class="input w-full" @change="selectGroup(selectedGroup.group_id)">
                <option v-for="group in groups" :key="group.id" :value="group.id">{{ group.name }}</option>
              </select>
            </div>
            <label class="flex items-center gap-2 text-sm text-gray-700 dark:text-gray-200"><input v-model="selectedGroup.enabled" type="checkbox" class="checkbox" />启用监控</label>
            <button class="btn btn-secondary btn-sm text-red-600" @click="removeGroup">移除</button>
          </div>

          <div class="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
            <label class="block text-sm text-gray-700 dark:text-gray-200">首字阈值（秒）<input v-model.number="selectedGroup.latency_threshold_seconds" type="number" min="1" class="input mt-1 w-full" /></label>
            <label class="block text-sm text-gray-700 dark:text-gray-200">异常统计窗口（秒）<input v-model.number="selectedGroup.failure_window_seconds" type="number" min="1" class="input mt-1 w-full" /></label>
            <label class="block text-sm text-gray-700 dark:text-gray-200">连续异常次数<input v-model.number="selectedGroup.consecutive_failures" type="number" min="1" class="input mt-1 w-full" /></label>
            <label class="block text-sm text-gray-700 dark:text-gray-200">备用检测周期（秒）<input v-model.number="selectedGroup.probe_interval_seconds" type="number" min="1" class="input mt-1 w-full" /></label>
            <label class="block text-sm text-gray-700 dark:text-gray-200">无用户请求时检测周期（秒）<input v-model.number="selectedGroup.idle_probe_interval_seconds" type="number" min="1" class="input mt-1 w-full" /></label>
            <label class="block text-sm text-gray-700 dark:text-gray-200">单次探测超时（秒）<input v-model.number="selectedGroup.probe_timeout_seconds" type="number" min="1" class="input mt-1 w-full" /></label>
            <label class="block text-sm text-gray-700 dark:text-gray-200">并发探测账号数<input v-model.number="selectedGroup.probe_concurrency" type="number" min="1" class="input mt-1 w-full" /></label>
            <label class="block text-sm text-gray-700 dark:text-gray-200">备用账号数量<input v-model.number="selectedGroup.backup_count" type="number" min="1" class="input mt-1 w-full" /></label>
            <label class="block text-sm text-gray-700 dark:text-gray-200">探测模型<input v-model.trim="selectedGroup.probe_model" class="input mt-1 w-full" /></label>
            <label class="block text-sm text-gray-700 dark:text-gray-200">探测内容<input v-model.trim="selectedGroup.probe_prompt" class="input mt-1 w-full" /></label>
            <label class="block text-sm text-gray-700 dark:text-gray-200">推理强度<select v-model="selectedGroup.probe_reasoning_effort" class="input mt-1 w-full"><option value="low">low（低）</option><option value="medium">medium（中）</option><option value="high">high（高）</option></select></label>
          </div>

          <div class="mt-5 border-t border-gray-100 pt-4 dark:border-dark-700">
            <label class="mb-2 block text-sm font-medium text-gray-800 dark:text-gray-100">长期启用账号</label>
            <p class="mb-3 text-xs text-gray-500">未选择时，监控会只保留一个开启调度的账号；选择的账号会在切换时保持开启调度。</p>
            <div v-if="groupAccounts.length" class="grid gap-2 sm:grid-cols-2 xl:grid-cols-3">
              <label v-for="account in groupAccounts" :key="account.id" class="flex items-center gap-2 border border-gray-200 px-3 py-2 text-sm dark:border-dark-700">
                <input :checked="selectedGroup.always_enabled_account_ids.includes(account.id)" type="checkbox" class="checkbox" @change="toggleAlwaysEnabled(account.id, ($event.target as HTMLInputElement).checked)" />
                <span class="min-w-0 truncate">{{ account.name }}</span><span class="ml-auto text-xs text-gray-500">优先级 {{ account.priority }}</span>
              </label>
            </div>
            <p v-else class="text-sm text-gray-500">该分组暂无账号。</p>
          </div>

          <div class="mt-5 border-t border-gray-100 pt-4 dark:border-dark-700">
            <h3 class="mb-3 text-sm font-medium text-gray-800 dark:text-gray-100">运行状态</h3>
            <div class="grid gap-3 sm:grid-cols-2">
              <div class="border border-gray-200 p-3 text-sm dark:border-dark-700"><span class="text-gray-500">当前开启调度账号</span><p class="mt-1 text-gray-900 dark:text-white">{{ activeAccountNames }}</p></div>
              <div class="border border-gray-200 p-3 text-sm dark:border-dark-700"><span class="text-gray-500">备用账号</span><p class="mt-1 text-gray-900 dark:text-white">{{ accountNames(runtimeForSelected?.backup_account_ids, '等待探测') }}</p></div>
              <div class="border border-gray-200 p-3 text-sm dark:border-dark-700"><span class="text-gray-500">最近探测</span><p class="mt-1 text-gray-900 dark:text-white">{{ formatTime(runtimeForSelected?.last_probe_at) }}</p></div>
            </div>
            <div v-if="sortedRuntimeAccounts.length" class="mt-3 overflow-x-auto"><table class="w-full text-left text-sm"><thead class="text-xs text-gray-500"><tr><th class="p-2">账号</th><th class="p-2">分组优先级</th><th class="p-2">首字</th><th class="p-2">连续异常</th><th class="p-2">最近结果</th></tr></thead><tbody><tr v-for="state in sortedRuntimeAccounts" :key="state.account_id" class="border-t border-gray-100 dark:border-dark-700"><td class="p-2">{{ accountName(state.account_id) }}</td><td class="p-2">{{ state.group_priority }}</td><td class="p-2" :class="latencyClass(state.last_latency_ms)">{{ state.last_latency_ms == null ? '-' : `${state.last_latency_ms} ms` }}</td><td class="p-2">{{ state.consecutive_failures }}</td><td class="p-2" :class="resultClass(state.last_success)">{{ state.last_success === undefined ? '-' : state.last_success ? '成功' : '失败' }}</td></tr></tbody></table></div>
          </div>
        </section>
        <section v-else class="flex min-h-80 items-center justify-center border border-dashed border-gray-300 text-sm text-gray-500 dark:border-dark-600">从左侧新增并选择一个分组。</section>
      </section>
    </main>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import { getAll } from '@/api/admin/groups'
import { list as listAccounts } from '@/api/admin/accounts'
import { getRuntime, getSettings, updateSettings, type AccountLatencyMonitorGroup, type AccountLatencyMonitorGroupState, type AccountLatencyMonitorSettings } from '@/api/admin/accountLatencyMonitor'
import type { AdminGroup, AccountListItem } from '@/types'

const groups = ref<AdminGroup[]>([])
const settings = ref<AccountLatencyMonitorSettings>({ groups: [] })
const runtime = ref<AccountLatencyMonitorGroupState[]>([])
const groupAccounts = ref<AccountListItem[]>([])
const selectedGroupID = ref<number | null>(null)
const loading = ref(false)
const saving = ref(false)
const selectedGroup = computed(() => settings.value.groups.find((group) => group.group_id === selectedGroupID.value))
const runtimeForSelected = computed(() => runtime.value.find((group) => group.group_id === selectedGroupID.value))
const activeAccountNames = computed(() => accountNames(runtimeForSelected.value?.active_account_ids, '暂无'))
const sortedRuntimeAccounts = computed(() => [...(runtimeForSelected.value?.accounts ?? [])].sort((left, right) => {
  const thresholdMs = (selectedGroup.value?.latency_threshold_seconds ?? 10) * 1000
  const leftFast = left.last_latency_ms != null && left.last_latency_ms < thresholdMs
  const rightFast = right.last_latency_ms != null && right.last_latency_ms < thresholdMs
  if (leftFast !== rightFast) return leftFast ? -1 : 1
  if (left.group_priority !== right.group_priority) return left.group_priority - right.group_priority
  if (left.last_latency_ms == null || right.last_latency_ms == null) {
    if (left.last_latency_ms == null && right.last_latency_ms != null) return 1
    if (left.last_latency_ms != null && right.last_latency_ms == null) return -1
    return left.account_id - right.account_id
  }
  return left.last_latency_ms - right.last_latency_ms || left.account_id - right.account_id
}))

function defaults(groupID: number): AccountLatencyMonitorGroup { return { group_id: groupID, enabled: true, latency_threshold_seconds: 10, failure_window_seconds: 30, consecutive_failures: 2, probe_interval_seconds: 60, idle_probe_interval_seconds: 1800, probe_timeout_seconds: 30, probe_concurrency: 4, backup_count: 2, always_enabled_account_ids: [], probe_model: 'gpt-5.6-sol', probe_prompt: 'hi', probe_reasoning_effort: 'low' } }
function normalizeSettings(value: AccountLatencyMonitorSettings): AccountLatencyMonitorSettings {
  return {
    ...value,
    groups: (value.groups ?? []).map((group) => ({
      ...group,
      always_enabled_account_ids: group.always_enabled_account_ids ?? []
    }))
  }
}
function groupName(id: number) { return groups.value.find((group) => group.id === id)?.name ?? `分组 #${id}` }
function accountName(id: number) { return groupAccounts.value.find((account) => account.id === id)?.name ?? `账号 #${id}` }
function accountNames(ids: number[] | undefined, fallback: string) { return ids?.length ? ids.map(accountName).join('、') : fallback }
function formatTime(value?: string) { return value ? new Date(value).toLocaleString() : '暂无' }
function latencyClass(latency?: number) { if (latency == null) return 'text-gray-500 dark:text-gray-400'; if (latency > 25_000) return 'text-red-600 dark:text-red-400'; if (latency >= (selectedGroup.value?.latency_threshold_seconds ?? 10) * 1000) return 'text-orange-600 dark:text-orange-400'; return 'text-emerald-600 dark:text-emerald-400' }
function resultClass(success?: boolean) { if (success === undefined) return 'text-gray-500 dark:text-gray-400'; return success ? 'text-emerald-600 dark:text-emerald-400' : 'text-red-600 dark:text-red-400' }
async function selectGroup(id: number) {
  selectedGroupID.value = id
  const firstPage = await listAccounts(1, 1000, { group: String(id) })
  const pages = await Promise.all(Array.from({ length: Math.max(firstPage.pages - 1, 0) }, (_, index) => listAccounts(index + 2, 1000, { group: String(id) })))
  if (selectedGroupID.value === id) groupAccounts.value = [...firstPage.items, ...pages.flatMap((page) => page.items)]
}
function addGroup() { const candidate = groups.value.find((group) => !settings.value.groups.some((item) => item.group_id === group.id)); if (!candidate) return; settings.value.groups.push(defaults(candidate.id)); void selectGroup(candidate.id) }
function removeGroup() { if (!selectedGroupID.value) return; settings.value.groups = settings.value.groups.filter((group) => group.group_id !== selectedGroupID.value); selectedGroupID.value = settings.value.groups[0]?.group_id ?? null; if (selectedGroupID.value) void selectGroup(selectedGroupID.value) }
function toggleAlwaysEnabled(id: number, checked: boolean) { if (!selectedGroup.value) return; const ids = selectedGroup.value.always_enabled_account_ids; selectedGroup.value.always_enabled_account_ids = checked ? [...ids, id] : ids.filter((item) => item !== id) }
async function load() { loading.value = true; try { const [allGroups, savedSettings, currentRuntime] = await Promise.all([getAll(), getSettings(), getRuntime()]); groups.value = allGroups ?? []; settings.value = normalizeSettings(savedSettings); runtime.value = currentRuntime.groups ?? []; if (!selectedGroupID.value && settings.value.groups[0]) await selectGroup(settings.value.groups[0].group_id); else if (selectedGroupID.value) await selectGroup(selectedGroupID.value) } finally { loading.value = false } }
async function save() { saving.value = true; try { settings.value = normalizeSettings(await updateSettings(settings.value)); await load() } finally { saving.value = false } }
onMounted(() => { void load() })
</script>

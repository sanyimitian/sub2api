<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Pagination from '@/components/common/Pagination.vue'
import { opsAPI, type OpsSystemLog, type OpsSystemLogQuery, type OpenAISessionPoolSnapshot, type OpenAISessionPoolProbeResult } from '@/api/admin/ops'

const { t, te } = useI18n()
const snapshot = ref<OpenAISessionPoolSnapshot | null>(null)
const groups = ref<OpsSystemLog[]>([])
const expanded = ref(false)
const poolExpanded = ref(false)
const page = ref(1)
const pageSize = ref(10)
const total = ref(0)
const poolError = ref(false)
const historyError = ref(false)
const poolLoading = ref(false)
const historyLoading = ref(false)
const now = ref(Date.now())
const filters = ref<Pick<OpsSystemLogQuery, 'time_range' | 'model'>>({ time_range: '1h' })
let poolRequest = 0
let historyRequest = 0
let refreshTimer: ReturnType<typeof setInterval> | undefined
let clockTimer: ReturnType<typeof setInterval> | undefined

// 以服务器时间为准，避免浏览器时钟偏差导致过早隐藏候选。
const clockOffset = ref(0)
const candidates = computed(() => (snapshot.value?.candidates ?? []).filter(candidate =>
  Date.parse(candidate.expires_at) > now.value + clockOffset.value && (!filters.value.model || candidate.model === filters.value.model)
))
const results = (group: OpsSystemLog): OpenAISessionPoolProbeResult[] =>
  Array.isArray(group.extra?.probe_results) ? group.extra.probe_results : []
const formatTime = (value: string) => new Date(value).toLocaleString()
const reasonLabel = (reason: string) => {
  const key = `admin.ops.switchRecords.pool.reasons.${reason}`
  return te(key) ? t(key) : t('admin.ops.switchRecords.pool.unknownReason')
}
const transportLabel = (value: string) => value === 'http_sse' ? 'HTTP' : value.startsWith('responses_websocket') ? 'WebSocket' : value

async function loadPool() {
  const request = ++poolRequest
  poolLoading.value = true
  try {
    const result = await opsAPI.getOpenAISessionPoolSnapshot()
    if (request !== poolRequest) return
    snapshot.value = result
    now.value = Date.now()
    clockOffset.value = Date.parse(result.observed_at) - now.value
    poolError.value = false
  } catch {
    if (request === poolRequest) { poolError.value = true; snapshot.value = null }
  } finally {
    if (request === poolRequest) poolLoading.value = false
  }
}

async function loadHistory() {
  const request = ++historyRequest
  historyLoading.value = true
  try {
    const response = await opsAPI.listSystemLogs({
      ...filters.value, q: 'openai.session_pool_refill_completed',
      page: expanded.value ? page.value : 1, page_size: expanded.value ? pageSize.value : 1
    })
    if (request !== historyRequest) return
    groups.value = (response.items || []).filter(group => group.message === 'openai.session_pool_refill_completed')
    total.value = response.total
    historyError.value = false
  } catch {
    if (request === historyRequest) { historyError.value = true; groups.value = [] }
  } finally {
    if (request === historyRequest) historyLoading.value = false
  }
}

async function refresh(nextFilters?: Pick<OpsSystemLogQuery, 'time_range' | 'model'>) {
  if (nextFilters) { filters.value = nextFilters; page.value = 1 }
  await Promise.all([loadPool(), loadHistory()])
}

async function toggleHistory() {
  expanded.value = !expanded.value
  page.value = 1
  pageSize.value = 10
  await loadHistory()
}
async function changePage(value: number) { page.value = value; await loadHistory() }
async function changePageSize(value: number) { pageSize.value = value; page.value = 1; await loadHistory() }

defineExpose({ refresh })
onMounted(() => {
  refreshTimer = setInterval(() => { if (!poolLoading.value && !historyLoading.value) void refresh() }, 15000)
  clockTimer = setInterval(() => { now.value = Date.now() }, 1000)
})
onUnmounted(() => {
  clearInterval(refreshTimer)
  clearInterval(clockTimer)
  poolRequest++
  historyRequest++
})
</script>

<template>
  <section class="min-w-0 space-y-3" data-testid="session-pool-section" :aria-busy="poolLoading">
    <header class="flex flex-wrap items-baseline justify-between gap-2">
      <h2 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.ops.switchRecords.pool.title') }} <span v-if="snapshot" class="ml-2 font-normal text-gray-600 dark:text-gray-300">{{ candidates.length }} / {{ snapshot.target_size }}</span></h2>
      <button type="button" class="btn btn-secondary btn-sm" :aria-expanded="poolExpanded" aria-controls="pool-candidates" data-testid="toggle-session-pool" @click="poolExpanded = !poolExpanded">
        {{ poolExpanded ? t('admin.ops.switchRecords.pool.collapse') : t('admin.ops.switchRecords.pool.expand') }}
      </button>
    </header>
    <div v-if="poolExpanded" id="pool-candidates" class="space-y-3">
      <p class="text-xs text-gray-600 dark:text-gray-300">{{ t('admin.ops.switchRecords.pool.liveHint') }}</p>
      <p v-if="poolError" role="alert" class="text-sm text-red-700 dark:text-red-300">{{ t('admin.ops.switchRecords.pool.loadFailed') }}</p>
      <p v-else-if="!snapshot" class="text-sm text-gray-600 dark:text-gray-300">{{ t('common.loading') }}</p>
      <p v-else-if="!snapshot.enabled" class="text-sm text-gray-600 dark:text-gray-300">{{ t('admin.ops.switchRecords.pool.disabled') }}</p>
      <div v-else class="overflow-x-auto rounded-lg border border-gray-200 dark:border-dark-700">
        <table class="w-full min-w-[640px] text-left text-sm">
          <thead class="bg-gray-50 text-xs text-gray-600 dark:bg-dark-800 dark:text-gray-300"><tr>
            <th class="px-4 py-3">{{ t('admin.ops.switchRecords.pool.account') }}</th>
            <th class="px-4 py-3">{{ t('admin.ops.switchRecords.model') }}</th>
            <th class="px-4 py-3">{{ t('admin.ops.switchRecords.pool.transport') }}</th>
            <th class="px-4 py-3">{{ t('admin.ops.switchRecords.probeLatency') }}</th>
            <th class="px-4 py-3">{{ t('admin.ops.switchRecords.pool.expiresAt') }}</th>
          </tr></thead>
          <tbody class="divide-y divide-gray-100 bg-white text-gray-800 dark:divide-dark-700 dark:bg-dark-900 dark:text-gray-200">
            <tr v-for="candidate in candidates" :key="candidate.result_id" data-testid="pool-candidate">
              <td class="px-4 py-3">{{ candidate.account_name || '—' }} <span class="ml-1 text-xs text-gray-600 dark:text-gray-300">#{{ candidate.account_id }}</span></td>
              <td class="px-4 py-3">{{ candidate.model }}</td>
              <td class="px-4 py-3">{{ transportLabel(candidate.transport) }}</td>
              <td class="whitespace-nowrap px-4 py-3 font-mono">{{ candidate.first_token_ms }} ms</td>
              <td class="whitespace-nowrap px-4 py-3"><time :datetime="candidate.expires_at">{{ formatTime(candidate.expires_at) }}</time></td>
            </tr>
            <tr v-if="!candidates.length"><td colspan="5" class="px-4 py-6 text-center text-gray-600 dark:text-gray-300">{{ t('admin.ops.switchRecords.pool.empty') }}</td></tr>
          </tbody>
        </table>
      </div>
    </div>
  </section>

  <section class="min-w-0 space-y-3" :aria-busy="historyLoading" data-testid="pool-probe-section">
    <header class="flex flex-wrap items-center justify-between gap-2">
      <h2 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.ops.switchRecords.pool.probesTitle') }}</h2>
      <button type="button" class="btn btn-secondary btn-sm" :disabled="historyLoading" :aria-expanded="expanded" aria-controls="pool-probe-groups" data-testid="toggle-pool-probes" @click="toggleHistory">
        {{ expanded ? t('admin.ops.switchRecords.pool.collapse') : t('admin.ops.switchRecords.pool.expand') }}
      </button>
    </header>
    <p class="text-xs text-gray-600 dark:text-gray-300">{{ t('admin.ops.switchRecords.pool.probesHint') }}</p>
    <p v-if="historyError" role="alert" class="text-sm text-red-700 dark:text-red-300">{{ t('admin.ops.switchRecords.pool.probesLoadFailed') }}</p>
    <div v-else id="pool-probe-groups" class="space-y-4">
      <p v-if="historyLoading && !groups.length" class="text-sm text-gray-600 dark:text-gray-300">{{ t('common.loading') }}</p>
      <p v-else-if="!groups.length" class="text-sm text-gray-600 dark:text-gray-300">{{ t('admin.ops.switchRecords.pool.noProbes') }}</p>
      <article v-for="group in groups" :key="group.id" class="space-y-2 border-t border-gray-200 pt-3 dark:border-dark-700" data-testid="pool-probe-group">
        <div class="flex flex-wrap items-baseline gap-x-4 gap-y-1 text-xs text-gray-600 dark:text-gray-300">
          <time :datetime="group.created_at" class="font-medium text-gray-900 dark:text-white">{{ formatTime(group.created_at) }}</time>
          <span>{{ reasonLabel(String(group.extra?.reason ?? '')) }}</span>
          <span>{{ t('admin.ops.switchRecords.pool.summary', { probes: group.extra?.probe_count ?? results(group).length, published: group.extra?.published_count ?? 0 }) }}</span>
        </div>
        <div class="overflow-x-auto rounded-lg border border-gray-200 dark:border-dark-700">
          <table class="w-full min-w-[720px] text-left text-sm">
            <thead class="bg-gray-50 text-xs text-gray-600 dark:bg-dark-800 dark:text-gray-300"><tr>
              <th class="px-4 py-2">{{ t('admin.ops.switchRecords.probeNumber') }}</th>
              <th class="px-4 py-2">{{ t('admin.ops.switchRecords.pool.account') }}</th>
              <th class="px-4 py-2">{{ t('admin.ops.switchRecords.model') }}</th>
              <th class="px-4 py-2">{{ t('admin.ops.switchRecords.pool.transport') }}</th>
              <th class="px-4 py-2">{{ t('admin.ops.switchRecords.probeLatency') }}</th>
              <th class="px-4 py-2">{{ t('admin.ops.switchRecords.probeResults') }}</th>
            </tr></thead>
            <tbody class="divide-y divide-gray-100 bg-white text-gray-800 dark:divide-dark-700 dark:bg-dark-900 dark:text-gray-200">
              <tr v-for="result in results(group)" :key="`${result.round}-${result.probe_number}`">
                <td class="whitespace-nowrap px-4 py-2">{{ result.round }} / #{{ result.probe_number }}</td>
                <td class="px-4 py-2">{{ result.account_name || '—' }} <span class="ml-1 text-xs text-gray-600 dark:text-gray-300">#{{ result.account_id }}</span></td>
                <td class="px-4 py-2">{{ result.model }}</td>
                <td class="px-4 py-2">{{ transportLabel(result.transport) }}</td>
                <td class="whitespace-nowrap px-4 py-2 font-mono">{{ result.first_token_ms == null ? '—' : `${result.first_token_ms} ms` }}</td>
                <td class="px-4 py-2"><span :class="result.published ? 'text-emerald-700 dark:text-emerald-300' : 'text-gray-700 dark:text-gray-300'">{{ result.published ? t('admin.ops.switchRecords.pool.published') : reasonLabel(result.reason) }}</span></td>
              </tr>
            </tbody>
          </table>
        </div>
      </article>
      <Pagination v-if="expanded && total > 0" :page="page" :page-size="pageSize" :total="total" @update:page="changePage" @update:pageSize="changePageSize" />
    </div>
  </section>
</template>

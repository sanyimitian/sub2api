<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Pagination from '@/components/common/Pagination.vue'
import Select from '@/components/common/Select.vue'
import { opsAPI, type OpsSystemLog } from '@/api/admin/ops'
import { useAppStore } from '@/stores'

type TimeRange = '5m' | '30m' | '1h' | '6h' | '24h' | '7d' | '30d'
type SwitchEvent = 'probe' | 'pending' | 'reconnect' | 'completed'

interface ProbeResultLog {
  probe_number?: number
  requested_account_id?: number
  account_id?: number
  attempted_account_ids?: number[]
  first_token_ms?: number
  success?: boolean
  timed_out?: boolean
}

const { t } = useI18n()
const appStore = useAppStore()
const records = ref<OpsSystemLog[]>([])
const loading = ref(false)
const page = ref(1)
const pageSize = ref(20)
const total = ref(0)
const timeRange = ref<TimeRange>('30d')
const modelFilter = ref('')

const timeRangeOptions = computed(() => [
  { value: '5m', label: '5m' },
  { value: '30m', label: '30m' },
  { value: '1h', label: '1h' },
  { value: '6h', label: '6h' },
  { value: '24h', label: '24h' },
  { value: '7d', label: '7d' },
  { value: '30d', label: '30d' }
])

const eventStyles: Record<SwitchEvent, string> = {
  probe: 'bg-violet-100 text-violet-800 dark:bg-violet-900/30 dark:text-violet-300',
  pending: 'bg-amber-100 text-amber-800 dark:bg-amber-900/30 dark:text-amber-300',
  reconnect: 'bg-blue-100 text-blue-800 dark:bg-blue-900/30 dark:text-blue-300',
  completed: 'bg-emerald-100 text-emerald-800 dark:bg-emerald-900/30 dark:text-emerald-300'
}

const eventType = (row: OpsSystemLog): SwitchEvent | null => {
  switch (row.message) {
    case 'openai.ws_latency_switch_probe_completed':
      return 'probe'
    case 'openai.ws_latency_switch_pending':
      return 'pending'
    case 'openai.ws_latency_switch_reconnect_requested':
      return 'reconnect'
    case 'openai.ws_latency_switch_completed':
      return 'completed'
    default:
      return null
  }
}

const eventLabel = (event: SwitchEvent) => {
  const keys: Record<SwitchEvent, string> = {
    probe: 'admin.ops.switchRecords.probeCompleted',
    pending: 'admin.ops.switchRecords.pending',
    reconnect: 'admin.ops.switchRecords.reconnectRequested',
    completed: 'admin.ops.switchRecords.completed'
  }
  return t(keys[event])
}

const extraValue = (row: OpsSystemLog, key: string) => {
  const value = row.extra?.[key]
  return value == null || value === '' ? '' : String(value)
}

const fromAccount = (row: OpsSystemLog) => extraValue(row, 'from_account_id') || '-'
const targetAccount = (row: OpsSystemLog) => extraValue(row, 'target_account_id') || extraValue(row, 'account_id') || '-'
const probeResults = (row: OpsSystemLog): ProbeResultLog[] => {
  const results = row.extra?.probe_results
  return Array.isArray(results) ? results as ProbeResultLog[] : []
}
const attemptedAccounts = (result: ProbeResultLog) => {
  if (result.attempted_account_ids?.length) {
    return result.attempted_account_ids.map((id) => `#${id}`).join(' -> ')
  }
  const accountID = result.requested_account_id ?? result.account_id
  return accountID == null ? '-' : `#${accountID}`
}
const probeStatus = (result: ProbeResultLog) => {
  if (result.success) return t('admin.ops.switchRecords.probeSuccess')
  if (result.timed_out) return t('admin.ops.switchRecords.probeTimedOut')
  return t('admin.ops.switchRecords.probeFailed')
}
const formatTime = (value: string) => {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString()
}

const fetchRecords = async () => {
  loading.value = true
  try {
    const response = await opsAPI.listSystemLogs({
      page: page.value,
      page_size: pageSize.value,
      time_range: timeRange.value,
      q: 'openai.ws_latency_switch_',
      model: modelFilter.value.trim() || undefined
    })
    records.value = (response.items || []).filter((row) => eventType(row) !== null)
    total.value = response.total || 0
  } catch (error: any) {
    console.error('[AccountSwitchRecordsView] Failed to fetch records', error)
    appStore.showError(error?.response?.data?.detail || t('admin.ops.switchRecords.loadFailed'))
  } finally {
    loading.value = false
  }
}

const applyFilters = () => {
  page.value = 1
  fetchRecords()
}

const changePage = (nextPage: number) => {
  page.value = nextPage
  fetchRecords()
}

const changePageSize = (nextPageSize: number) => {
  pageSize.value = nextPageSize
  page.value = 1
  fetchRecords()
}

onMounted(fetchRecords)
</script>

<template>
  <AppLayout>
    <main class="space-y-5 p-4 md:p-6">
      <header class="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h1 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('admin.ops.switchRecords.title') }}</h1>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.ops.switchRecords.description') }}</p>
        </div>
        <button type="button" class="btn btn-secondary btn-sm" :disabled="loading" @click="fetchRecords">
          {{ t('admin.ops.switchRecords.refresh') }}
        </button>
      </header>

      <section class="flex flex-wrap items-end gap-3">
        <label class="w-36 text-xs text-gray-600 dark:text-gray-300">
          {{ t('admin.ops.switchRecords.timeRange') }}
          <Select v-model="timeRange" class="mt-1" :options="timeRangeOptions" />
        </label>
        <label class="w-56 text-xs text-gray-600 dark:text-gray-300">
          {{ t('admin.ops.switchRecords.model') }}
          <input
            v-model="modelFilter"
            type="text"
            class="input mt-1"
            :placeholder="t('admin.ops.switchRecords.modelPlaceholder')"
            @keyup.enter="applyFilters"
          />
        </label>
        <button type="button" class="btn btn-primary btn-sm" @click="applyFilters">
          {{ t('admin.ops.switchRecords.search') }}
        </button>
      </section>

      <div class="overflow-x-auto rounded-lg border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-900">
        <table class="w-full min-w-[860px] text-left text-sm">
          <thead class="bg-gray-50 text-xs uppercase text-gray-500 dark:bg-dark-800 dark:text-gray-400">
            <tr>
              <th class="px-4 py-3 font-medium">{{ t('admin.ops.switchRecords.time') }}</th>
              <th class="px-4 py-3 font-medium">{{ t('admin.ops.switchRecords.status') }}</th>
              <th class="px-4 py-3 font-medium">{{ t('admin.ops.switchRecords.fromAccount') }}</th>
              <th class="px-4 py-3 font-medium">{{ t('admin.ops.switchRecords.targetAccount') }}</th>
              <th class="px-4 py-3 font-medium">{{ t('admin.ops.switchRecords.model') }}</th>
              <th class="px-4 py-3 font-medium">{{ t('admin.ops.switchRecords.probeResults') }}</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-gray-100 dark:divide-dark-800">
            <tr v-if="loading">
              <td colspan="6" class="px-4 py-10 text-center text-gray-500">{{ t('common.loading') }}</td>
            </tr>
            <tr v-else-if="records.length === 0">
              <td colspan="6" class="px-4 py-10 text-center text-gray-500">{{ t('admin.ops.switchRecords.empty') }}</td>
            </tr>
            <template v-else>
              <tr v-for="row in records" :key="row.id" class="text-gray-700 dark:text-gray-200">
                <td class="whitespace-nowrap px-4 py-3">{{ formatTime(row.created_at) }}</td>
                <td class="px-4 py-3">
                  <span
                    v-if="eventType(row)"
                    class="inline-flex rounded-md px-2 py-1 text-xs font-medium"
                    :class="eventStyles[eventType(row)!]"
                  >
                    {{ eventLabel(eventType(row)!) }}
                  </span>
                </td>
                <td class="px-4 py-3 font-mono">{{ fromAccount(row) }}</td>
                <td class="px-4 py-3 font-mono">{{ targetAccount(row) }}</td>
                <td class="px-4 py-3">{{ row.model || '-' }}</td>
                <td class="px-4 py-3">
                  <div v-if="probeResults(row).length" class="space-y-1">
                    <div
                      v-for="(result, index) in probeResults(row)"
                      :key="result.probe_number ?? index"
                      class="whitespace-nowrap text-xs"
                    >
                      <span class="font-mono">{{ t('admin.ops.switchRecords.probeNumber') }} #{{ result.probe_number ?? index + 1 }}: {{ attemptedAccounts(result) }}</span>
                      <span class="ml-2 text-gray-500 dark:text-gray-400">
                        {{ result.first_token_ms == null ? '-' : `${result.first_token_ms} ms` }}
                        · {{ probeStatus(result) }}
                      </span>
                    </div>
                  </div>
                  <span v-else class="text-gray-400">-</span>
                </td>
              </tr>
            </template>
          </tbody>
        </table>
      </div>

      <Pagination
        v-if="total > 0"
        :page="page"
        :total="total"
        :page-size="pageSize"
        @update:page="changePage"
        @update:pageSize="changePageSize"
      />
    </main>
  </AppLayout>
</template>

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
const allRecords = ref<OpsSystemLog[]>([])
const loading = ref(false)
const page = ref(1)
const pageSize = ref(20)
const total = ref(0)
const timeRange = ref<TimeRange>('30d')
const modelFilter = ref('')
const expandedGroups = ref(new Set<string>())

interface SwitchRecordGroup {
  id: string
  events: OpsSystemLog[]
  latest: OpsSystemLog
  fromAccount: string
  targetAccount: string
}

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
const triggeringUser = (row: OpsSystemLog) => extraValue(row, 'username') || extraValue(row, 'user_email') || (row.user_id ? `#${row.user_id}` : '-')
const probeResults = (row: OpsSystemLog): ProbeResultLog[] => {
  const results = row.extra?.probe_results
  return Array.isArray(results) ? results as ProbeResultLog[] : []
}
const probeRound = (row: OpsSystemLog) => {
  const current = Number(extraValue(row, 'probe_round'))
  const total = Number(extraValue(row, 'max_probe_rounds'))
  return current > 0 && total > 0 ? t('admin.ops.switchRecords.probeRound', { current, total }) : ''
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

const groups = computed<SwitchRecordGroup[]>(() => {
  const grouped = new Map<string, OpsSystemLog[]>()
  for (const row of allRecords.value) {
    const conversationID = extraValue(row, 'conversation_id')
    const key = conversationID ? `conversation:${conversationID}` : `legacy:${row.id}`
    const rows = grouped.get(key) || []
    rows.push(row)
    grouped.set(key, rows)
  }

  return [...grouped.entries()].map(([id, events]) => {
    events.sort((a, b) => new Date(a.created_at).getTime() - new Date(b.created_at).getTime())
    const newestValue = (field: (row: OpsSystemLog) => string) => [...events].reverse().map(field).find((value) => value && value !== '-') || '-'
    return {
      id,
      events,
      latest: events[events.length - 1]!,
      fromAccount: newestValue(fromAccount),
      targetAccount: newestValue(targetAccount)
    }
  }).sort((a, b) => new Date(b.latest.created_at).getTime() - new Date(a.latest.created_at).getTime())
})

const pagedGroups = computed(() => {
  const start = (page.value - 1) * pageSize.value
  return groups.value.slice(start, start + pageSize.value)
})

const toggleGroup = (id: string) => {
  const next = new Set(expandedGroups.value)
  if (next.has(id)) next.delete(id)
  else next.add(id)
  expandedGroups.value = next
}

const groupStatus = (group: SwitchRecordGroup) => {
  const status = eventType(group.latest)
  return status ? eventLabel(status) : '-'
}

const fetchRecords = async () => {
  loading.value = true
  try {
    const pageSize = 200
    const rows: OpsSystemLog[] = []
    let currentPage = 1
    let responseTotal = 0
    do {
      const response = await opsAPI.listSystemLogs({
        page: currentPage,
        page_size: pageSize,
        time_range: timeRange.value,
        q: 'openai.ws_latency_switch_',
        model: modelFilter.value.trim() || undefined
      })
      rows.push(...(response.items || []).filter((row) => eventType(row) !== null))
      responseTotal = response.total || 0
      currentPage++
    } while (rows.length < responseTotal && currentPage <= Math.ceil(responseTotal / pageSize))
    allRecords.value = rows
    total.value = groups.value.length
    expandedGroups.value = new Set()
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
}

const changePageSize = (nextPageSize: number) => {
  pageSize.value = nextPageSize
  page.value = 1
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
        <table class="w-full min-w-[1020px] text-left text-sm">
          <thead class="bg-gray-50 text-xs uppercase text-gray-500 dark:bg-dark-800 dark:text-gray-400">
            <tr>
              <th class="px-4 py-3 font-medium">{{ t('admin.ops.switchRecords.time') }}</th>
              <th class="px-4 py-3 font-medium">{{ t('admin.ops.switchRecords.status') }}</th>
              <th class="px-4 py-3 font-medium">{{ t('admin.ops.switchRecords.triggeringUser') }}</th>
              <th class="px-4 py-3 font-medium">{{ t('admin.ops.switchRecords.fromAccount') }}</th>
              <th class="px-4 py-3 font-medium">{{ t('admin.ops.switchRecords.targetAccount') }}</th>
              <th class="px-4 py-3 font-medium">{{ t('admin.ops.switchRecords.model') }}</th>
              <th class="px-4 py-3 font-medium">{{ t('admin.ops.switchRecords.probeResults') }}</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-gray-100 dark:divide-dark-800">
            <tr v-if="loading">
              <td colspan="7" class="px-4 py-10 text-center text-gray-500">{{ t('common.loading') }}</td>
            </tr>
            <tr v-else-if="groups.length === 0">
              <td colspan="7" class="px-4 py-10 text-center text-gray-500">{{ t('admin.ops.switchRecords.empty') }}</td>
            </tr>
            <template v-else>
              <template v-for="group in pagedGroups" :key="group.id">
              <tr class="text-gray-700 dark:text-gray-200">
                <td class="whitespace-nowrap px-4 py-3">
                  <button
                    type="button"
                    class="inline-flex items-center gap-2 rounded text-left focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500"
                    data-testid="toggle-switch-group"
                    :aria-expanded="expandedGroups.has(group.id)"
                    :aria-label="`${expandedGroups.has(group.id) ? t('admin.ops.switchRecords.collapse') : t('admin.ops.switchRecords.expand')} ${triggeringUser(group.latest)}`"
                    @click="toggleGroup(group.id)"
                  >
                    <span aria-hidden="true" class="w-4 shrink-0 text-center">{{ expandedGroups.has(group.id) ? '▾' : '▸' }}</span>
                    {{ formatTime(group.latest.created_at) }}
                  </button>
                </td>
                <td class="px-4 py-3">
                  <span
                    v-if="eventType(group.latest)"
                    class="inline-flex rounded-md px-2 py-1 text-xs font-medium"
                    :class="eventStyles[eventType(group.latest)!]"
                  >
                    {{ groupStatus(group) }}<span v-if="eventType(group.latest) === 'probe' && probeRound(group.latest)"> · {{ probeRound(group.latest) }}</span>
                  </span>
                  <span class="ml-2 text-xs text-gray-500">{{ t('admin.ops.switchRecords.eventCount', { count: group.events.length }) }}</span>
                </td>
                <td class="px-4 py-3" data-testid="triggering-user">
                  <div class="font-medium">{{ triggeringUser(group.latest) }}</div>
                  <div v-if="extraValue(group.latest, 'username') && extraValue(group.latest, 'user_email')" class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ extraValue(group.latest, 'user_email') }}</div>
                  <div v-if="group.latest.user_id && (extraValue(group.latest, 'username') || extraValue(group.latest, 'user_email'))" class="mt-1 font-mono text-xs text-gray-500 dark:text-gray-400">ID: {{ group.latest.user_id }}</div>
                </td>
                <td class="px-4 py-3 font-mono">{{ group.fromAccount }}</td>
                <td class="px-4 py-3 font-mono">{{ group.targetAccount }}</td>
                <td class="px-4 py-3">{{ group.latest.model || '-' }}</td>
                <td class="px-4 py-3">
                  <div v-if="probeResults(group.latest).length" class="space-y-1">
                    <div
                      v-for="(result, index) in probeResults(group.latest)"
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
              <tr v-if="expandedGroups.has(group.id)" class="bg-gray-50 text-gray-700 dark:bg-dark-800/50 dark:text-gray-200">
                <td colspan="7" class="px-4 py-3">
                  <ol class="space-y-2">
                    <li v-for="event in group.events" :key="event.id" class="grid gap-2 text-xs sm:grid-cols-[150px_150px_minmax(0,1fr)]">
                      <time class="text-gray-500 dark:text-gray-400">{{ formatTime(event.created_at) }}</time>
                      <span>{{ eventType(event) ? eventLabel(eventType(event)!) : '-' }}</span>
                      <div v-if="probeResults(event).length" class="space-y-1">
                        <div v-for="(result, index) in probeResults(event)" :key="result.probe_number ?? index">
                          <span class="font-mono">{{ t('admin.ops.switchRecords.probeNumber') }} #{{ result.probe_number ?? index + 1 }}: {{ attemptedAccounts(result) }}</span>
                          <span class="ml-2 text-gray-500 dark:text-gray-400">{{ result.first_token_ms == null ? '-' : `${result.first_token_ms} ms` }} · {{ probeStatus(result) }}</span>
                        </div>
                      </div>
                    </li>
                  </ol>
                </td>
              </tr>
              </template>
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

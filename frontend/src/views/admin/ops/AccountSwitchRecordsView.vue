<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Pagination from '@/components/common/Pagination.vue'
import Select from '@/components/common/Select.vue'
import { opsAPI, type OpsSystemLog } from '@/api/admin/ops'
import { useAppStore } from '@/stores'

type TimeRange = '5m' | '30m' | '1h' | '6h' | '24h'
type SwitchEvent = 'probe' | 'pending' | 'reconnect' | 'completed' | 'assigned' | 'claimed' | 'discarded'
type RecordCategory = 'latency' | 'claim'

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
const loading = ref(false)
const timeRange = ref<TimeRange>('1h')
const modelFilter = ref('')
const expandedGroups = ref(new Set<string>())
const categories = reactive([
  { id: 'latency' as RecordCategory, title: 'latencyTitle', query: 'openai.ws_latency_switch_', expanded: false, page: 1, pageSize: 20, records: [] as OpsSystemLog[] },
  { id: 'claim' as RecordCategory, title: 'claimTitle', query: 'openai.session_pool_', expanded: false, page: 1, pageSize: 20, records: [] as OpsSystemLog[] }
])

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
  { value: '24h', label: '24h' }
])

const eventStyles: Record<SwitchEvent, string> = {
  probe: 'bg-violet-100 text-violet-800 dark:bg-violet-900/30 dark:text-violet-300',
  pending: 'bg-amber-100 text-amber-800 dark:bg-amber-900/30 dark:text-amber-300',
  reconnect: 'bg-blue-100 text-blue-800 dark:bg-blue-900/30 dark:text-blue-300',
  completed: 'bg-emerald-100 text-emerald-800 dark:bg-emerald-900/30 dark:text-emerald-300',
  assigned: 'bg-amber-100 text-amber-800 dark:bg-amber-900/30 dark:text-amber-300',
  claimed: 'bg-emerald-100 text-emerald-800 dark:bg-emerald-900/30 dark:text-emerald-300',
  discarded: 'bg-red-100 text-red-800 dark:bg-red-900/30 dark:text-red-300'
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
    case 'openai.session_pool_assigned':
      return 'assigned'
    case 'openai.session_pool_assignment_completed':
      return 'claimed'
    case 'openai.session_pool_candidate_discarded':
      return 'discarded'
    default:
      return null
  }
}

const eventLabel = (event: SwitchEvent) => {
  const keys: Record<SwitchEvent, string> = {
    probe: 'admin.ops.switchRecords.probeCompleted',
    pending: 'admin.ops.switchRecords.pending',
    reconnect: 'admin.ops.switchRecords.reconnectRequested',
    completed: 'admin.ops.switchRecords.completed',
    assigned: 'admin.ops.switchRecords.assigned',
    claimed: 'admin.ops.switchRecords.claimed',
    discarded: 'admin.ops.switchRecords.discarded'
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

const groupRecords = (records: OpsSystemLog[], category: RecordCategory): SwitchRecordGroup[] => {
  const grouped = new Map<string, OpsSystemLog[]>()
  for (const row of records) {
    const conversationID = extraValue(row, 'conversation_id')
    const key = conversationID ? `${category}:conversation:${conversationID}` : `${category}:legacy:${row.id}`
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
}

const sections = computed(() => categories.map((category) => {
  const groups = groupRecords(category.records, category.id)
  const start = (category.page - 1) * category.pageSize
  return { ...category, groups, pagedGroups: groups.slice(start, start + category.pageSize) }
}))

const toggleSection = (id: RecordCategory) => {
  const category = categories.find((item) => item.id === id)!
  category.expanded = !category.expanded
}

const claimProbeLatency = (group: SwitchRecordGroup) => {
  const event = [...group.events].reverse().find((row) => row.extra?.probe_first_token_ms != null)
  return event ? `${extraValue(event, 'probe_first_token_ms')} ms` : '-'
}

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
  if (loading.value) return
  loading.value = true
  const filters = { time_range: timeRange.value, model: modelFilter.value.trim() || undefined }
  try {
    await Promise.all(categories.map(async (category) => {
      try {
        const batchSize = 200
        const rows: OpsSystemLog[] = []
        let currentPage = 1
        let responseTotal = 0
        do {
          const response = await opsAPI.listSystemLogs({
            page: currentPage,
            page_size: batchSize,
            ...filters,
            q: category.query
          })
          rows.push(...(response.items || []).filter((row) => row.message.startsWith(category.query) && eventType(row) !== null))
          responseTotal = response.total || 0
          currentPage++
        } while (currentPage <= Math.ceil(responseTotal / batchSize))
        category.records = rows
        category.page = 1
      } catch (error: any) {
        category.records = []
        console.error('[AccountSwitchRecordsView] Failed to fetch records', error)
        appStore.showError(error?.response?.data?.detail || t('admin.ops.switchRecords.loadFailed'))
      }
    }))
    expandedGroups.value = new Set()
  } finally {
    loading.value = false
  }
}

const applyFilters = () => {
  fetchRecords()
}

const changePage = (id: RecordCategory, nextPage: number) => {
  categories.find((item) => item.id === id)!.page = nextPage
}

const changePageSize = (id: RecordCategory, nextPageSize: number) => {
  const category = categories.find((item) => item.id === id)!
  category.pageSize = nextPageSize
  category.page = 1
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
        <button type="button" class="btn btn-primary btn-sm" :disabled="loading" @click="applyFilters">
          {{ t('admin.ops.switchRecords.search') }}
        </button>
      </section>

      <section v-for="section in sections" :key="section.id" class="space-y-3">
        <h2>
          <button
            type="button"
            class="flex w-full items-center gap-2 rounded-lg border border-gray-200 bg-white px-4 py-3 text-left text-sm font-semibold text-gray-900 hover:bg-gray-50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 dark:border-dark-700 dark:bg-dark-900 dark:text-white dark:hover:bg-dark-800"
            :data-testid="`toggle-${section.id}-section`"
            :aria-expanded="section.expanded"
            :aria-controls="`${section.id}-records`"
            @click="toggleSection(section.id)"
          >
            <span aria-hidden="true" class="w-4 shrink-0 text-center">{{ section.expanded ? '▾' : '▸' }}</span>
            {{ t(`admin.ops.switchRecords.${section.title}`) }}
            <span v-if="!loading" class="ml-auto text-xs font-normal text-gray-500 dark:text-gray-400">{{ t('admin.ops.switchRecords.conversationCount', { count: section.groups.length }) }}</span>
          </button>
        </h2>
        <div v-if="section.expanded" :id="`${section.id}-records`" class="space-y-3">
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
                  <th class="px-4 py-3 font-medium">{{ t(section.id === 'claim' ? 'admin.ops.switchRecords.probeLatency' : 'admin.ops.switchRecords.probeResults') }}</th>
                </tr>
              </thead>
              <tbody class="divide-y divide-gray-100 dark:divide-dark-800">
                <tr v-if="loading">
                  <td colspan="7" class="px-4 py-10 text-center text-gray-500">{{ t('common.loading') }}</td>
                </tr>
                <tr v-else-if="section.groups.length === 0">
                  <td colspan="7" class="px-4 py-10 text-center text-gray-500">{{ t('admin.ops.switchRecords.empty') }}</td>
                </tr>
                <template v-else>
                  <template v-for="group in section.pagedGroups" :key="group.id">
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
                        <span v-if="section.id === 'claim'" class="whitespace-nowrap font-mono text-xs">{{ claimProbeLatency(group) }}</span>
                        <div v-else-if="probeResults(group.latest).length" class="space-y-1">
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
            v-if="section.groups.length > 0"
            :page="section.page"
            :total="section.groups.length"
            :page-size="section.pageSize"
            @update:page="changePage(section.id, $event)"
            @update:pageSize="changePageSize(section.id, $event)"
          />
        </div>
      </section>
    </main>
  </AppLayout>
</template>

<template>
  <section
    class="card overflow-hidden !rounded-3xl !border-0 p-0 shadow-sm ring-1 ring-gray-900/5 dark:!bg-dark-800 dark:ring-dark-700"
    :aria-label="t('publicTransit.monitoringSummary')"
  >
    <header class="flex flex-wrap items-start justify-between gap-4 border-b border-gray-100 px-5 py-4 dark:border-dark-700 sm:px-6">
      <div class="min-w-0">
        <h2 class="flex items-center gap-2 text-xl font-black text-gray-900 dark:text-white">
          <span class="inline-flex h-8 w-8 items-center justify-center rounded-xl bg-blue-50 text-blue-500 dark:bg-blue-900/30 dark:text-blue-400">
            <Icon name="chart" size="sm" />
          </span>
          {{ t('publicTransit.passiveMonitoring') }}
        </h2>
        <div class="mt-1.5 flex flex-wrap items-center gap-2 text-xs text-gray-500 dark:text-gray-400">
          <span class="relative flex h-2 w-2 shrink-0">
            <span
              class="relative inline-flex h-2 w-2 rounded-full"
              :class="monitoring?.enabled ? 'bg-green-500' : 'bg-gray-400'"
            ></span>
          </span>
          <span>{{ t('publicTransit.passiveMonitoringHint') }}</span>
          <span
            v-if="monitoring"
            class="badge"
            :class="monitoring.enabled ? 'badge-success' : 'badge-gray'"
          >
            {{ monitoring.enabled ? t('publicTransit.passiveEnabled') : t('publicTransit.passiveUnavailable') }}
          </span>
        </div>
      </div>
      <div class="max-w-sm text-right text-xs text-gray-400 dark:text-gray-500">
        <p>{{ t('publicTransit.passiveSource') }}</p>
        <p class="mt-1">{{ t('publicTransit.passivePrivacyHint') }}</p>
      </div>
    </header>

    <div
      v-if="monitoring"
      class="monitor-toolbar flex flex-nowrap items-center gap-1.5 overflow-x-auto border-b border-gray-100 px-4 py-3 dark:border-dark-700 sm:gap-2 sm:px-5"
    >
      <div
        class="tabs inline-flex shrink-0"
        role="group"
        :aria-label="t('publicTransit.passiveRange')"
      >
        <button
          v-for="option in ranges"
          :key="option"
          type="button"
          class="tab !px-2 !py-1 text-xs sm:!px-2.5"
          :class="range === option ? 'tab-active' : ''"
          @click="emit('update:range', option)"
        >
          {{ t(`publicTransit.passiveRanges.${option}`) }}
        </button>
      </div>

      <span class="mx-0.5 hidden h-5 w-px shrink-0 bg-gray-200 dark:bg-dark-700 sm:block" aria-hidden="true"></span>

      <FilterMultiSelect
        v-model="platformFilters"
        compact
        :label="t('publicTransit.platform')"
        :all-label="t('publicTransit.allPlatforms')"
        :options="platformOptions"
      />
      <FilterMultiSelect
        v-model="groupFilters"
        compact
        :label="t('publicTransit.group')"
        :all-label="t('publicTransit.allGroups')"
        :options="groupOptions"
      />
      <FilterMultiSelect
        v-model="modelFilters"
        compact
        :label="t('publicTransit.model')"
        :all-label="t('publicTransit.allModels')"
        :options="modelOptions"
      />
      <button
        type="button"
        class="btn btn-ghost btn-sm shrink-0 !px-2 !py-1 text-xs"
        :disabled="!hasFilters"
        :class="!hasFilters ? 'opacity-40' : ''"
        @click="clearFilters"
      >
        {{ t('publicTransit.resetFilters') }}
      </button>

      <span class="mx-0.5 hidden h-5 w-px shrink-0 bg-gray-200 dark:bg-dark-700 md:block" aria-hidden="true"></span>

      <div
        class="tabs ml-auto inline-flex shrink-0"
        role="group"
        :aria-label="t('publicTransit.passiveMetric')"
      >
        <button
          v-for="option in metrics"
          :key="option"
          type="button"
          class="tab !px-2 !py-1 text-xs"
          :class="metric === option ? 'tab-active' : ''"
          @click="metric = option"
        >
          {{ t(`publicTransit.passiveMetrics.${option}`) }}
        </button>
      </div>
    </div>

    <div v-if="loading" class="grid grid-cols-2 gap-3 p-4 sm:grid-cols-3 xl:grid-cols-5" aria-hidden="true">
      <div v-for="i in 5" :key="i" class="h-24 animate-pulse rounded-2xl bg-gray-50 dark:bg-dark-900/30" />
    </div>

    <template v-else-if="monitoring?.enabled">
      <div v-if="monitoring.warnings?.length" class="mx-4 mt-4 rounded-2xl border border-amber-200 bg-amber-50 px-3 py-2 text-xs text-amber-800 dark:border-amber-900/60 dark:bg-amber-950/30 dark:text-amber-200 sm:mx-5" role="status">
        {{ monitoring.warnings.join('；') }}
      </div>

      <section v-if="monitoring.window.metrics" class="grid grid-cols-2 gap-3 p-4 sm:grid-cols-3 sm:p-5 xl:grid-cols-5" :aria-label="t('publicTransit.monitoringSummary')">
        <MetricCell
          :label="t('channelMonitorV2.metrics.successRate')"
          :value="formatMonitorPercent(1 - monitoring.window.metrics.error_rate)"
          :detail="t('channelMonitorV2.metrics.errorRateValue', { value: formatMonitorPercent(monitoring.window.metrics.error_rate) })"
          :state="healthState(monitoring.window.health?.error_rate)"
        />
        <MetricCell
          :label="t('channelMonitorV2.metrics.ttftP50')"
          :value="formatMonitorMs(monitoring.window.metrics.ttft.p50_ms)"
          :detail="formatLatencyKpiSecondary(monitoring.window.metrics.ttft.avg_ms, monitoring.window.metrics.ttft.p90_ms, monitoring.window.metrics.ttft.p95_ms)"
          :title="formatLatencyPrivacy(monitoring.window.metrics.ttft.p50_ms, monitoring.window.metrics.ttft.p90_ms, monitoring.window.metrics.ttft.avg_ms, monitoring.window.metrics.ttft.p95_ms)"
          :state="healthState(monitoring.window.health?.ttft)"
        />
        <MetricCell
          :label="t('channelMonitorV2.metrics.tps')"
          :value="formatMonitorTokensPerSecond(monitoring.window.metrics.tpm)"
          :detail="t('channelMonitorV2.metrics.tpsDetail')"
        />
        <MetricCell
          :label="t('channelMonitorV2.metrics.cacheRate')"
          :value="formatMonitorPercent(monitoring.window.metrics.cache_rate)"
          :detail="t('channelMonitorV2.metrics.cacheDetail')"
          :state="healthState(monitoring.window.health?.cache || monitoring.window.health?.overall)"
        />
        <MetricCell
          :label="t('channelMonitorV2.metrics.rpm')"
          :value="formatMonitorThroughput(monitoring.window.metrics.rpm)"
          :detail="t('channelMonitorV2.metrics.rpmDetail')"
        />
      </section>

      <section v-else class="grid grid-cols-2 gap-3 p-4 sm:grid-cols-3 sm:p-5 xl:grid-cols-5" :aria-label="t('publicTransit.monitoringSummary')">
        <MetricCell
          :label="t('publicTransit.passiveSuccessRate')"
          :value="formatRate(monitoring.window.success_rate)"
          :detail="`${t('publicTransit.passiveErrorRate')} ${formatRate(errorRate)}`"
          :state="healthForSuccess(monitoring.window.success_rate)"
        />
        <MetricCell
          :label="t('publicTransit.passiveAvgTTFT')"
          :value="formatMilliseconds(monitoring.window.avg_ttft_ms)"
          :detail="t('publicTransit.passiveWindowLabel')"
        />
        <MetricCell
          :label="t('publicTransit.passiveAvgLatency')"
          :value="formatMilliseconds(monitoring.window.avg_latency_ms)"
          :detail="`${t('publicTransit.passiveRequests')} ${formatInteger(monitoring.window.request_count)}`"
        />
        <MetricCell
          :label="t('publicTransit.passiveCacheRate')"
          :value="formatPercent(cacheRate)"
          :detail="t('publicTransit.passiveCacheHint')"
        />
        <MetricCell
          :label="t('publicTransit.passiveRPM')"
          :value="formatRateValue(rpm)"
          :detail="t('publicTransit.passiveRPMHint')"
        />
      </section>

      <div v-if="exactMatrix" class="mx-4 mb-5 sm:mx-5">
        <RelayPulseMatrix
          :rows="exactMatrixRows"
          :coverage="exactMatrix.coverage"
          :health-mode="exactHealthMode"
          show-throughput
        />
      </div>

      <section v-if="exactMatrix" class="mx-4 mb-5 overflow-hidden rounded-2xl border border-gray-100 dark:border-dark-700 sm:mx-5">
        <div class="border-b border-gray-100 px-4 pt-3 dark:border-dark-700 sm:px-5">
          <h3 class="pb-3 text-sm font-bold text-gray-900 dark:text-white">{{ t('publicTransit.passiveModelsTab') }}</h3>
        </div>
        <div class="overflow-x-auto">
          <table class="table monitor-table min-w-[720px]">
            <thead>
              <tr>
                <th>{{ t('channelMonitorV2.table.platformModel') }}</th>
                <th>{{ t('channelMonitorV2.metrics.successRate') }}</th>
                <th>{{ t('channelMonitorV2.metrics.ttftP50') }}</th>
                <th>{{ t('channelMonitorV2.metrics.tps') }}</th>
                <th>{{ t('channelMonitorV2.metrics.cacheRate') }}</th>
                <th>{{ t('channelMonitorV2.metrics.rpm') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="item in filteredModels" :key="`${item.platform}:${item.group_name || ''}:${item.model}`">
                <td>
                  <div class="flex items-center gap-2">
                    <span class="status-dot" :class="healthState(item.health?.overall)" aria-hidden="true"></span>
                    <div>
                      <span class="block text-xs text-gray-500 dark:text-dark-400">{{ platformLabel(item.platform) }}<span v-if="item.group_name"> · {{ item.group_name }}</span></span>
                      <strong class="font-semibold text-gray-900 dark:text-white">{{ item.model }}</strong>
                    </div>
                  </div>
                </td>
                <td>
                  <span class="block">{{ item.metrics ? formatMonitorPercent(1 - item.metrics.error_rate) : formatRate(item.success_rate) }}</span>
                  <small class="text-xs text-gray-400">{{ t('channelMonitorV2.metrics.errorRateValue', { value: item.metrics ? formatMonitorPercent(item.metrics.error_rate) : formatRate(1 - item.success_rate) }) }}</small>
                </td>
                <td>
                  <span class="block">{{ item.metrics ? formatMonitorMs(item.metrics.ttft.p50_ms) : formatMilliseconds(item.avg_ttft_ms) }}</span>
                  <small v-if="item.metrics" class="text-xs text-gray-400">{{ formatLatencyKpiSecondary(item.metrics.ttft.avg_ms, item.metrics.ttft.p90_ms, item.metrics.ttft.p95_ms) }}</small>
                </td>
                <td>{{ item.metrics ? formatMonitorTokensPerSecond(item.metrics.tpm) : '-' }}</td>
                <td>{{ item.metrics ? formatMonitorPercent(item.metrics.cache_rate) : '-' }}</td>
                <td>{{ item.metrics ? formatMonitorThroughput(item.metrics.rpm) : '-' }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>

      <section v-if="!exactMatrix" class="mx-4 mb-5 overflow-hidden rounded-2xl border border-gray-100 dark:border-dark-700 sm:mx-5">
        <div class="border-b border-gray-100 px-4 pt-3 dark:border-dark-700 sm:px-5">
          <div class="flex flex-wrap items-center justify-between gap-2 pb-2">
            <h3 class="flex items-center gap-2 text-sm font-bold text-gray-900 dark:text-white">
              <span class="inline-flex h-4 w-4 text-emerald-500" aria-hidden="true"><Icon name="grid" size="sm" /></span>
              {{ t('publicTransit.passiveTimelineTitle') }}
            </h3>
            <span v-if="activeTab === 'groups'" class="badge badge-gray">{{ bucketSizeLabel }}</span>
          </div>
          <nav class="tabs w-full max-w-md sm:w-auto" role="tablist" :aria-label="t('publicTransit.passiveDimensionTitle')">
            <button
              v-for="tab in tabs"
              :key="tab"
              type="button"
              role="tab"
              class="tab flex-1 sm:flex-none"
              :aria-selected="activeTab === tab"
              :class="activeTab === tab ? 'tab-active' : ''"
              @click="activeTab = tab"
            >
              {{ tab === 'groups' ? t('publicTransit.passiveGroupsTab') : t('publicTransit.passiveModelsTab') }}
            </button>
          </nav>
          <p class="pb-3 pt-2 text-xs text-gray-500 dark:text-dark-400">
            {{ t('publicTransit.passiveDimensionHint') }}
            <span class="ml-1 text-gray-400">{{ t('publicTransit.passiveWindowLabel') }} · {{ t(`publicTransit.passiveRanges.${range}`) }}</span>
          </p>
        </div>

        <div class="overflow-x-auto">
          <table v-if="activeTab === 'groups'" class="table monitor-table min-w-[1180px]">
            <thead>
              <tr>
                <th>{{ t('publicTransit.passiveDimension') }}</th>
                <th>{{ t('publicTransit.passiveSuccessRate') }}</th>
                <th>{{ t('publicTransit.passiveErrorRate') }}</th>
                <th>{{ t('publicTransit.passiveAvgTTFT') }}</th>
                <th>{{ t('publicTransit.passiveAvgLatency') }}</th>
                <th>{{ t('publicTransit.passiveCacheRate') }}</th>
                <th>{{ t('publicTransit.passiveRequests') }}</th>
                <th class="min-w-[360px]">
                  <span class="flex items-center justify-between gap-4 text-[10px] font-semibold text-gray-500 dark:text-dark-400">
                    <span>{{ timelineAxisStart }}</span>
                    <span>{{ timelineAxisEnd }}</span>
                  </span>
                </th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="item in filteredGroups" :key="`${item.platform}:${item.name}`">
                <td>
                  <div class="flex min-w-[220px] items-center gap-2">
                    <span class="status-dot" :class="statusDot(item.success_rate)" aria-hidden="true"></span>
                    <div class="min-w-0">
                      <span class="block text-xs text-gray-500 dark:text-dark-400">{{ platformLabel(item.platform) }}</span>
                      <strong class="block truncate font-semibold text-gray-900 dark:text-white">{{ item.name }}</strong>
                    </div>
                  </div>
                </td>
                <td>{{ formatRate(item.success_rate) }}</td>
                <td class="text-rose-600 dark:text-rose-300">{{ formatRate(1 - item.success_rate) }}</td>
                <td>{{ formatMilliseconds(item.avg_ttft_ms) }}</td>
                <td>{{ formatMilliseconds(item.avg_latency_ms) }}</td>
                <td>{{ formatPercent(cacheRateForGroup(item.name, item.platform)) }}</td>
                <td>{{ formatInteger(item.request_count) }}</td>
                <td>
                  <div
                    class="pulse-track"
                    :style="pulseTrackStyle"
                    :aria-label="t('publicTransit.passiveTimeline')"
                  >
                    <span
                      v-for="start in timelineStarts"
                      :key="start"
                      class="pulse-cell"
                      :class="bucketTone(bucketForGroup(item, start))"
                      tabindex="0"
                      role="img"
                      :title="bucketTooltip(bucketForGroup(item, start), start)"
                      :aria-label="bucketTooltip(bucketForGroup(item, start), start)"
                    ></span>
                  </div>
                </td>
              </tr>
            </tbody>
          </table>

          <table v-else class="table monitor-table min-w-[820px]">
            <thead>
              <tr>
                <th>{{ t('publicTransit.passiveDimension') }}</th>
                <th>{{ t('publicTransit.passiveSuccessRate') }}</th>
                <th>{{ t('publicTransit.passiveErrorRate') }}</th>
                <th>{{ t('publicTransit.passiveAvgTTFT') }}</th>
                <th>{{ t('publicTransit.passiveAvgLatency') }}</th>
                <th>{{ t('publicTransit.passiveCacheRate') }}</th>
                <th>{{ t('publicTransit.passiveRequests') }}</th>
                <th>{{ t('publicTransit.lastRequest') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="item in filteredModels" :key="`${item.platform}:${item.group_name || ''}:${item.model}`">
                <td>
                  <div class="flex min-w-[250px] items-center gap-2">
                    <span class="status-dot" :class="statusDot(item.success_rate)" aria-hidden="true"></span>
                    <div class="min-w-0">
                      <span class="block text-xs text-gray-500 dark:text-dark-400">{{ platformLabel(item.platform) }}<span v-if="item.group_name"> · {{ item.group_name }}</span></span>
                      <strong class="block truncate font-semibold text-gray-900 dark:text-white">{{ item.model }}</strong>
                    </div>
                  </div>
                </td>
                <td>{{ formatRate(item.success_rate) }}</td>
                <td class="text-rose-600 dark:text-rose-300">{{ formatRate(1 - item.success_rate) }}</td>
                <td>{{ formatMilliseconds(item.avg_ttft_ms) }}</td>
                <td>{{ formatMilliseconds(item.avg_latency_ms) }}</td>
                <td>{{ item.group_name ? formatPercent(cacheRateForGroup(item.group_name, item.platform)) : '-' }}</td>
                <td>{{ formatInteger(item.request_count) }}</td>
                <td>{{ item.last_request_at ? formatDate(item.last_request_at) : '-' }}</td>
              </tr>
            </tbody>
          </table>

          <div v-if="activeTab === 'groups' && filteredGroups.length" class="border-t border-gray-100 px-4 py-3 dark:border-dark-700 sm:px-5">
            <div class="flex items-center gap-2 text-[11px] text-gray-500 dark:text-gray-400">
              <span class="shrink-0">{{ t('publicTransit.passiveBad') }}</span>
              <div class="score-legend h-2.5 flex-1 overflow-hidden rounded-full" aria-hidden="true"></div>
              <span class="shrink-0">{{ t('publicTransit.passiveGood') }}</span>
            </div>
            <div class="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-[11px] text-gray-500 dark:text-gray-400">
              <span class="inline-flex items-center gap-1.5"><i class="status-dot pulse-healthy"></i>{{ t('publicTransit.passiveHealthy') }}</span>
              <span class="inline-flex items-center gap-1.5"><i class="status-dot pulse-warning"></i>{{ t('publicTransit.passiveWarning') }}</span>
              <span class="inline-flex items-center gap-1.5"><i class="status-dot pulse-critical"></i>{{ t('publicTransit.passiveCritical') }}</span>
              <span class="inline-flex items-center gap-1.5"><i class="status-dot pulse-empty"></i>{{ t('publicTransit.passiveNoTraffic') }}</span>
            </div>
          </div>

          <p v-if="activeRows.length === 0" class="px-4 py-10 text-center text-sm text-gray-500 dark:text-dark-400">
            {{ t('publicTransit.passiveEmpty') }}
          </p>
        </div>
      </section>
    </template>

    <div v-else-if="monitoring" class="px-5 py-12 text-center sm:px-6">
      <p class="text-sm font-medium text-gray-700 dark:text-gray-200">{{ t('publicTransit.passiveUnavailable') }}</p>
      <p class="mt-1 text-xs text-gray-500 dark:text-dark-400">{{ error || t('publicTransit.passiveUnavailableDesc') }}</p>
    </div>
    <div v-else class="px-5 py-12 text-center sm:px-6">
      <p class="text-sm text-gray-500 dark:text-dark-400">{{ error || t('publicTransit.passiveUnavailableDesc') }}</p>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import FilterMultiSelect from '@/features/channel-monitor-v2/FilterMultiSelect.vue'
import MetricCell from '@/features/channel-monitor-v2/MetricCell.vue'
import RelayPulseMatrix from '@/features/channel-monitor-v2/RelayPulseMatrix.vue'
import type { HealthState, MonitorMatrixRow } from '@/api/channelMonitorV2'
import type {
  PublicTransitPassiveBucket,
  PublicTransitGroup,
  PublicTransitPassiveDisclosure,
  PublicTransitPassiveGroup,
  PublicTransitPassiveModel,
} from '@/api/publicTransit'
import {
  formatLatencyKpiSecondary,
  formatLatencyPrivacy,
  formatMonitorMs,
  formatMonitorPercent,
  formatMonitorThroughput,
  formatMonitorTokensPerSecond,
} from '@/features/channel-monitor-v2/monitorFormat'

type PassiveRange = '90m' | '24h' | '7d' | '30d'
type PassiveMetric = 'overall' | 'error' | 'ttft' | 'cache'
type PassiveTab = 'groups' | 'models'

const props = withDefaults(
  defineProps<{
    monitoring: PublicTransitPassiveDisclosure | null
    groups: PublicTransitGroup[]
    range: PassiveRange
    loading?: boolean
    error?: string
  }>(),
  { loading: false, error: '' },
)

const emit = defineEmits<{ 'update:range': [value: PassiveRange] }>()
const { t } = useI18n()

const ranges: PassiveRange[] = ['90m', '24h', '7d', '30d']
const metrics: PassiveMetric[] = ['overall', 'error', 'ttft', 'cache']
const tabs: PassiveTab[] = ['groups', 'models']
const platformFilters = ref<string[]>([])
const groupFilters = ref<string[]>([])
const modelFilters = ref<string[]>([])
const metric = ref<PassiveMetric>('overall')
const activeTab = ref<PassiveTab>('groups')

const sourceGroups = computed(() => props.monitoring?.groups || [])
const sourceModels = computed(() => props.monitoring?.models || [])
const exactMatrix = computed(() => props.monitoring?.matrix || null)
const exactHealthMode = computed<'overall' | 'success' | 'ttft' | 'cache'>(() =>
  metric.value === 'error' ? 'success' : metric.value,
)
const exactMatrixRows = computed<MonitorMatrixRow[]>(() => {
  const rows = exactMatrix.value?.items || []
  return rows
    .filter((row) => matchesPlatform(row.platform))
    .filter((row) => !row.group_name || matchesGroup(row.platform, row.group_name))
    .filter((row) => {
      if (selectedModels.value.size === 0) return true
      if (row.model) return selectedModels.value.has(modelKey(row.platform, row.model))
      return sourceModels.value.some((model) =>
        model.platform === row.platform && model.group_name === row.group_name && matchesModel(model),
      )
    })
})
const hasFilters = computed(() => platformFilters.value.length + groupFilters.value.length + modelFilters.value.length > 0)

const platformOptions = computed(() => {
  const values = new Map<string, number>()
  for (const item of sourceGroups.value) values.set(item.platform, (values.get(item.platform) || 0) + item.request_count)
  for (const item of sourceModels.value) if (!values.has(item.platform)) values.set(item.platform, item.request_count)
  return [...values.entries()].sort(([a], [b]) => a.localeCompare(b)).map(([value, count]) => ({ value, label: platformLabel(value), count }))
})

const groupOptions = computed(() => sourceGroups.value
  .slice()
  .sort((a, b) => `${a.platform}/${a.name}`.localeCompare(`${b.platform}/${b.name}`))
  .map((item) => ({ value: groupKey(item.platform, item.name), label: `${platformLabel(item.platform)} / ${item.name}`, count: item.request_count })))

const modelOptions = computed(() => {
  const values = new Map<string, { label: string; count: number }>()
  for (const item of sourceModels.value) {
    const value = modelKey(item.platform, item.model)
    const current = values.get(value)
    values.set(value, { label: `${platformLabel(item.platform)} / ${item.model}`, count: (current?.count || 0) + item.request_count })
  }
  return [...values.entries()].sort(([, a], [, b]) => a.label.localeCompare(b.label)).map(([value, item]) => ({ value, ...item }))
})

const selectedPlatforms = computed(() => new Set(platformFilters.value))
const selectedGroups = computed(() => new Set(groupFilters.value))
const selectedModels = computed(() => new Set(modelFilters.value))

const filteredGroups = computed(() => sourceGroups.value
  .filter((item) => matchesPlatform(item.platform) && matchesGroup(item.platform, item.name) && matchesModelForGroup(item))
  .slice()
  .sort((a, b) => metricValue(b) - metricValue(a)))

const filteredModels = computed(() => sourceModels.value
  .filter((item) => matchesPlatform(item.platform) && matchesGroup(item.platform, item.group_name) && matchesModel(item))
  .slice()
  .sort((a, b) => metricValue(b) - metricValue(a)))

const activeRows = computed(() => activeTab.value === 'groups' ? filteredGroups.value : filteredModels.value)
const errorRate = computed(() => {
  const rate = props.monitoring?.window.success_rate
  return typeof rate === 'number' && Number.isFinite(rate) ? Math.max(0, 1 - rate) : 0
})
const cacheRate = computed(() => {
  let input = 0
  let created = 0
  let read = 0
  for (const group of props.groups) {
    if (!matchesPlatform(group.platform) || !matchesGroup(group.platform, group.name)) continue
    const usage = cacheWindow(group, props.range)
    input += usage.input_tokens
    created += usage.cache_creation_tokens
    read += usage.cache_read_tokens
  }
  const total = input + created + read
  return total > 0 ? (read / total) * 100 : 0
})
const rpm = computed(() => {
  const requests = props.monitoring?.window.request_count || 0
  const minutes = props.range === '90m' ? 90 : props.range === '7d' ? 7 * 24 * 60 : props.range === '30d' ? 30 * 24 * 60 : 24 * 60
  return requests / minutes
})
const timelineStarts = computed(() => {
  const window = props.monitoring?.window
  if (!window) return []
  const step = Math.max(60, window.bucket_seconds || fallbackBucketSeconds(props.range)) * 1000
  const end = new Date(window.end).getTime()
  if (!Number.isFinite(end)) return []
  const count = props.range === '90m' ? 18 : props.range === '7d' ? 28 : props.range === '30d' ? 30 : 24
  const last = Math.floor((end - 1) / step) * step
  return Array.from({ length: count }, (_, index) => last - (count - index - 1) * step)
})
const pulseTrackStyle = computed(() => ({
  gridTemplateColumns: `repeat(${Math.max(1, timelineStarts.value.length)}, minmax(7px, 1fr))`,
}))
const timelineAxisStart = computed(() => timelineStarts.value.length ? formatAxisTime(timelineStarts.value[0]) : '')
const timelineAxisEnd = computed(() => timelineStarts.value.length ? formatAxisTime(timelineStarts.value[timelineStarts.value.length - 1]) : '')
const bucketSizeLabel = computed(() => {
  const seconds = props.monitoring?.window.bucket_seconds || fallbackBucketSeconds(props.range)
  if (seconds < 3600) return t('publicTransit.passiveBucketMinutes', { count: Math.round(seconds / 60) })
  if (seconds < 86400) return t('publicTransit.passiveBucketHours', { count: Math.round(seconds / 3600) })
  return t('publicTransit.passiveBucketDays', { count: Math.round(seconds / 86400) })
})

function clearFilters() {
  platformFilters.value = []
  groupFilters.value = []
  modelFilters.value = []
}

function matchesPlatform(platform: string) {
  return selectedPlatforms.value.size === 0 || selectedPlatforms.value.has(platform)
}

function matchesGroup(platform: string, name?: string) {
  return selectedGroups.value.size === 0 || selectedGroups.value.has(groupKey(platform, name || ''))
}

function matchesModel(item: PublicTransitPassiveModel) {
  return selectedModels.value.size === 0 || selectedModels.value.has(modelKey(item.platform, item.model))
}

function matchesModelForGroup(item: PublicTransitPassiveGroup) {
  if (selectedModels.value.size === 0) return true
  return sourceModels.value.some((model) => model.platform === item.platform && model.group_name === item.name && matchesModel(model))
}

function groupKey(platform: string, name: string) {
  return `${platform}:${name}`
}

function modelKey(platform: string, model: string) {
  return `${platform}:${model}`
}

function metricValue(item: PublicTransitPassiveGroup | PublicTransitPassiveModel) {
  if (metric.value === 'error') return 1 - item.success_rate
  if (metric.value === 'ttft') return item.avg_ttft_ms || 0
  if (metric.value === 'cache') return item.success_rate
  return item.request_count
}

function cacheWindow(group: PublicTransitGroup, range: PassiveRange) {
  if (range === '7d') return group.cache_usage.last_7d
  if (range === '30d') return group.cache_usage.total || group.cache_usage.last_7d
  return group.cache_usage.last_24h
}

function cacheRateForGroup(name: string, platform: string) {
  const group = props.groups.find((item) => item.name === name && item.platform === platform)
  if (!group) return 0
  const usage = cacheWindow(group, props.range)
  const total = usage.input_tokens + usage.cache_creation_tokens + usage.cache_read_tokens
  return total > 0 ? (usage.cache_read_tokens / total) * 100 : 0
}

function fallbackBucketSeconds(value: PassiveRange) {
  if (value === '90m') return 5 * 60
  if (value === '7d') return 6 * 60 * 60
  if (value === '30d') return 24 * 60 * 60
  return 60 * 60
}

function bucketForGroup(group: PublicTransitPassiveGroup, start: number) {
  return (group.buckets || []).find((bucket) => new Date(bucket.start).getTime() === start)
}

function bucketTone(bucket?: PublicTransitPassiveBucket) {
  if (!bucket || bucket.request_count <= 0) return 'pulse-empty'
  if (metric.value === 'ttft') {
    const ttft = bucket.avg_ttft_ms
    if (ttft == null) return 'pulse-empty'
    if (ttft <= 500) return 'pulse-healthy'
    if (ttft <= 1000) return 'pulse-good'
    if (ttft <= 2000) return 'pulse-warning'
    if (ttft <= 4000) return 'pulse-poor'
    return 'pulse-critical'
  }
  if (metric.value === 'cache') {
    if (bucket.cache_hit_rate >= 0.4) return 'pulse-healthy'
    if (bucket.cache_hit_rate >= 0.25) return 'pulse-good'
    if (bucket.cache_hit_rate >= 0.1) return 'pulse-warning'
    if (bucket.cache_hit_rate > 0) return 'pulse-poor'
    return 'pulse-critical'
  }
  if (bucket.success_rate >= 0.99) return 'pulse-healthy'
  if (bucket.success_rate >= 0.97) return 'pulse-good'
  if (bucket.success_rate >= 0.95) return 'pulse-warning'
  if (bucket.success_rate >= 0.9) return 'pulse-poor'
  return 'pulse-critical'
}

function bucketTooltip(bucket: PublicTransitPassiveBucket | undefined, start: number) {
  const rangeLabel = formatBucketRange(start)
  if (!bucket) return `${rangeLabel} · ${t('publicTransit.passiveNoTraffic')}`
  return [
    rangeLabel,
    `${t('publicTransit.passiveRequests')} ${formatInteger(bucket.request_count)}`,
    `${t('publicTransit.passiveSuccessRate')} ${formatRate(bucket.success_rate)}`,
    `${t('publicTransit.passiveErrorRate')} ${formatRate(1 - bucket.success_rate)}`,
    `${t('publicTransit.passiveAvgTTFT')} ${formatMilliseconds(bucket.avg_ttft_ms)}`,
    `${t('publicTransit.passiveAvgLatency')} ${formatMilliseconds(bucket.avg_latency_ms)}`,
    `${t('publicTransit.passiveCacheRate')} ${formatRate(bucket.cache_hit_rate)}`,
  ].join(' · ')
}

function formatAxisTime(value: number) {
  return new Intl.DateTimeFormat(undefined, props.range === '30d'
    ? { month: '2-digit', day: '2-digit' }
    : { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' }).format(new Date(value))
}

function formatBucketRange(start: number) {
  const seconds = props.monitoring?.window.bucket_seconds || fallbackBucketSeconds(props.range)
  const end = start + seconds * 1000
  return `${formatAxisTime(start)}–${formatAxisTime(end)}`
}

function healthForSuccess(rate: number): HealthState {
  if (!Number.isFinite(rate)) return 'unknown'
  if (rate >= 0.99) return 'healthy'
  if (rate >= 0.95) return 'warning'
  return 'critical'
}

function healthState(value?: string): HealthState {
  return value === 'healthy' || value === 'warning' || value === 'critical' ? value : 'unknown'
}

function statusDot(rate: number) {
  const state = healthForSuccess(rate)
  return `health-${state}`
}

function platformLabel(platform: string) {
  switch (platform) {
    case 'anthropic': return 'Anthropic'
    case 'openai': return 'OpenAI'
    case 'gemini': return 'Gemini'
    case 'antigravity': return 'Antigravity'
    case 'grok': return 'Grok'
    default: return platform
  }
}

function formatRate(value: number) {
  if (!Number.isFinite(value)) return '-'
  return `${(value * 100).toFixed(1)}%`
}

function formatRateValue(value: number) {
  if (!Number.isFinite(value)) return '-'
  return value >= 1000 ? `${(value / 1000).toFixed(1)}K` : value.toFixed(1)
}

function formatPercent(value: number) {
  if (!Number.isFinite(value)) return '-'
  return `${value.toFixed(1)}%`
}

function formatInteger(value: number) {
  return new Intl.NumberFormat().format(value || 0)
}

function formatMilliseconds(value?: number) {
  return value == null || !Number.isFinite(value) ? '-' : value >= 1000 ? `${(value / 1000).toFixed(1)}s` : `${Math.round(value)}ms`
}

function formatDate(value: string) {
  if (!value) return '-'
  return new Intl.DateTimeFormat(undefined, { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' }).format(new Date(value))
}
</script>

<style scoped>
.status-dot {
  display: inline-block;
  height: 0.5rem;
  width: 0.5rem;
  flex: none;
  border-radius: 9999px;
}
.health-healthy { background: #22c55e; }
.health-warning { background: #f59e0b; }
.health-critical { background: #ef4444; }
.health-unknown { background: #9ca3af; }
.pulse-track {
  display: grid;
  min-width: 340px;
  gap: 2px;
}
.pulse-cell {
  display: block;
  height: 18px;
  min-width: 0;
  border-radius: 3px;
  transition: filter 160ms ease-out, transform 160ms ease-out;
}
.pulse-cell:hover,
.pulse-cell:focus-visible {
  filter: brightness(0.94) saturate(1.08);
  outline: 2px solid rgba(37, 99, 235, 0.55);
  outline-offset: 1px;
  transform: translateY(-1px);
}
.pulse-healthy { background: #16a34a; }
.pulse-good { background: #84cc16; }
.pulse-warning { background: #f59e0b; }
.pulse-poor { background: #fb7185; }
.pulse-critical { background: #ef4444; }
.pulse-empty { background: #c7cdd6; }
:global(.dark) .pulse-empty { background: #4b5563; }
.score-legend {
  background: linear-gradient(90deg, #ef4444 0%, #fb7185 24%, #f59e0b 48%, #a3e635 72%, #16a34a 100%);
}
@media (prefers-reduced-motion: reduce) {
  .pulse-cell { transition: none; }
}
</style>

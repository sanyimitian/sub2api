<template>
  <AppLayout>
    <div class="min-w-0 space-y-5 pb-8">
      <header class="rounded-3xl bg-white p-5 shadow-sm ring-1 ring-gray-900/5 dark:bg-dark-800 dark:ring-dark-700 sm:p-6">
        <div class="flex flex-wrap items-start justify-between gap-4">
          <div class="min-w-0">
            <h1 class="flex items-center gap-2 text-xl font-black text-gray-900 dark:text-white"><span class="inline-flex h-8 w-8 items-center justify-center rounded-xl bg-blue-50 text-blue-500 dark:bg-blue-900/30 dark:text-blue-400"><Icon name="lightbulb" size="sm" /></span>{{ t('admin.intelligenceStatus.title') }}</h1>
            <p class="mt-2 text-xs leading-5 text-gray-500 dark:text-gray-400">{{ t('admin.intelligenceStatus.description') }}</p>
          </div>
          <div class="flex flex-wrap items-center gap-3">
            <label class="inline-flex items-center gap-2 text-xs text-gray-500"><input v-model="autoRefresh" type="checkbox" class="rounded border-gray-300 text-primary-600" data-testid="auto-refresh">{{ t('admin.intelligenceStatus.autoRefresh') }}</label>
            <button type="button" class="btn btn-secondary gap-1.5 text-xs" data-testid="refresh-status" :disabled="refreshing" @click="loadStatuses()"><Icon name="refresh" size="sm" :class="refreshing ? 'animate-spin' : ''" aria-hidden="true" />{{ t('admin.intelligenceStatus.refresh') }}</button>
          </div>
        </div>
        <div class="mt-4 flex flex-wrap items-center gap-x-5 gap-y-2 border-t border-gray-100 pt-4 text-xs dark:border-dark-700" aria-live="polite">
          <span class="font-medium text-gray-700 dark:text-gray-200">{{ t('admin.intelligenceStatus.totalPlans', { count: items.length }) }}</span>
          <span class="text-emerald-600 dark:text-emerald-400">{{ t('admin.intelligenceStatus.successCount', { count: successCount }) }}</span>
          <span class="text-red-600 dark:text-red-400">{{ t('admin.intelligenceStatus.failedCount', { count: failedCount }) }}</span>
          <span class="text-gray-500">{{ t('admin.intelligenceStatus.notTestedCount', { count: items.length - successCount - failedCount }) }}</span>
          <span v-if="updatedAt" class="sm:ml-auto text-gray-400">{{ t('admin.intelligenceStatus.updatedAt', { time: formatDateTime(updatedAt) }) }}</span>
        </div>
      </header>

      <div v-if="loadError" role="alert" class="rounded-xl border border-red-200 bg-red-50 p-4 text-sm text-red-700 dark:border-red-900 dark:bg-red-900/20 dark:text-red-400">{{ loadError }}</div>
      <div v-if="loading" role="status" class="rounded-2xl border border-gray-200 bg-white py-16 text-center text-sm text-gray-500 dark:border-dark-700 dark:bg-dark-800">{{ t('common.loading') }}</div>
      <div v-else-if="!items.length && !loadError" class="rounded-2xl border border-dashed border-gray-300 bg-white px-6 py-16 text-center dark:border-dark-700 dark:bg-dark-800"><Icon name="lightbulb" size="lg" class="mx-auto mb-3 text-gray-400" /><p class="text-sm font-medium text-gray-700 dark:text-gray-200">{{ t('admin.intelligenceStatus.empty') }}</p><p class="mt-2 text-xs text-gray-500">{{ t('admin.intelligenceStatus.emptyHelp') }}</p></div>
      <div v-else class="grid min-w-0 grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4" data-testid="intelligence-grid">
        <IntelligenceStatusCard v-for="item in items" :key="item.plan.id" :item="item" :running="isRunning(item)" :error="runErrors[item.plan.id]" :toggling="togglingIds.has(item.plan.id)" :toggle-error="toggleErrors[item.plan.id]" @run="runNow(item)" @history="openHistory(item)" @toggle-enabled="toggleEnabled(item, $event)" />
      </div>
    </div>

    <DegradationTestsPanel :show="historyTarget !== null" :account-id="historyTarget?.plan.account_id ?? null" :account-name="historyTarget?.account_name" :initial-plan-id="historyTarget?.plan.id" :model-options="historyModels" @close="closeHistory" />
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import IntelligenceStatusCard from '@/components/admin/IntelligenceStatusCard.vue'
import DegradationTestsPanel from '@/components/admin/account/DegradationTestsPanel.vue'
import type { SelectOption } from '@/components/common/Select.vue'
import { Icon } from '@/components/icons'
import { adminAPI } from '@/api/admin'
import { useAppStore } from '@/stores/app'
import type { DegradationTestStatus } from '@/types'
import { extractApiErrorMessage } from '@/utils/apiError'
import { formatDateTime } from '@/utils/format'
import { useDegradationTestRuns } from '@/composables/useDegradationTestRuns'

const { t } = useI18n()
const appStore = useAppStore()
const items = ref<DegradationTestStatus[]>([])
const loading = ref(true)
const refreshing = ref(false)
const autoRefresh = ref(true)
const loadError = ref('')
const updatedAt = ref('')
const runs = useDegradationTestRuns()
const pendingResults = new Map<number, number | null>()
const runErrors = reactive<Record<number, string>>({})
const togglingIds = reactive(new Set<number>())
const toggleErrors = reactive<Record<number, string>>({})
const historyTarget = ref<DegradationTestStatus | null>(null)
const historyModels = ref<SelectOption[]>([])
const now = ref(Date.now())
const successCount = computed(() => items.value.filter(item => item.latest_result?.status === 'success').length)
const failedCount = computed(() => items.value.filter(item => item.latest_result && item.latest_result.status !== 'success').length)
let active = true
let statusController: AbortController | null = null
let statusRequestId = 0
let historyRequestId = 0
let pollTimer: ReturnType<typeof setInterval> | undefined
let lastStatusRequestAt = 0

function isRunning(item: DegradationTestStatus): boolean {
  return runs.isRunning(item.plan, now.value)
}

function invalidateStatusRequest() {
  statusController?.abort()
  statusController = null
  statusRequestId++
  refreshing.value = false
}

async function loadStatuses() {
  if (!active) return
  statusController?.abort()
  const controller = new AbortController()
  statusController = controller
  const request = ++statusRequestId
  const fence = runs.fence()
  lastStatusRequestAt = Date.now()
  refreshing.value = true
  try {
    const data = await adminAPI.degradationTests.listStatus(controller.signal)
    if (!active || controller.signal.aborted || request !== statusRequestId) return
    runs.observe(data.map(item => item.plan), fence)
    items.value = data
    for (const planId of pendingResults.keys()) {
      if (!data.some(item => item.plan.id === planId)) pendingResults.delete(planId)
    }
    for (const item of data) {
      if (!pendingResults.has(item.plan.id) || isRunning(item)) continue
      if (item.latest_result && item.latest_result.id !== pendingResults.get(item.plan.id)) delete runErrors[item.plan.id]
      else if (!runErrors[item.plan.id]) runErrors[item.plan.id] = t('admin.intelligenceStatus.resultUnavailable')
      pendingResults.delete(item.plan.id)
    }
    loadError.value = ''
    updatedAt.value = new Date().toISOString()
  } catch (error) {
    if (!active || controller.signal.aborted || request !== statusRequestId) return
    loadError.value = extractApiErrorMessage(error, t('admin.intelligenceStatus.loadFailed'))
  } finally {
    if (active && request === statusRequestId) {
      loading.value = false
      refreshing.value = false
      statusController = null
    }
  }
}

async function runNow(item: DegradationTestStatus) {
  const planId = item.plan.id
  if (!active || !runs.begin(item.plan)) return
  pendingResults.set(planId, item.latest_result?.id ?? null)
  delete runErrors[planId]
  // An earlier status response must not replace this newly completed execution.
  invalidateStatusRequest()
  try {
    const plan = await adminAPI.degradationTests.run(planId)
    runs.accepted(plan)
    if (!active) return
    invalidateStatusRequest()
    items.value = items.value.map(current => current.plan.id === planId ? { ...current, plan } : current)
    appStore.showSuccess(t('admin.intelligenceStatus.runStarted'))
  } catch (error) {
    runs.uncertain(planId)
    if (!active) return
    runErrors[planId] = extractApiErrorMessage(error, t('admin.intelligenceStatus.runFailed'))
    appStore.showError(runErrors[planId])
  } finally {
    if (active) {
      // Also reconcile saved results when the execution response was lost.
      await loadStatuses()
    }
  }
}

async function toggleEnabled(item: DegradationTestStatus, enabled: boolean) {
  const planId = item.plan.id
  const current = items.value.find(status => status.plan.id === planId)
  if (!active || togglingIds.has(planId) || !current || current.plan.enabled === enabled) return
  togglingIds.add(planId)
  delete toggleErrors[planId]
  invalidateStatusRequest()
  try {
    const plan = await adminAPI.degradationTests.update(planId, { enabled })
    if (!active) return
    // A refresh started while saving must not restore the previous enabled state.
    invalidateStatusRequest()
    items.value = items.value.map(status => status.plan.id === planId ? { ...status, plan } : status)
    appStore.showSuccess(t(`admin.intelligenceStatus.${plan.enabled ? 'scheduleEnabled' : 'scheduleDisabled'}`))
  } catch (error) {
    if (!active) return
    toggleErrors[planId] = extractApiErrorMessage(error, t('admin.intelligenceStatus.scheduleUpdateFailed'))
    appStore.showError(toggleErrors[planId])
  } finally {
    if (active) {
      togglingIds.delete(planId)
      await loadStatuses()
    }
  }
}

async function openHistory(item: DegradationTestStatus) {
  historyTarget.value = item
  historyModels.value = []
  const request = ++historyRequestId
  try {
    const models = await adminAPI.accounts.getAvailableModels(item.plan.account_id)
    if (!active || request !== historyRequestId || historyTarget.value?.plan.account_id !== item.plan.account_id) return
    historyModels.value = models.map(model => ({ value: model.id, label: model.display_name || model.id }))
  } catch {
    // The plan editor also accepts a manually entered model ID.
    if (active && request === historyRequestId) historyModels.value = []
  }
}

function closeHistory() {
  historyRequestId++
  historyTarget.value = null
  historyModels.value = []
  void loadStatuses()
}

onMounted(() => {
  void loadStatuses()
  pollTimer = setInterval(() => {
    const wasRunning = items.value.some(isRunning)
    now.value = Date.now()
    const running = items.value.some(isRunning)
    const scheduledRefresh = autoRefresh.value && !document.hidden && !historyTarget.value && Date.now() - lastStatusRequestAt >= 15000
    if ((running || wasRunning || pendingResults.size > 0 || scheduledRefresh) && !refreshing.value && !togglingIds.size) void loadStatuses()
  }, 5000)
})

onBeforeUnmount(() => {
  active = false
  statusController?.abort()
  statusRequestId++
  historyRequestId++
  if (pollTimer) clearInterval(pollTimer)
})
</script>

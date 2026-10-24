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
        <IntelligenceStatusCard v-for="item in items" :key="item.plan.id" :item="item" :running="isRunning(item)" :error="runErrors[item.plan.id]" @run="runNow(item)" @history="openHistory(item)" />
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

const { t } = useI18n()
const appStore = useAppStore()
const items = ref<DegradationTestStatus[]>([])
const loading = ref(true)
const refreshing = ref(false)
const autoRefresh = ref(true)
const loadError = ref('')
const updatedAt = ref('')
const runningIds = reactive(new Set<number>())
const runErrors = reactive<Record<number, string>>({})
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

function isRunning(item: DegradationTestStatus): boolean {
  return runningIds.has(item.plan.id) || (!!item.plan.running_until && new Date(item.plan.running_until).getTime() > now.value)
}

async function loadStatuses() {
  if (!active) return
  statusController?.abort()
  const controller = new AbortController()
  statusController = controller
  const request = ++statusRequestId
  refreshing.value = true
  try {
    const data = await adminAPI.degradationTests.listStatus(controller.signal)
    if (!active || controller.signal.aborted || request !== statusRequestId) return
    items.value = data
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
  if (!active || isRunning(item)) return
  runningIds.add(planId)
  delete runErrors[planId]
  // An earlier status response must not replace this newly completed execution.
  statusController?.abort()
  statusRequestId++
  refreshing.value = false
  try {
    const result = await adminAPI.degradationTests.run(planId)
    if (!active) return
    items.value = items.value.map(current => current.plan.id === planId ? { ...current, latest_result: result } : current)
    if (result.status === 'success') appStore.showSuccess(t('admin.intelligenceStatus.runSuccess'))
    else appStore.showError(result.error_message || t('admin.intelligenceStatus.runFailed'))
  } catch (error) {
    if (!active) return
    runErrors[planId] = extractApiErrorMessage(error, t('admin.intelligenceStatus.runFailed'))
    appStore.showError(runErrors[planId])
  } finally {
    if (active) {
      runningIds.delete(planId)
      // Also reconcile saved results when the execution response was lost.
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
    now.value = Date.now()
    if (autoRefresh.value && !document.hidden && !refreshing.value && !historyTarget.value) void loadStatuses()
  }, 15000)
})

onBeforeUnmount(() => {
  active = false
  statusController?.abort()
  statusRequestId++
  historyRequestId++
  if (pollTimer) clearInterval(pollTimer)
})
</script>

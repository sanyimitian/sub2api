<template>
  <BaseDialog :show="show" :title="t('admin.degradationTests.title')" width="full" @close="emit('close')">
    <div class="space-y-4">
      <div class="flex flex-wrap items-start justify-between gap-3">
        <div>
          <p class="font-medium text-gray-900 dark:text-gray-100">{{ t('admin.degradationTests.account') }}: {{ accountName || `#${accountId}` }}</p>
          <p class="mt-1 text-sm text-gray-500">{{ t('admin.degradationTests.description') }}</p>
        </div>
        <button class="btn btn-primary" data-testid="add-plan" @click="showAddForm = !showAddForm">
          {{ t('admin.degradationTests.addPlan') }}
        </button>
      </div>

      <div v-if="showAddForm" class="rounded-xl border border-primary-200 bg-primary-50/50 p-4 dark:border-primary-800 dark:bg-primary-900/20">
        <h3 class="mb-3 font-medium">{{ t('admin.degradationTests.addPlan') }}</h3>
        <DegradationTestPlanForm :model-options="modelOptions" :busy="saving" @submit="handleCreate" @cancel="showAddForm = false" />
      </div>

      <p v-if="panelError" role="alert" class="whitespace-pre-wrap break-words text-sm text-red-600">{{ panelError }}</p>
      <div v-if="loading" class="py-8 text-center text-sm text-gray-500">{{ t('common.loading') }}</div>
      <div v-else-if="!plans.length" class="rounded-xl border border-dashed border-gray-300 py-10 text-center text-sm text-gray-500 dark:border-dark-600">
        {{ t('admin.degradationTests.noPlans') }}
      </div>

      <div v-else class="space-y-3">
        <section v-for="plan in plans" :key="plan.id" class="rounded-xl border border-l-4 border-gray-200 bg-white dark:border-dark-600 dark:bg-dark-800" :class="expandedPlanId === plan.id && results.length ? (results[0]?.status === 'success' ? '!border-l-emerald-500' : '!border-l-amber-500') : ''">
          <div class="flex cursor-pointer flex-wrap items-center justify-between gap-3 px-4 py-3" @click="toggleExpand(plan.id)">
            <button class="min-w-0 text-left" :aria-expanded="expandedPlanId === plan.id" @click.stop="toggleExpand(plan.id)">
              <span class="block break-all text-sm font-semibold">{{ plan.model_id }}</span>
              <span class="mt-1 block text-xs text-gray-500">{{ scheduleLabel(plan.cron_expression) }}</span>
            </button>
            <div class="flex flex-wrap items-center gap-2" @click.stop>
              <label class="flex items-center gap-1.5 text-xs text-gray-500">
                <Toggle :model-value="plan.enabled" :disabled="saving" @update:model-value="handleToggleEnabled(plan, $event)" />
                {{ plan.enabled ? t('admin.degradationTests.enabled') : t('admin.degradationTests.disabled') }}
              </label>
              <button class="btn btn-primary text-xs" :data-testid="`run-plan-${plan.id}`" :disabled="isRunning(plan) || saving" @click="runNow(plan)">
                {{ isRunning(plan) ? t('admin.degradationTests.running') : t('admin.degradationTests.runNow') }}
              </button>
              <button class="btn btn-secondary text-xs" :disabled="saving" @click="editingPlanId = editingPlanId === plan.id ? null : plan.id">{{ t('common.edit') }}</button>
              <button class="rounded-lg p-2 text-gray-400 hover:text-red-600" :title="t('admin.degradationTests.deletePlan')" :aria-label="t('admin.degradationTests.deletePlan')" :disabled="isRunning(plan) || saving" @click="deletingPlan = plan">
                <Icon name="trash" size="sm" />
              </button>
            </div>
          </div>
          <div class="flex flex-wrap gap-x-5 gap-y-1 px-4 pb-3 text-xs text-gray-500">
            <span v-if="plan.last_run_at">{{ t('admin.degradationTests.lastRun') }}: {{ formatDateTime(plan.last_run_at) }}</span>
            <span v-if="plan.enabled && plan.next_run_at">{{ t('admin.degradationTests.nextRun') }}: {{ formatDateTime(plan.next_run_at) }}</span>
          </div>

          <div v-if="editingPlanId === plan.id" class="border-t border-gray-100 p-4 dark:border-dark-700">
            <DegradationTestPlanForm :key="plan.id" :initial="plan" :model-options="modelOptions" :busy="saving" @submit="handleEdit(plan.id, $event)" @cancel="editingPlanId = null" />
          </div>

          <div v-if="expandedPlanId === plan.id" class="border-t border-gray-100 p-4 dark:border-dark-700">
            <div class="mb-3 flex items-center justify-between">
              <h3 class="text-sm font-medium">{{ t('admin.degradationTests.recentResults') }}</h3>
              <button class="text-xs text-primary-600 hover:underline" :disabled="loadingResults" @click="loadResults(plan.id)">{{ t('admin.degradationTests.refresh') }}</button>
            </div>
            <p v-if="resultsError" role="alert" class="mb-3 whitespace-pre-wrap break-words text-sm text-red-600">{{ resultsError }}</p>
            <div v-if="loadingResults" class="py-4 text-center text-sm text-gray-500">{{ t('common.loading') }}</div>
            <p v-else-if="!results.length" class="py-4 text-center text-sm text-gray-500">{{ t('admin.degradationTests.noResults') }}</p>
            <DegradationTestResultsGallery v-else :results="results" @details="openDetails(plan.id)" />
          </div>
        </section>
      </div>
    </div>
  </BaseDialog>
  <BaseDialog :show="detailsPlanId !== null" :title="t('admin.degradationTests.allResults')" width="full" :z-index="60" @close="closeDetails">
    <div v-if="detailsPlanId !== null" class="space-y-4" data-testid="results-details">
      <div class="flex items-center justify-between gap-3">
        <p class="break-all text-sm text-gray-500">{{ accountName || `#${accountId}` }} · {{ plans.find(plan => plan.id === detailsPlanId)?.model_id }}</p>
        <button class="shrink-0 text-xs text-primary-600 hover:underline" :disabled="detailsLoading" @click="detailsPlanId !== null && loadDetails(detailsPlanId)">{{ t('admin.degradationTests.refresh') }}</button>
      </div>
      <p v-if="detailsError" role="alert" class="whitespace-pre-wrap break-words text-sm text-red-600">{{ detailsError }}</p>
      <p v-if="detailsLoading" class="py-4 text-center text-sm text-gray-500">{{ t('admin.degradationTests.loadingDetails') }}</p>
      <p v-else-if="!detailsResults.length" class="py-4 text-center text-sm text-gray-500">{{ t('admin.degradationTests.noResults') }}</p>
      <DegradationTestResultsGallery v-else :key="detailsPlanId" :results="detailsResults" :display-limit="24" :show-details-button="false" wrap />
    </div>
  </BaseDialog>
  <ConfirmDialog :show="!!deletingPlan" :title="t('admin.degradationTests.deletePlan')" :message="t('admin.degradationTests.confirmDelete')" danger @confirm="handleDelete" @cancel="deletingPlan = null" />
</template>

<script setup lang="ts">
import { onBeforeUnmount, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Toggle from '@/components/common/Toggle.vue'
import type { SelectOption } from '@/components/common/Select.vue'
import { Icon } from '@/components/icons'
import { adminAPI } from '@/api/admin'
import { useAppStore } from '@/stores/app'
import { formatDateTime } from '@/utils/format'
import type { DegradationTestPlan, DegradationTestResult } from '@/types'
import DegradationTestPlanForm, { type DegradationTestPlanFormValue } from './DegradationTestPlanForm.vue'
import DegradationTestResultsGallery from './DegradationTestResultsGallery.vue'

const props = defineProps<{ show: boolean; accountId: number | null; accountName?: string; modelOptions: SelectOption[] }>()
const emit = defineEmits<{ close: [] }>()
const { t } = useI18n()
const appStore = useAppStore()
const plans = ref<DegradationTestPlan[]>([])
const results = ref<DegradationTestResult[]>([])
const loading = ref(false)
const saving = ref(false)
const loadingResults = ref(false)
const showAddForm = ref(false)
const editingPlanId = ref<number | null>(null)
const expandedPlanId = ref<number | null>(null)
const deletingPlan = ref<DegradationTestPlan | null>(null)
const runningPlanIds = reactive(new Set<number>())
const panelError = ref('')
const resultsError = ref('')
const detailsPlanId = ref<number | null>(null)
const detailsResults = ref<DegradationTestResult[]>([])
const detailsLoading = ref(false)
const detailsError = ref('')
let scope = 0
let plansRequestId = 0
let resultsRequestId = 0
let detailsRequestId = 0
let pollTimer: ReturnType<typeof setInterval> | undefined
let polling = false

function errorMessage(error: unknown): string {
  const err = error as { message?: string; response?: { data?: { message?: string } } }
  return err?.response?.data?.message || err?.message || t('admin.degradationTests.requestFailed')
}

function isCurrent(token: number): boolean { return token === scope && props.show }
function isRunning(plan: DegradationTestPlan): boolean {
  return runningPlanIds.has(plan.id) || (!!plan.running_until && new Date(plan.running_until).getTime() > Date.now())
}
function scheduleLabel(expression: string): string {
  const interval = expression?.match(/^@every\s+(\d+)(m|h)$/)
  return interval ? t('admin.degradationTests.intervalSummary', { value: interval[1], unit: t(`admin.degradationTests.${interval[2] === 'm' ? 'minutes' : 'hours'}`) }) : expression
}

watch(expandedPlanId, () => {
  resultsRequestId++
  results.value = []
  resultsError.value = ''
  loadingResults.value = false
}, { flush: 'sync' })

async function loadPlans(quiet = false) {
  if (!props.show || !props.accountId) return
  const token = scope
  const request = ++plansRequestId
  if (!quiet) loading.value = true
  try {
    const data = await adminAPI.degradationTests.listByAccount(props.accountId)
    if (!isCurrent(token) || request !== plansRequestId) return
    plans.value = data
    panelError.value = ''
    if (expandedPlanId.value && !data.some(plan => plan.id === expandedPlanId.value)) expandedPlanId.value = null
    if (detailsPlanId.value && !data.some(plan => plan.id === detailsPlanId.value)) closeDetails()
  } catch (error) {
    if (isCurrent(token) && request === plansRequestId) panelError.value = errorMessage(error)
  } finally {
    if (isCurrent(token) && request === plansRequestId) loading.value = false
  }
}

async function loadResults(planId: number, quiet = false) {
  const token = scope
  const request = ++resultsRequestId
  if (!quiet) loadingResults.value = true
  try {
    const data = await adminAPI.degradationTests.listResults(planId, 10)
    if (!isCurrent(token) || request !== resultsRequestId || expandedPlanId.value !== planId) return
    results.value = data.slice(0, 10)
    resultsError.value = ''
  } catch (error) {
    if (!isCurrent(token) || request !== resultsRequestId) return
    resultsError.value = errorMessage(error)
    if (!quiet) appStore.showError(resultsError.value)
  } finally {
    if (isCurrent(token) && request === resultsRequestId) loadingResults.value = false
  }
}

function openDetails(planId: number) {
  detailsPlanId.value = planId
  detailsResults.value = []
  detailsError.value = ''
  void loadDetails(planId)
}

function closeDetails() {
  detailsRequestId++
  detailsPlanId.value = null
  detailsResults.value = []
  detailsLoading.value = false
  detailsError.value = ''
}

async function loadDetails(planId: number, quiet = false) {
  const token = scope
  const request = ++detailsRequestId
  if (!quiet) detailsLoading.value = true
  try {
    const data = await adminAPI.degradationTests.listResults(planId, 24)
    if (!isCurrent(token) || request !== detailsRequestId || detailsPlanId.value !== planId) return
    detailsResults.value = data.slice(0, 24)
    detailsError.value = ''
  } catch (error) {
    if (!isCurrent(token) || request !== detailsRequestId || detailsPlanId.value !== planId) return
    detailsError.value = errorMessage(error)
  } finally {
    if (isCurrent(token) && request === detailsRequestId) detailsLoading.value = false
  }
}

function toggleExpand(planId: number) {
  if (expandedPlanId.value === planId) expandedPlanId.value = null
  else {
    expandedPlanId.value = planId
    void loadResults(planId)
  }
}

async function handleCreate(value: DegradationTestPlanFormValue) {
  if (!props.accountId || saving.value) return
  const token = scope
  saving.value = true
  try {
    await adminAPI.degradationTests.create({ account_id: props.accountId, ...value })
    if (!isCurrent(token)) return
    showAddForm.value = false
    appStore.showSuccess(t('admin.degradationTests.createSuccess'))
    await loadPlans(true)
  } catch (error) {
    if (isCurrent(token)) appStore.showError(errorMessage(error))
  } finally {
    if (isCurrent(token)) saving.value = false
  }
}

async function handleEdit(planId: number, value: Partial<DegradationTestPlanFormValue>) {
  if (saving.value) return
  const token = scope
  saving.value = true
  try {
    const updated = await adminAPI.degradationTests.update(planId, value)
    if (!isCurrent(token)) return
    plansRequestId++
    plans.value = plans.value.map(plan => plan.id === planId ? updated : plan)
    editingPlanId.value = null
    appStore.showSuccess(t('admin.degradationTests.updateSuccess'))
  } catch (error) {
    if (isCurrent(token)) appStore.showError(errorMessage(error))
  } finally {
    if (isCurrent(token)) saving.value = false
  }
}

function handleToggleEnabled(plan: DegradationTestPlan, enabled: boolean) {
  void handleEdit(plan.id, { enabled })
}

async function handleDelete() {
  if (!deletingPlan.value || saving.value) return
  const token = scope
  const planId = deletingPlan.value.id
  saving.value = true
  try {
    await adminAPI.degradationTests.delete(planId)
    if (!isCurrent(token)) return
    plansRequestId++
    plans.value = plans.value.filter(plan => plan.id !== planId)
    if (expandedPlanId.value === planId) expandedPlanId.value = null
    if (detailsPlanId.value === planId) closeDetails()
    appStore.showSuccess(t('admin.degradationTests.deleteSuccess'))
  } catch (error) {
    if (isCurrent(token)) appStore.showError(errorMessage(error))
  } finally {
    if (isCurrent(token)) { saving.value = false; deletingPlan.value = null }
  }
}

async function runNow(plan: DegradationTestPlan) {
  if (isRunning(plan)) return
  const token = scope
  runningPlanIds.add(plan.id)
  panelError.value = ''
  if (expandedPlanId.value !== plan.id) {
    expandedPlanId.value = plan.id
    void loadResults(plan.id)
  }
  try {
    const result = await adminAPI.degradationTests.run(plan.id)
    if (!isCurrent(token)) return
    if (expandedPlanId.value === plan.id) {
      resultsRequestId++
      loadingResults.value = false
      resultsError.value = ''
      results.value = [result, ...results.value.filter(item => item.id !== result.id)].slice(0, 10)
    }
    if (result.status === 'success') appStore.showSuccess(t('admin.degradationTests.runSuccess'))
    else appStore.showError(result.error_message || t('admin.degradationTests.runFailed'))
    if (detailsPlanId.value === plan.id) void loadDetails(plan.id, true)
    await loadPlans(true)
  } catch (error) {
    if (isCurrent(token)) {
      panelError.value = errorMessage(error)
      appStore.showError(panelError.value)
      // A lost HTTP response does not mean the server failed to save the result.
      if (expandedPlanId.value === plan.id) void loadResults(plan.id, true)
    }
  } finally {
    runningPlanIds.delete(plan.id)
  }
}

watch(() => [props.show, props.accountId] as const, ([visible, accountId]) => {
  scope++
  plansRequestId++
  resultsRequestId++
  closeDetails()
  if (pollTimer) clearInterval(pollTimer)
  plans.value = []
  results.value = []
  expandedPlanId.value = null
  editingPlanId.value = null
  deletingPlan.value = null
  showAddForm.value = false
  loading.value = false
  saving.value = false
  loadingResults.value = false
  panelError.value = ''
  resultsError.value = ''
  if (!visible || !accountId) return
  void loadPlans()
  pollTimer = setInterval(async () => {
    if (polling || loading.value || saving.value) return
    polling = true
    const token = scope
    try {
      await loadPlans(true)
      if (isCurrent(token) && expandedPlanId.value && !loadingResults.value) await loadResults(expandedPlanId.value, true)
      if (isCurrent(token) && detailsPlanId.value && !detailsLoading.value) await loadDetails(detailsPlanId.value, true)
    } finally { polling = false }
  }, 10000)
}, { immediate: true })

onBeforeUnmount(() => {
  scope++
  if (pollTimer) clearInterval(pollTimer)
})
</script>

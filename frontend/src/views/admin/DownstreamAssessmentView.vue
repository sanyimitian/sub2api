<template>
  <AppLayout>
    <div class="space-y-6">
      <div class="card space-y-4 p-4 sm:p-6">
        <div class="flex flex-wrap items-end gap-4">
          <div class="w-full sm:w-64">
            <label class="input-label" for="assessment-group">{{ t('admin.downstreamAssessment.group') }} <span class="text-red-500">*</span></label>
            <Select
              id="assessment-group"
              v-model="groupId"
              :options="groupOptions"
              :placeholder="t('admin.downstreamAssessment.selectGroup')"
              :loading="groupsLoading"
              searchable
            />
          </div>
          <div>
            <label class="input-label">{{ t('admin.downstreamAssessment.dateRange') }} <span class="text-red-500">*</span></label>
            <DateRangePicker v-model:start-date="startDate" v-model:end-date="endDate" />
          </div>
          <div class="w-full sm:w-64">
            <label class="input-label" for="assessment-threshold">{{ t('admin.downstreamAssessment.dailyThreshold') }} <span class="text-red-500">*</span></label>
            <input
              id="assessment-threshold"
              v-model="thresholdInput"
              class="input"
              type="text"
              inputmode="decimal"
              required
              aria-describedby="assessment-threshold-help"
              :aria-invalid="thresholdInput.trim() !== '' && dailyThreshold === null"
              :placeholder="t('admin.downstreamAssessment.thresholdPlaceholder')"
              @keyup.enter="runAssessment"
            />
          </div>
          <button type="button" class="btn btn-primary" data-testid="assess" :disabled="!canAssess || loading" @click="runAssessment">
            {{ loading ? t('common.loading') : t('admin.downstreamAssessment.calculate') }}
          </button>
        </div>
        <p id="assessment-threshold-help" class="text-sm text-gray-500 dark:text-gray-400">{{ t('admin.downstreamAssessment.thresholdHelp') }}</p>
        <p v-if="thresholdInput.trim() && dailyThreshold === null" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ t('admin.downstreamAssessment.invalidThreshold') }}</p>
        <p class="text-sm text-gray-500 dark:text-gray-400">{{ t('admin.downstreamAssessment.scopeHelp') }}</p>
        <p class="text-sm text-gray-500 dark:text-gray-400">{{ t('admin.downstreamAssessment.calculationHelp') }}</p>
        <p v-if="groupsError" role="alert" class="text-sm text-red-600 dark:text-red-400">
          {{ t('admin.downstreamAssessment.groupsError') }}
          <button type="button" class="ml-2 underline" @click="loadGroups">{{ t('common.tryAgain') }}</button>
        </p>
      </div>

      <div v-if="error" role="alert" class="card p-6 text-red-600 dark:text-red-400">{{ t('admin.downstreamAssessment.loadError') }}</div>
      <div v-else-if="loading" role="status" class="card p-8 text-center text-gray-500">{{ t('common.loading') }}</div>
      <template v-else-if="result && appliedFilters">
        <div class="grid grid-cols-2 gap-4 lg:grid-cols-4">
          <div v-for="card in summaryCards" :key="card.label" class="card min-w-0 p-4">
            <p class="text-sm text-gray-500 dark:text-gray-400">{{ card.label }}</p>
            <p class="mt-2 break-words text-2xl font-semibold tabular-nums text-gray-900 dark:text-gray-100" :title="card.tokens ? `${formatTokenAmount(card.value)} B` : formatNumber(card.value)">{{ card.tokens ? formatTokenAmount(card.value) : formatNumber(card.value) }}</p>
          </div>
        </div>
        <div class="card overflow-hidden">
          <div class="space-y-1 border-b border-gray-200 p-4 text-sm text-gray-600 dark:border-dark-700 dark:text-gray-300">
            <p>{{ t('admin.downstreamAssessment.resultSummary', { group: appliedGroupName, start: appliedFilters.start_date, end: appliedFilters.end_date, days: result.days, threshold: formatTokenAmount(result.daily_threshold) }) }}</p>
            <p>{{ t('admin.downstreamAssessment.sortHint') }}</p>
          </div>
          <div class="overflow-x-auto">
            <table class="min-w-full divide-y divide-gray-200 text-sm dark:divide-dark-700">
              <thead class="whitespace-nowrap bg-gray-50 text-left text-gray-500 dark:bg-dark-900/50 dark:text-gray-400">
                <tr>
                  <th scope="col" class="px-4 py-3">{{ t('admin.downstreamAssessment.user') }}</th>
                  <th scope="col" class="px-4 py-3 text-right">{{ t('admin.downstreamAssessment.requests') }}</th>
                  <th scope="col" class="px-4 py-3 text-right">{{ t('admin.downstreamAssessment.totalTokens') }}</th>
                  <th scope="col" class="px-4 py-3 text-right">{{ t('admin.downstreamAssessment.dailyAverage') }}</th>
                  <th scope="col" class="px-4 py-3">{{ t('admin.downstreamAssessment.status') }}</th>
                  <th scope="col" class="px-4 py-3">{{ t('common.actions') }}</th>
                </tr>
              </thead>
              <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
                <tr v-for="row in result.items" :key="row.user_id" :data-user-id="row.user_id" class="text-gray-700 dark:text-gray-200">
                  <td class="px-4 py-3"><div class="font-medium">{{ row.email }}</div><div class="mt-1 text-xs opacity-75">{{ row.username }} #{{ row.user_id }}</div></td>
                  <td class="px-4 py-3 text-right tabular-nums">{{ formatNumber(row.total_requests) }}</td>
                  <td class="px-4 py-3 text-right tabular-nums" data-testid="total-tokens-b">{{ formatTokenAmount(row.total_tokens) }}</td>
                  <td class="px-4 py-3 text-right font-medium tabular-nums" data-testid="daily-average-b">{{ formatTokenAmount(row.daily_average) }}</td>
                  <td class="whitespace-nowrap px-4 py-3">
                    <span class="badge" :class="row.qualified ? 'badge-success' : 'badge-danger'" data-testid="assessment-status">
                      {{ t(row.qualified ? 'admin.downstreamAssessment.qualified' : 'admin.downstreamAssessment.unqualified') }}
                    </span>
                  </td>
                  <td class="whitespace-nowrap px-4 py-3">
                    <button
                      type="button"
                      class="btn btn-secondary px-3 py-1.5 text-xs"
                      data-testid="configure-groups"
                      :disabled="loadingGroupUserId !== null"
                      :aria-label="t('admin.downstreamAssessment.configureUserGroups', { email: row.email })"
                      @click="openAllowedGroups(row.user_id)"
                    >
                      <Icon name="users" size="sm" />
                      {{ loadingGroupUserId === row.user_id ? t('common.loading') : t('admin.users.groups') }}
                    </button>
                  </td>
                </tr>
                <tr v-if="result.items.length === 0"><td colspan="6" class="p-8 text-center text-gray-500">{{ t('admin.downstreamAssessment.noMembers') }}</td></tr>
              </tbody>
            </table>
          </div>
          <Pagination v-if="result.total > 0" :total="result.total" :page="result.page" :page-size="result.page_size" show-jump @update:page="changePage" @update:page-size="changePageSize" />
        </div>
      </template>
      <div v-else class="card p-8 text-center text-gray-500 dark:text-gray-400">{{ t('admin.downstreamAssessment.initialHint') }}</div>
    </div>
    <UserAllowedGroupsModal :show="showAllowedGroupsModal" :user="allowedGroupsUser" @close="closeAllowedGroupsModal" @success="refreshAssessment" />
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Select from '@/components/common/Select.vue'
import DateRangePicker from '@/components/common/DateRangePicker.vue'
import Pagination from '@/components/common/Pagination.vue'
import Icon from '@/components/icons/Icon.vue'
import UserAllowedGroupsModal from '@/components/admin/user/UserAllowedGroupsModal.vue'
import { getAllIncludingInactive } from '@/api/admin/groups'
import { getById as getUserById } from '@/api/admin/users'
import { getDownstreamAssessment, type DownstreamAssessmentParams, type DownstreamAssessmentResult } from '@/api/admin/downstreamAssessment'
import { getPersistedPageSize } from '@/composables/usePersistedPageSize'
import { parseDailyTokenThresholdB, formatTokensInBillions } from '@/utils/downstreamAssessment'
import { useAppStore } from '@/stores/app'
import type { AdminGroup, AdminUser } from '@/types'

const { t, locale } = useI18n()
const appStore = useAppStore()
const groups = ref<AdminGroup[]>([])
const groupsLoading = ref(false)
const groupsError = ref(false)
const groupId = ref<number | null>(null)
const groupOptions = computed(() => groups.value.map(group => ({ value: group.id, label: group.name })))
const dateString = (date: Date) => `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`
// Default to the last seven completed calendar days for a daily assessment.
const yesterday = new Date()
yesterday.setDate(yesterday.getDate() - 1)
const weekStart = new Date(yesterday)
weekStart.setDate(weekStart.getDate() - 6)
const startDate = ref(dateString(weekStart))
const endDate = ref(dateString(yesterday))
const thresholdInput = ref('')
const dailyThreshold = computed(() => parseDailyTokenThresholdB(thresholdInput.value))
const canAssess = computed(() => !!groupId.value && dailyThreshold.value !== null && !!startDate.value && !!endDate.value && startDate.value <= endDate.value)
const pageSize = ref(getPersistedPageSize())
const loading = ref(false)
const error = ref(false)
const result = ref<DownstreamAssessmentResult | null>(null)
const appliedFilters = ref<DownstreamAssessmentParams | null>(null)
const appliedGroupName = computed(() => groups.value.find(group => group.id === appliedFilters.value?.group_id)?.name ?? '')
let controller: AbortController | null = null
let requestVersion = 0
const showAllowedGroupsModal = ref(false)
const allowedGroupsUser = ref<AdminUser | null>(null)
const loadingGroupUserId = ref<number | null>(null)
let groupRequestVersion = 0
const formatNumber = (value: number) => new Intl.NumberFormat(locale.value, { maximumFractionDigits: 2 }).format(value)
const formatTokenAmount = (value: number) => formatTokensInBillions(value, locale.value)
const summaryCards = computed(() => result.value ? [
  { label: t('admin.downstreamAssessment.members'), value: result.value.total },
  { label: t('admin.downstreamAssessment.unqualified'), value: result.value.unqualified_count },
  { label: t('admin.downstreamAssessment.qualified'), value: result.value.qualified_count },
  { label: t('admin.downstreamAssessment.totalTokens'), value: result.value.total_tokens, tokens: true }
] : [])

async function loadGroups() {
  groupsLoading.value = true
  groupsError.value = false
  try {
    groups.value = await getAllIncludingInactive()
  } catch {
    groupsError.value = true
  } finally {
    groupsLoading.value = false
  }
}

// Edited criteria invalidate both visible results and outstanding requests.
watch([groupId, startDate, endDate, thresholdInput], () => {
  closeAllowedGroupsModal()
  requestVersion++
  controller?.abort()
  loading.value = false
  result.value = null
  appliedFilters.value = null
  error.value = false
}, { flush: 'sync' })

async function fetchAssessment(params: DownstreamAssessmentParams) {
  closeAllowedGroupsModal()
  controller?.abort()
  controller = new AbortController()
  const version = ++requestVersion
  loading.value = true
  error.value = false
  try {
    const data = await getDownstreamAssessment(params, { signal: controller.signal })
    if (version !== requestVersion) return
    // Membership may shrink between page requests. Return to the last real page.
    if (data.total > 0 && params.page > data.pages) {
      await fetchAssessment({ ...params, page: data.pages })
      return
    }
    result.value = data
    appliedFilters.value = { ...params }
  } catch {
    if (version !== requestVersion) return
    result.value = null
    error.value = true
  } finally {
    if (version === requestVersion) loading.value = false
  }
}

function runAssessment() {
  if (!canAssess.value || loading.value || groupId.value === null || dailyThreshold.value === null) return
  void fetchAssessment({ group_id: groupId.value, start_date: startDate.value, end_date: endDate.value, daily_threshold: dailyThreshold.value, page: 1, page_size: pageSize.value })
}

function changePage(page: number) {
  if (!appliedFilters.value || loading.value) return
  void fetchAssessment({ ...appliedFilters.value, page })
}

function changePageSize(size: number) {
  pageSize.value = size
  if (!appliedFilters.value || loading.value) return
  void fetchAssessment({ ...appliedFilters.value, page: 1, page_size: size })
}

async function openAllowedGroups(userId: number) {
  if (loadingGroupUserId.value !== null) return
  const version = ++groupRequestVersion
  loadingGroupUserId.value = userId
  try {
    // Assessment rows contain usage only; fetch the complete, current group settings.
    const user = await getUserById(userId)
    if (version !== groupRequestVersion) return
    allowedGroupsUser.value = user
    showAllowedGroupsModal.value = true
  } catch {
    if (version === groupRequestVersion) appStore.showError(t('admin.downstreamAssessment.loadUserError'))
  } finally {
    if (version === groupRequestVersion) loadingGroupUserId.value = null
  }
}

function closeAllowedGroupsModal() {
  groupRequestVersion++
  loadingGroupUserId.value = null
  showAllowedGroupsModal.value = false
  allowedGroupsUser.value = null
}

function refreshAssessment() {
  if (appliedFilters.value) void fetchAssessment({ ...appliedFilters.value })
}

onMounted(loadGroups)
onUnmounted(() => { requestVersion++; groupRequestVersion++; controller?.abort() })
</script>

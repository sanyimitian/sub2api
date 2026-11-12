<template>
  <section class="space-y-4 rounded-xl border border-gray-200 bg-white p-5 dark:border-dark-700 dark:bg-dark-900" data-testid="user-records">
    <div class="flex flex-wrap items-center justify-between gap-3"><h2 class="text-lg font-semibold">{{ t('modelBenchmark.userRecords') }}</h2><button class="btn btn-secondary" :disabled="loading" @click="loadPage(page)">{{ t('modelBenchmark.refreshResults') }}</button></div>
    <p class="text-xs text-gray-500">{{ t('modelBenchmark.userRecordsHint') }} {{ t('modelBenchmark.retentionHint') }}</p>
    <form class="grid items-end gap-3 md:grid-cols-2 xl:grid-cols-4" @submit.prevent="search">
      <div class="space-y-2 md:col-span-1 xl:col-span-2">
        <label class="block space-y-1 text-sm"><span>{{ t('modelBenchmark.userAccount') }}</span><input v-model="accountQuery" name="account_search" maxlength="200" class="input w-full" :placeholder="t('modelBenchmark.accountSearchPlaceholder')" @input="searchAccounts" /></label>
        <select v-if="accountQuery.trim()" v-model="selectedAccount" name="account_selection" class="input w-full" :aria-label="t('modelBenchmark.selectAccount')" :disabled="accountsLoading">
          <option value="">{{ t(accountsLoading ? 'modelBenchmark.loading' : 'modelBenchmark.selectAccount') }}</option>
          <option v-for="user in accounts" :key="user.id" :value="String(user.id)">{{ user.email }}{{ user.username ? ` · ${user.username}` : '' }} · #{{ user.id }}{{ user.deleted ? ` · ${t('modelBenchmark.deletedAccount')}` : '' }}</option>
        </select>
        <p v-if="accountsError" role="alert" class="text-xs text-red-600">{{ accountsError }}</p>
        <p v-else-if="accountQuery.trim() && !accountsLoading && !accounts.length" class="text-xs text-gray-500">{{ t('modelBenchmark.noMatchingAccount') }}</p>
        <p v-if="moreAccounts" class="text-xs text-gray-500">{{ t('modelBenchmark.refineAccountSearch') }}</p>
      </div>
      <label class="block space-y-1 text-sm"><span>{{ t('modelBenchmark.model') }}</span><input v-model="modelQuery" name="model_search" maxlength="200" class="input w-full" :placeholder="t('modelBenchmark.modelSearchPlaceholder')" /></label>
      <div class="flex gap-2"><button type="submit" class="btn btn-primary" :disabled="loading || (!!accountQuery.trim() && !selectedAccount)">{{ t('modelBenchmark.searchRecords') }}</button><button type="button" class="btn btn-secondary" @click="reset">{{ t('modelBenchmark.reset') }}</button></div>
    </form>
    <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
    <p v-if="loading" role="status" class="text-sm text-gray-500">{{ t('modelBenchmark.loading') }}</p>
    <p v-else-if="!items.length && !error" class="py-8 text-center text-sm text-gray-500">{{ t('modelBenchmark.noUserRecords') }}</p>
    <div v-if="items.length" class="overflow-x-auto">
      <table class="w-full text-left text-sm"><thead class="border-b border-gray-200 text-gray-500 dark:border-dark-700"><tr><th class="p-3">{{ t('modelBenchmark.userAccount') }}</th><th class="p-3">{{ t('modelBenchmark.model') }}</th><th class="p-3">{{ t('modelBenchmark.suiteVersion') }}</th><th class="p-3">{{ t('modelBenchmark.status') }}</th><th class="p-3">{{ t('modelBenchmark.passRate') }}</th><th class="p-3">{{ t('modelBenchmark.createdAt') }}</th><th class="p-3">{{ t('modelBenchmark.actions') }}</th></tr></thead>
        <tbody><tr v-for="run in items" :key="run.id" class="border-b border-gray-100 dark:border-dark-800" :data-user-run="run.id">
          <td class="p-3"><div class="max-w-64 break-all font-medium">{{ run.owner?.email || `#${run.owner_user_id}` }}</div><div v-if="run.owner?.username" class="text-xs text-gray-500">{{ run.owner.username }}</div><div class="text-xs text-gray-500">#{{ run.owner_user_id }} <span v-if="run.owner?.site_id">· {{ t('modelBenchmark.remoteAccount', { site: run.owner.site_id }) }}</span><span v-else-if="run.owner?.deleted || run.owner?.missing">· {{ t('modelBenchmark.deletedAccount') }}</span></div></td>
          <td class="p-3"><div class="max-w-56 break-all font-mono">{{ run.model }}</div><span class="text-xs text-gray-500">{{ t('modelBenchmark.effort') }}: {{ t(`modelBenchmark.efforts.${run.effort}`) }}</span></td>
          <td class="whitespace-nowrap p-3 text-xs">{{ run.suite_day }} · v{{ run.suite_revision }}<code class="block text-gray-500" :title="run.suite_fingerprint">{{ run.suite_fingerprint.slice(0, 12) }}</code></td>
          <td class="p-3"><span>{{ t(`modelBenchmark.statuses.${run.status}`) }}</span><div class="text-xs text-gray-500">{{ run.completed }}/{{ run.total }}</div></td>
          <td class="p-3"><strong class="text-xl tabular-nums" :class="benchmarkScoreClass(run.pass_rate, run.status)" data-testid="user-pass-rate">{{ formatBenchmarkPassRate(run.pass_rate, run.status) }}</strong><div v-for="category in categories" :key="category" class="whitespace-nowrap text-xs" :class="benchmarkScoreClass(benchmarkCategoryScore(run.categories[category].passed, run.categories[category].total), run.status)">{{ t(`modelBenchmark.categories.${category}`) }} {{ run.categories[category].passed }}/{{ run.categories[category].total }}</div></td>
          <td class="whitespace-nowrap p-3 text-xs">{{ formatDate(run.created_at) }}<div class="text-gray-500">{{ t('modelBenchmark.seconds', { count: Math.round(run.elapsed_seconds) }) }}</div></td>
          <td class="p-3"><button class="text-primary-600 hover:underline" @click="openDetail(run)">{{ t('modelBenchmark.details') }}</button></td>
        </tr></tbody>
      </table>
    </div>
    <div v-if="total > 0" class="overflow-x-auto"><Pagination :total="total" :page="page" :page-size="20" :show-page-size-selector="false" show-jump @update:page="loadPage" /></div>
    <BaseDialog :show="!!selected" :title="t('modelBenchmark.details')" width="wide" @close="closeDetail">
      <div v-if="selected" class="space-y-4" data-testid="user-record-detail">
        <p class="break-all text-sm">{{ selected.owner?.email || `#${selected.owner_user_id}` }} · {{ selected.model }}</p>
        <p class="text-sm">{{ t('modelBenchmark.passRate') }}: <strong class="text-2xl" :class="benchmarkScoreClass(selected.pass_rate, selected.status)">{{ formatBenchmarkPassRate(selected.pass_rate, selected.status) }}</strong> · {{ t(`modelBenchmark.statuses.${selected.status}`) }}</p>
        <p v-if="detailLoading" role="status">{{ t('modelBenchmark.loading') }}</p><p v-if="detailError" role="alert" class="text-red-600">{{ detailError }}</p>
        <p v-if="selected.error" class="text-sm text-red-600">{{ selected.error }}</p>
        <div v-for="result in selected.results" :key="result.id" class="space-y-2 rounded-lg border border-gray-200 p-3 dark:border-dark-700"><p class="text-sm font-medium">{{ result.index }}. {{ result.title }} <span :class="result.passed ? 'text-emerald-600' : 'text-red-600'">{{ t(`modelBenchmark.resultStatuses.${result.status}`) }}</span></p><p class="text-xs text-gray-500">{{ result.message }}</p><details v-if="result.answer"><summary class="cursor-pointer text-sm text-primary-600">{{ t('modelBenchmark.answer') }}</summary><p v-if="result.answer_truncated" class="text-xs text-gray-500">{{ t('modelBenchmark.truncated') }}</p><pre class="mt-2 max-h-80 overflow-auto whitespace-pre-wrap break-words text-xs">{{ result.answer }}</pre></details></div>
      </div>
    </BaseDialog>
  </section>
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { benchmarks, type BenchmarkRun, type BenchmarkOwner, type Category } from '@/api/admin/model-benchmarks'
import { formatBenchmarkPassRate, benchmarkScoreClass, benchmarkCategoryScore } from '@/utils/benchmarkScore'
import Pagination from '@/components/common/Pagination.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
const { t } = useI18n()
const categories: Category[] = ['coding', 'math', 'reasoning']
const items = ref<BenchmarkRun[]>([])
const total = ref(0), page = ref(1), loading = ref(false), error = ref('')
const accountQuery = ref(''), selectedAccount = ref(''), modelQuery = ref('')
const accounts = ref<BenchmarkOwner[]>([]), accountsLoading = ref(false), accountsError = ref(''), moreAccounts = ref(false)
const selected = ref<BenchmarkRun | null>(null), detailLoading = ref(false), detailError = ref('')
let filters: { user_id?: number; model?: string } = {}
let listController: AbortController | undefined, accountController: AbortController | undefined, detailController: AbortController | undefined
let accountTimer: ReturnType<typeof setTimeout> | undefined
const formatDate = (value: string) => new Date(value).toLocaleString()
async function loadPage(nextPage: number) {
  listController?.abort()
  const request = new AbortController(); listController = request
  loading.value = true; error.value = ''
  try {
    const result = await benchmarks.userRuns(nextPage, filters, request.signal)
    if (request.signal.aborted) return
    items.value = result.items; total.value = result.total; page.value = result.page
  } catch {
    if (!request.signal.aborted) { error.value = t('modelBenchmark.loadError'); items.value = []; total.value = 0 }
  } finally { if (!request.signal.aborted) loading.value = false }
}
function searchAccounts() {
  clearTimeout(accountTimer); accountController?.abort(); selectedAccount.value = ''; accounts.value = []; accountsError.value = ''; moreAccounts.value = false
  const keyword = accountQuery.value.trim()
  accountsLoading.value = !!keyword
  if (!keyword) return
  const request = new AbortController(); accountController = request
  accountTimer = setTimeout(async () => {
    try {
      const result = await benchmarks.userOptions(keyword, request.signal)
      if (!request.signal.aborted) { accounts.value = result.items; moreAccounts.value = result.has_more }
    } catch { if (!request.signal.aborted) accountsError.value = t('modelBenchmark.loadError') }
    finally { if (!request.signal.aborted) accountsLoading.value = false }
  }, 300)
}
function search() {
  if (accountQuery.value.trim() && !selectedAccount.value) return
  filters = { user_id: selectedAccount.value ? Number(selectedAccount.value) : undefined, model: modelQuery.value.trim() || undefined }
  closeDetail(); void loadPage(1)
}
function reset() { accountQuery.value = ''; modelQuery.value = ''; searchAccounts(); search() }
async function openDetail(run: BenchmarkRun) {
  detailController?.abort()
  const request = new AbortController(); detailController = request
  selected.value = run; detailLoading.value = true; detailError.value = ''
  try {
    const result = await benchmarks.detail(run.id, request.signal)
    if (!request.signal.aborted) selected.value = { ...result, owner: run.owner, owner_user_id: run.owner_user_id }
  } catch { if (!request.signal.aborted) detailError.value = t('modelBenchmark.loadError') }
  finally { if (!request.signal.aborted) detailLoading.value = false }
}
function closeDetail() { detailController?.abort(); selected.value = null; detailError.value = ''; detailLoading.value = false }
onMounted(() => { void loadPage(1) })
onBeforeUnmount(() => { clearTimeout(accountTimer); listController?.abort(); accountController?.abort(); detailController?.abort() })
</script>

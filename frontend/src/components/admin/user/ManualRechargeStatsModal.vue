<template>
  <BaseDialog :show="show" :title="t('admin.users.rechargeStats')" width="extra-wide" @close="close">
    <div class="space-y-5">
      <form class="flex flex-wrap items-end gap-3" @submit.prevent="applyFilters">
        <div class="min-w-36 flex-1">
          <label for="stats-from" class="input-label">{{ t('admin.users.startDate') }}</label>
          <input id="stats-from" v-model="filters.from" type="date" class="input" />
        </div>
        <div class="min-w-36 flex-1">
          <label for="stats-to" class="input-label">{{ t('admin.users.endDate') }}</label>
          <input id="stats-to" v-model="filters.to" type="date" :min="filters.from || undefined" class="input" />
        </div>
        <div class="min-w-32 flex-1">
          <label for="stats-platform" class="input-label">{{ t('admin.users.receiptPlatform') }}</label>
          <select id="stats-platform" v-model="filters.platform" class="input">
            <option value="">{{ t('admin.users.allPlatforms') }}</option>
            <option value="wechat">{{ t('admin.users.wechat') }}</option>
            <option value="alipay">{{ t('admin.users.alipay') }}</option>
            <option value="other">{{ t('admin.users.otherPlatform') }}</option>
          </select>
        </div>
        <div class="min-w-32 flex-1">
          <label for="stats-recipient" class="input-label">{{ t('admin.users.receiptRecipient') }}</label>
          <select id="stats-recipient" v-model="filters.recipient" class="input">
            <option value="">{{ t('admin.users.allRecipients') }}</option>
            <option value="鹏">鹏</option><option value="杰">杰</option><option value="翔">翔</option>
          </select>
        </div>
        <div class="min-w-44 flex-[1.5]">
          <label for="stats-user" class="input-label">{{ t('admin.users.userSearch') }}</label>
          <input id="stats-user" v-model.trim="filters.search" class="input" :placeholder="t('admin.users.userSearch')" />
        </div>
        <button type="submit" class="btn btn-primary" :disabled="loading">{{ t('common.search') }}</button>
        <button type="button" class="btn btn-secondary" @click="clearFilters">{{ t('admin.users.clearFilters') }}</button>
      </form>

      <div v-if="loading && !stats" class="py-16 text-center text-gray-500">{{ t('common.loading') }}</div>
      <div v-else-if="loadError && !stats" class="flex items-center justify-center gap-3 py-12 text-sm text-gray-600 dark:text-gray-300">
        <span>{{ loadError }}</span><button type="button" class="btn btn-secondary" @click="load">{{ t('admin.users.retryLoad') }}</button>
      </div>
      <template v-else-if="stats">
        <div class="flex flex-wrap gap-x-8 gap-y-2 border-y border-gray-200 py-3 text-sm dark:border-dark-600">
          <span>{{ t('admin.users.rechargeTotalAmount') }} <strong class="ml-2 text-gray-900 dark:text-white">${{ money(stats.amount) }}</strong></span>
          <span>{{ t('admin.users.rechargeCount') }} <strong class="ml-2 text-gray-900 dark:text-white">{{ stats.total }}</strong></span>
          <span :title="t('admin.users.deductionFilterHint')">{{ t('admin.users.deductionTotal') }} <strong class="ml-2 text-gray-900 dark:text-white">${{ money(stats.deducted) }}</strong></span>
        </div>

        <div class="grid gap-3 sm:grid-cols-3">
          <section v-for="recipient in recipients" :key="recipient" class="rounded-lg border border-gray-200 p-3 dark:border-dark-600">
            <div class="flex items-center justify-between">
              <h4 class="font-medium text-gray-900 dark:text-white">{{ recipient }}</h4>
              <span class="text-sm text-gray-500 dark:text-gray-400">{{ t('admin.users.accountBalance') }}</span>
            </div>
            <div class="mt-1 text-xl font-semibold text-gray-900 dark:text-white">${{ money(recipientBalance(recipient)) }}</div>
            <button type="button" class="btn btn-secondary mt-3 w-full" :disabled="recipientBalance(recipient) <= 0" @click="openDeduction(recipient)">{{ t('admin.users.deduction') }}</button>
          </section>
        </div>

        <form v-if="deducting" class="space-y-3 border-y border-gray-200 py-4 dark:border-dark-600" @submit.prevent="submitDeduction">
          <div class="flex items-center justify-between">
            <h4 class="font-medium text-gray-900 dark:text-white">{{ t('admin.users.deduction') }} · {{ deducting.recipient }}</h4>
            <button type="button" class="text-sm text-gray-600 hover:underline dark:text-gray-300" @click="deducting = null">{{ t('common.cancel') }}</button>
          </div>
          <div class="grid gap-3 sm:grid-cols-3">
            <div><label for="deduction-amount" class="input-label">{{ t('admin.users.deductionAmount') }}</label><div class="relative"><span class="absolute left-3 top-1/2 -translate-y-1/2 text-gray-500">$</span><input id="deduction-amount" v-model.number="deductionAmount" type="number" step="any" min="0" :max="recipientBalance(deducting.recipient)" required class="input pl-8" /></div></div>
            <div class="sm:col-span-2"><label for="deduction-notes" class="input-label">{{ t('admin.users.deductionNotes') }}</label><input id="deduction-notes" v-model="deductionNotes" required maxlength="1000" class="input" /></div>
          </div>
          <div class="flex flex-wrap items-end justify-between gap-3">
            <div><label for="deduction-screenshot" class="input-label">{{ t('admin.users.optionalScreenshot') }}</label><input id="deduction-screenshot" type="file" accept="image/png,image/jpeg,image/webp" class="block max-w-full text-sm" @change="setScreenshot" /></div>
            <button type="submit" class="btn btn-primary" :disabled="submitting">{{ submitting ? t('common.saving') : t('admin.users.deduction') }}</button>
          </div>
        </form>

        <section v-if="stats.breakdown.length" class="space-y-2">
          <h4 class="text-sm font-medium text-gray-900 dark:text-white">{{ t('admin.users.rechargeBreakdown') }}</h4>
          <div class="flex flex-wrap gap-2 text-sm">
            <div v-for="item in stats.breakdown" :key="`${item.platform}-${item.recipient}`" class="flex items-center gap-3 rounded border border-gray-200 px-3 py-2 dark:border-dark-600">
              <span>{{ item.recipient }} · {{ platformLabel(item.platform) }}</span>
              <span class="font-medium tabular-nums">${{ money(item.amount) }}</span>
              <span class="text-gray-500 dark:text-gray-400">{{ item.count }} {{ t('admin.users.rechargeCountUnit') }}</span>
            </div>
          </div>
        </section>

        <div class="flex border-b border-gray-200 dark:border-dark-600">
          <button type="button" class="px-4 py-2 text-sm" :class="tab === 'recharges' ? 'border-b-2 border-primary-600 font-medium text-primary-700 dark:text-primary-300' : 'text-gray-600 dark:text-gray-300'" @click="tab = 'recharges'">{{ t('admin.users.rechargeDetails') }}</button>
          <button type="button" class="px-4 py-2 text-sm" :class="tab === 'deductions' ? 'border-b-2 border-primary-600 font-medium text-primary-700 dark:text-primary-300' : 'text-gray-600 dark:text-gray-300'" @click="tab = 'deductions'">{{ t('admin.users.deductionDetails') }}</button>
        </div>

        <div class="overflow-x-auto">
          <table class="w-full min-w-[650px] text-left text-sm">
            <thead class="text-gray-500 dark:text-gray-400"><tr class="border-b border-gray-200 dark:border-dark-600">
              <th class="py-2 pr-3 font-medium">{{ t('admin.users.createdAt') }}</th>
              <th v-if="tab === 'recharges'" class="py-2 pr-3 font-medium">{{ t('admin.users.userSearch') }}</th>
              <th v-if="tab === 'recharges'" class="py-2 pr-3 font-medium">{{ t('admin.users.receiptPlatform') }}</th>
              <th class="py-2 pr-3 font-medium">{{ t('admin.users.receiptRecipient') }}</th>
              <th class="py-2 pr-3 text-right font-medium">{{ tab === 'recharges' ? t('admin.users.depositAmount') : t('admin.users.deductionAmount') }}</th>
              <th class="py-2 pr-3 font-medium">{{ t('admin.users.notes') }}</th>
              <th v-if="tab === 'deductions'" class="py-2 font-medium">{{ t('admin.users.screenshot') }}</th>
            </tr></thead>
            <tbody v-if="tab === 'recharges'">
              <tr v-for="item in stats.items" :key="item.id" class="border-b border-gray-100 dark:border-dark-700">
                <td class="py-2 pr-3 whitespace-nowrap">{{ dateTime(item.created_at) }}</td><td class="py-2 pr-3">{{ item.user_email }} <span class="text-gray-500">#{{ item.user_id }}</span></td>
                <td class="py-2 pr-3">{{ platformLabel(item.platform) }}</td><td class="py-2 pr-3">{{ item.recipient }}</td>
                <td class="py-2 pr-3 text-right tabular-nums">${{ money(item.amount) }}</td><td class="py-2 pr-3 break-words">{{ item.notes || '-' }}</td>
              </tr>
            </tbody>
            <tbody v-else>
              <tr v-for="item in stats.deductions" :key="item.id" class="border-b border-gray-100 dark:border-dark-700">
                <td class="py-2 pr-3 whitespace-nowrap">{{ dateTime(item.created_at) }}</td><td class="py-2 pr-3">{{ item.recipient }}</td>
                <td class="py-2 pr-3 text-right tabular-nums">-${{ money(item.amount) }}</td><td class="py-2 pr-3 break-words">{{ item.notes }}</td>
                <td class="py-2"><button v-if="item.has_screenshot" type="button" class="text-primary-700 hover:underline dark:text-primary-300" @click="viewScreenshot(item.id)">{{ t('admin.users.screenshot') }}</button></td>
              </tr>
            </tbody>
          </table>
          <p v-if="tab === 'recharges' && !stats.items.length || tab === 'deductions' && !stats.deductions.length" class="py-8 text-center text-sm text-gray-500">{{ t('admin.users.noRecharges') }}</p>
        </div>
        <div class="flex items-center justify-end gap-3 text-sm">
          <button type="button" class="btn btn-secondary px-2 py-1" :disabled="loading || currentPage <= 1" @click="changePage(currentPage - 1)">‹</button>
          <span>{{ currentPage }} / {{ totalPages }}</span>
          <button type="button" class="btn btn-secondary px-2 py-1" :disabled="loading || currentPage >= totalPages" @click="changePage(currentPage + 1)">›</button>
        </div>
        <div v-if="previewURL" class="border-t border-gray-200 pt-4 dark:border-dark-600">
          <div class="mb-2 flex justify-end"><button type="button" class="btn btn-secondary px-2 py-1" @click="clearPreview">{{ t('common.close') }}</button></div>
          <img :src="previewURL" :alt="t('admin.users.screenshot')" class="mx-auto max-h-80 max-w-full object-contain" />
        </div>
      </template>
    </div>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import type { ManualRechargePlatform, ManualRechargeRecipient, ManualRechargeStats } from '@/api/admin/users'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import BaseDialog from '@/components/common/BaseDialog.vue'

const props = defineProps<{ show: boolean }>()
const emit = defineEmits<{ close: [] }>()
const { t } = useI18n()
const appStore = useAppStore()
const recipients: ManualRechargeRecipient[] = ['鹏', '杰', '翔']
const filters = reactive({ from: '', to: '', platform: '', recipient: '', search: '' })
const stats = ref<ManualRechargeStats | null>(null)
const loading = ref(false)
const loadError = ref('')
const submitting = ref(false)
const page = ref(1)
const deductionPage = ref(1)
const tab = ref<'recharges' | 'deductions'>('recharges')
const deducting = ref<{ recipient: ManualRechargeRecipient } | null>(null)
const deductionAmount = ref<number | null>(null)
const deductionNotes = ref('')
const screenshot = ref<File | null>(null)
const deductionKey = ref('')
let lastDeductionAttempt: { recipient: ManualRechargeRecipient; amount: number; notes: string; screenshot: File | null } | null = null
const previewURL = ref<string | null>(null)
let requestSequence = 0

const money = (value: number) => new Intl.NumberFormat(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 8 }).format(value || 0)
const dateTime = (value: string) => new Date(value).toLocaleString()
const platformLabel = (value: ManualRechargePlatform) => t(`admin.users.${value === 'wechat' ? 'wechat' : value === 'alipay' ? 'alipay' : 'otherPlatform'}`)
const recipientBalance = (recipient: ManualRechargeRecipient) => stats.value?.accounts.find(item => item.recipient === recipient)?.balance || 0
const currentPage = computed(() => tab.value === 'recharges' ? page.value : deductionPage.value)
const totalPages = computed(() => Math.max(1, Math.ceil(((tab.value === 'recharges' ? stats.value?.total : stats.value?.deduction_total) ?? 0) / 20)))
const dateStart = (date: string) => date ? new Date(`${date}T00:00:00`).toISOString() : undefined
const dateEnd = (date: string) => { if (!date) return undefined; const day = new Date(`${date}T00:00:00`); day.setDate(day.getDate() + 1); return day.toISOString() }

async function load() {
  const sequence = ++requestSequence
  loading.value = true
  loadError.value = ''
  try {
    const data = await adminAPI.users.getManualRechargeStats({ from: dateStart(filters.from), to: dateEnd(filters.to), platform: filters.platform, recipient: filters.recipient, search: filters.search, page: page.value, deduction_page: deductionPage.value, page_size: 20 })
    if (sequence === requestSequence) stats.value = data
  } catch (error) {
    if (sequence === requestSequence) { loadError.value = extractApiErrorMessage(error, t('admin.users.loadRechargeStatsFailed')); appStore.showError(loadError.value) }
  } finally { if (sequence === requestSequence) loading.value = false }
}
watch(() => props.show, show => { if (show) load(); else { requestSequence++; clearPreview(); deducting.value = null } })
function applyFilters() { page.value = 1; deductionPage.value = 1; load() }
function clearFilters() { Object.assign(filters, { from: '', to: '', platform: '', recipient: '', search: '' }); applyFilters() }
function changePage(next: number) { if (tab.value === 'recharges') page.value = next; else deductionPage.value = next; load() }
function openDeduction(recipient: ManualRechargeRecipient) { deducting.value = { recipient }; deductionAmount.value = null; deductionNotes.value = ''; screenshot.value = null; deductionKey.value = ''; lastDeductionAttempt = null }
function setScreenshot(event: Event) { screenshot.value = (event.target as HTMLInputElement).files?.[0] || null }
async function submitDeduction() {
  if (!deducting.value || !deductionAmount.value || deductionAmount.value <= 0 || !deductionNotes.value.trim()) { appStore.showError(t('admin.users.deductionNotes')); return }
  if (deductionAmount.value > recipientBalance(deducting.value.recipient)) { appStore.showError(t('admin.users.insufficientBalance')); return }
  if (screenshot.value && (screenshot.value.size > 5 * 1024 * 1024 || !['image/png', 'image/jpeg', 'image/webp'].includes(screenshot.value.type))) { appStore.showError(t('admin.users.invalidScreenshot')); return }
  submitting.value = true
  try {
    const attempt = { recipient: deducting.value.recipient, amount: deductionAmount.value, notes: deductionNotes.value.trim(), screenshot: screenshot.value }
    if (!lastDeductionAttempt || attempt.recipient !== lastDeductionAttempt.recipient || attempt.amount !== lastDeductionAttempt.amount || attempt.notes !== lastDeductionAttempt.notes || attempt.screenshot !== lastDeductionAttempt.screenshot) {
      deductionKey.value = crypto.randomUUID()
      lastDeductionAttempt = attempt
    }
    await adminAPI.users.deductManualRecharge({ ...deducting.value, amount: deductionAmount.value, notes: deductionNotes.value.trim(), screenshot: screenshot.value, idempotencyKey: deductionKey.value })
    appStore.showSuccess(t('common.success'))
    deducting.value = null
    await load()
  } catch (error) { appStore.showError(extractApiErrorMessage(error, t('common.error'))) }
  finally { submitting.value = false }
}
async function viewScreenshot(id: number) {
  try { const blob = await adminAPI.users.getManualRechargeScreenshot(id); clearPreview(); previewURL.value = URL.createObjectURL(blob) }
  catch (error) { appStore.showError(extractApiErrorMessage(error, t('common.error'))) }
}
function clearPreview() { if (previewURL.value) URL.revokeObjectURL(previewURL.value); previewURL.value = null }
function close() { emit('close') }
onUnmounted(clearPreview)
</script>

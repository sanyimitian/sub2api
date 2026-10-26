<template>
  <BaseDialog :show="true" :title="t('admin.degradationTests.addPlan')" width="wide" :show-close-button="!saving" :close-on-escape="!saving" @close="close">
    <div class="space-y-4">
      <div>
        <label for="create-degradation-account" class="mb-1 block text-sm font-medium">{{ t('admin.degradationTests.account') }}</label>
        <Select
          id="create-degradation-account"
          :model-value="selectedAccount?.id ?? null"
          :options="accountOptions"
          :placeholder="t('admin.intelligenceStatus.selectAccount')"
          :search-placeholder="t('admin.intelligenceStatus.accountSearchPlaceholder')"
          :empty-text="t('admin.intelligenceStatus.noAccounts')"
          :loading="loadingAccounts"
          :disabled="saving"
          aria-describedby="create-degradation-account-help"
          remote
          searchable
          @update:model-value="selectAccount"
          @search="loadAccounts"
        />
        <p id="create-degradation-account-help" class="mt-1 text-xs text-gray-500">{{ t('admin.intelligenceStatus.accountSearchHelp') }}</p>
        <div v-if="accountsError" role="alert" class="mt-2 text-sm text-red-600">
          {{ accountsError }}
          <button type="button" class="ml-2 underline" :disabled="loadingAccounts || saving" @click="loadAccounts(accountSearch)">{{ t('common.tryAgain') }}</button>
        </div>
      </div>
      <p v-if="loadingModels" role="status" class="text-xs text-gray-500">{{ t('common.loading') }}</p>
      <p v-if="modelsError" role="status" class="text-xs text-amber-600 dark:text-amber-400">{{ modelsError }}</p>
      <p v-if="saveError" role="alert" class="whitespace-pre-wrap break-words text-sm text-red-600">{{ saveError }}</p>
      <fieldset v-if="selectedAccount" :disabled="saving" class="min-w-0">
        <DegradationTestPlanForm :key="selectedAccount.id" :model-options="modelOptions" :busy="saving" @submit="createPlan" @cancel="close" />
      </fieldset>
      <div v-else class="flex justify-end">
        <button type="button" class="btn btn-secondary" @click="close">{{ t('common.cancel') }}</button>
      </div>
    </div>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select, { type SelectOption } from '@/components/common/Select.vue'
import { useAppStore } from '@/stores/app'
import type { AccountListItem, DegradationTestPlan } from '@/types'
import { extractApiErrorMessage } from '@/utils/apiError'
import DegradationTestPlanForm, { type DegradationTestPlanFormValue } from './DegradationTestPlanForm.vue'

type AccountChoice = Pick<AccountListItem, 'id' | 'name' | 'platform'>
const emit = defineEmits<{ close: []; created: [plan: DegradationTestPlan] }>()
const { t } = useI18n()
const appStore = useAppStore()
const accounts = ref<AccountChoice[]>([])
const selectedAccount = ref<AccountChoice | null>(null)
const accountSearch = ref('')
const loadingAccounts = ref(false)
const accountsError = ref('')
const loadingModels = ref(false)
const modelsError = ref('')
const modelOptions = ref<SelectOption[]>([])
const saving = ref(false)
const saveError = ref('')
let active = true
let accountsController: AbortController | null = null
let accountsRequestId = 0
let modelsRequestId = 0

const accountOptions = computed<SelectOption[]>(() => {
  const choices = selectedAccount.value
    ? [selectedAccount.value, ...accounts.value.filter(account => account.id !== selectedAccount.value?.id)]
    : accounts.value
  return choices.map(account => ({ value: account.id, label: `${account.name} · ${account.platform} · #${account.id}` }))
})

async function loadAccounts(search = '') {
  if (!active) return
  accountSearch.value = search
  accountsController?.abort()
  const controller = new AbortController()
  accountsController = controller
  const request = ++accountsRequestId
  loadingAccounts.value = true
  accountsError.value = ''
  try {
    const data = await adminAPI.accounts.list(1, 50, { search: search.trim(), lite: 'true' }, { signal: controller.signal })
    if (!active || controller.signal.aborted || request !== accountsRequestId) return
    accounts.value = data.items
  } catch (error) {
    if (!active || controller.signal.aborted || request !== accountsRequestId) return
    accountsError.value = extractApiErrorMessage(error, t('admin.intelligenceStatus.accountsLoadFailed'))
  } finally {
    if (active && request === accountsRequestId) {
      loadingAccounts.value = false
      accountsController = null
    }
  }
}

async function selectAccount(value: SelectOption['value']) {
  if (!active || saving.value || value === selectedAccount.value?.id) return
  selectedAccount.value = accounts.value.find(account => account.id === value) ?? null
  modelOptions.value = []
  modelsError.value = ''
  saveError.value = ''
  const request = ++modelsRequestId
  const accountId = selectedAccount.value?.id
  loadingModels.value = !!accountId
  if (!accountId) return
  try {
    const models = await adminAPI.accounts.getAvailableModels(accountId)
    if (!active || request !== modelsRequestId) return
    modelOptions.value = models.map(model => ({ value: model.id, label: model.display_name || model.id }))
  } catch {
    if (active && request === modelsRequestId) modelsError.value = t('admin.intelligenceStatus.modelsLoadFailed')
  } finally {
    if (active && request === modelsRequestId) loadingModels.value = false
  }
}

async function createPlan(value: DegradationTestPlanFormValue) {
  if (!active || !selectedAccount.value || saving.value) return
  saving.value = true
  saveError.value = ''
  try {
    const plan = await adminAPI.degradationTests.create({ ...value, account_id: selectedAccount.value.id })
    if (!active) return
    appStore.showSuccess(t('admin.degradationTests.createSuccess'))
    emit('created', plan)
  } catch (error) {
    if (!active) return
    saveError.value = extractApiErrorMessage(error, t('admin.degradationTests.requestFailed'))
    appStore.showError(saveError.value)
  } finally {
    if (active) saving.value = false
  }
}

function close() {
  if (!saving.value) emit('close')
}

onMounted(() => { void loadAccounts() })
onBeforeUnmount(() => {
  active = false
  accountsController?.abort()
  accountsRequestId++
  modelsRequestId++
})
</script>

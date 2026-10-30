<template>
  <BaseDialog :show="show" :title="operation === 'add' ? t('admin.users.deposit') : t('admin.users.withdraw')" width="narrow" @close="$emit('close')">
    <form v-if="user" id="balance-form" @submit.prevent="handleBalanceSubmit" class="space-y-5">
      <div class="flex items-center gap-3 rounded-xl bg-gray-50 p-4 dark:bg-dark-700">
        <div class="flex h-10 w-10 items-center justify-center rounded-full bg-primary-100"><span class="text-lg font-medium text-primary-700">{{ user.email.charAt(0).toUpperCase() }}</span></div>
        <div class="flex-1"><p class="font-medium text-gray-900 dark:text-gray-100">{{ user.email }}</p><p class="text-sm text-gray-500 dark:text-gray-400">{{ t('admin.users.currentBalance') }}: ${{ formatBalance(user.balance) }}</p></div>
      </div>
      <div>
        <label class="input-label">{{ operation === 'add' ? t('admin.users.depositAmount') : t('admin.users.withdrawAmount') }}</label>
        <div class="relative flex gap-2">
          <div class="relative flex-1"><div class="absolute left-3 top-1/2 -translate-y-1/2 font-medium text-gray-500">$</div><input v-model.number="form.amount" type="number" step="any" min="0" required class="input pl-8" /></div>
          <button v-if="operation === 'subtract'" type="button" @click="fillAllBalance" class="btn btn-secondary whitespace-nowrap">{{ t('admin.users.withdrawAll') }}</button>
        </div>
      </div>
      <div v-if="operation === 'add'" class="grid grid-cols-1 gap-4 sm:grid-cols-2">
        <div>
          <label for="recharge-platform" class="input-label">{{ t('admin.users.receiptPlatform') }}</label>
          <select id="recharge-platform" v-model="form.platform" required class="input">
            <option value="" disabled>{{ t('admin.users.choosePlatform') }}</option>
            <option value="wechat">{{ t('admin.users.wechat') }}</option>
            <option value="alipay">{{ t('admin.users.alipay') }}</option>
            <option value="other">{{ t('admin.users.otherPlatform') }}</option>
          </select>
        </div>
        <div>
          <label for="recharge-recipient" class="input-label">{{ t('admin.users.receiptRecipient') }}</label>
          <select id="recharge-recipient" v-model="form.recipient" required class="input">
            <option value="" disabled>{{ t('admin.users.chooseRecipient') }}</option>
            <option value="鹏">鹏</option><option value="杰">杰</option><option value="翔">翔</option>
          </select>
        </div>
      </div>
      <div><label for="balance-notes" class="input-label">{{ t('admin.users.notes') }}{{ operation === 'add' && form.platform === 'other' ? ' *' : '' }}</label><textarea id="balance-notes" v-model="form.notes" rows="3" class="input" :required="operation === 'add' && form.platform === 'other'"></textarea></div>
      <div v-if="form.amount > 0" class="rounded-xl border border-blue-200 bg-blue-50 p-4 dark:border-blue-800 dark:bg-blue-950"><div class="flex items-center justify-between text-sm"><span class="text-gray-700 dark:text-gray-300">{{ t('admin.users.newBalance') }}:</span><span class="font-bold text-gray-900 dark:text-gray-100">${{ formatBalance(calculateNewBalance()) }}</span></div></div>
    </form>
    <template #footer>
      <div class="flex justify-end gap-3">
        <button @click="$emit('close')" class="btn btn-secondary">{{ t('common.cancel') }}</button>
        <button type="submit" form="balance-form" :disabled="submitting || !form.amount" class="btn" :class="operation === 'add' ? 'bg-emerald-600 text-white' : 'btn-danger'">{{ submitting ? t('common.saving') : t('common.confirm') }}</button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminAPI } from '@/api/admin'
import { extractApiErrorMessage } from '@/utils/apiError'
import type { AdminUser } from '@/types'
import type { ManualRechargePlatform, ManualRechargeRecipient } from '@/api/admin/users'
import BaseDialog from '@/components/common/BaseDialog.vue'

const props = defineProps<{ show: boolean, user: AdminUser | null, operation: 'add' | 'subtract' }>()
const emit = defineEmits(['close', 'success']); const { t } = useI18n(); const appStore = useAppStore()

const submitting = ref(false); const form = reactive({ amount: 0, notes: '', platform: '' as ManualRechargePlatform | '', recipient: '' as ManualRechargeRecipient | '' })
let lastAttempt = ''
let operationKey = ''
watch(() => props.show, (v) => { if(v) { form.amount = 0; form.notes = ''; form.platform = ''; form.recipient = ''; lastAttempt = ''; operationKey = '' } })

// 格式化余额：显示完整精度，去除尾部多余的0
const formatBalance = (value: number) => {
  if (value === 0) return '0.00'
  // 最多保留8位小数，去除尾部的0
  const formatted = value.toFixed(8).replace(/\.?0+$/, '')
  // 确保至少有2位小数
  const parts = formatted.split('.')
  if (parts.length === 1) return formatted + '.00'
  if (parts[1].length === 1) return formatted + '0'
  return formatted
}

// 填入全部余额
const fillAllBalance = () => {
  if (props.user) {
    form.amount = props.user.balance
  }
}

const calculateNewBalance = () => {
  if (!props.user) return 0
  const result = props.operation === 'add' ? props.user.balance + form.amount : props.user.balance - form.amount
  // 避免浮点数精度问题导致的 -0.00 显示
  return Math.abs(result) < 1e-10 ? 0 : result
}
const handleBalanceSubmit = async () => {
  if (!props.user) return
  if (!form.amount || form.amount <= 0) {
    appStore.showError(t('admin.users.amountRequired'))
    return
  }
  if (props.operation === 'add' && (!form.platform || !form.recipient)) {
    appStore.showError(t('admin.users.receiptRequired'))
    return
  }
  if (props.operation === 'add' && form.platform === 'other' && !form.notes.trim()) {
    appStore.showError(t('admin.users.otherNotesRequired'))
    return
  }
  // 退款时验证金额不超过实际余额
  if (props.operation === 'subtract' && form.amount > props.user.balance) {
    appStore.showError(t('admin.users.insufficientBalance'))
    return
  }
  submitting.value = true
  try {
    const attempt = JSON.stringify([props.user.id, props.operation, form.amount, form.notes.trim(), form.platform, form.recipient])
    if (attempt !== lastAttempt) { operationKey = crypto.randomUUID(); lastAttempt = attempt }
    await adminAPI.users.updateBalance(props.user.id, form.amount, props.operation, form.notes.trim(),
      props.operation === 'add' ? { platform: form.platform as ManualRechargePlatform, recipient: form.recipient as ManualRechargeRecipient } : undefined,
      operationKey)
    appStore.showSuccess(t('common.success')); emit('success'); emit('close')
  } catch (e: any) {
    console.error('Failed to update balance:', e)
    appStore.showError(extractApiErrorMessage(e, t('common.error')))
  } finally { submitting.value = false }
}
</script>

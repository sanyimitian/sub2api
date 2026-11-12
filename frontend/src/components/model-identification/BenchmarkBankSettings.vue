<template>
  <section class="rounded-xl border border-gray-200 bg-white p-5 dark:border-dark-700 dark:bg-dark-900" :aria-label="t('modelBenchmark.bankManagement')">
    <div class="flex flex-wrap items-start justify-between gap-4">
      <div class="space-y-2">
        <h2 class="text-lg font-semibold">{{ t('modelBenchmark.bankManagement') }}</h2>
        <p class="text-sm text-gray-500">{{ t('modelBenchmark.bankUpdateHint') }}</p>
        <div class="flex flex-wrap gap-x-4 gap-y-1 text-sm">
          <span v-for="category in categories" :key="category">{{ t(`modelBenchmark.categories.${category}`) }} <strong>{{ state?.capacities[category] ?? 0 }}</strong></span>
          <span v-if="state?.coding_capacities" class="text-gray-500">{{ t('modelBenchmark.difficulties.medium') }} {{ state.coding_capacities.medium }} / {{ t('modelBenchmark.difficulties.hard') }} {{ state.coding_capacities.hard }}</span>
        </div>
        <p v-if="state?.bank_update?.checked_at" class="text-xs text-gray-500">{{ t('modelBenchmark.bankLastCheck', { time: new Date(state.bank_update.checked_at).toLocaleString() }) }}</p>
      </div>
      <button type="button" class="btn btn-secondary shrink-0" :disabled="busy || !state?.ready || state?.resources?.blocked || checking" data-testid="bank-update" @click="emit('update')">{{ t(checking ? 'modelBenchmark.bankChecking' : 'modelBenchmark.updateBank') }}</button>
    </div>
    <p v-if="state?.bank_update?.message" class="mt-4 rounded-lg px-3 py-2 text-sm" :class="state.bank_update.status === 'failed' ? 'bg-red-50 text-red-700 dark:bg-red-950/30 dark:text-red-300' : 'bg-gray-50 text-gray-600 dark:bg-dark-800 dark:text-gray-300'" role="status">{{ state.bank_update.message }}</p>
    <div v-if="candidate && state?.bank_update?.status === 'ready'" class="mt-4 flex flex-wrap items-center justify-between gap-4 rounded-lg border border-primary-200 bg-primary-50 p-4 dark:border-primary-900 dark:bg-primary-950/20">
      <div class="space-y-2 text-sm">
        <p class="font-medium">{{ t('modelBenchmark.bankChanges', candidate) }}</p>
        <p v-if="candidate.skipped" class="text-xs text-gray-500">{{ t('modelBenchmark.bankSkipped', { count: candidate.skipped }) }}</p>
        <p class="flex flex-wrap gap-x-4 gap-y-1 text-gray-600 dark:text-gray-300"><span v-for="category in categories" :key="category">{{ t(`modelBenchmark.categories.${category}`) }} {{ state?.capacities[category] ?? 0 }} → <strong>{{ candidate.counts[category] }}</strong></span></p>
      </div>
      <button type="button" class="btn btn-primary" :disabled="busy || !state?.ready" data-testid="bank-activate" @click="confirmVersion = candidate.version">{{ t('modelBenchmark.activateBank') }}</button>
    </div>
    <ConfirmDialog :show="!!confirmVersion" :title="t('modelBenchmark.activateBank')" :message="t('modelBenchmark.activateBankHint')" @cancel="confirmVersion = ''" @confirm="activate" />
  </section>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import type { BenchmarkState, Category } from '@/api/admin/model-benchmarks'
const props = defineProps<{ state: BenchmarkState | null; busy: boolean }>()
const emit = defineEmits<{ update: []; activate: [version: string] }>()
const { t } = useI18n()
const categories: Category[] = ['coding', 'math', 'reasoning']
const checking = computed(() => props.state?.bank_update?.status === 'checking')
const candidate = computed(() => props.state?.bank_update?.candidate)
const confirmVersion = ref('')
function activate() { const version = confirmVersion.value; confirmVersion.value = ''; if (version) emit('activate', version) }
</script>

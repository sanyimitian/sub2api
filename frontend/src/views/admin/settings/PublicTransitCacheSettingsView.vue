<template>
  <AppLayout>
    <section class="mx-auto max-w-4xl space-y-6">
      <header class="flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 class="text-2xl font-semibold text-gray-900 dark:text-white">
            {{ t('publicTransit.cacheConfig.title') }}
          </h1>
          <p class="mt-1 max-w-2xl text-sm text-gray-600 dark:text-gray-300">
            {{ t('publicTransit.cacheConfig.description') }}
          </p>
        </div>
        <button
          type="button"
          class="inline-flex h-10 items-center gap-2 rounded-md bg-primary-600 px-4 text-sm font-medium text-white hover:bg-primary-700 focus:outline-none focus:ring-2 focus:ring-primary-500 focus:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-50"
          :disabled="loading || saving || !valid"
          :title="t('publicTransit.cacheConfig.save')"
          @click="save"
        >
          {{ saving ? t('common.saving') : t('common.save') }}
        </button>
      </header>

      <div v-if="loading" class="space-y-4" aria-busy="true">
        <div class="h-16 animate-pulse rounded-md bg-gray-200 dark:bg-gray-800" />
        <div class="h-40 animate-pulse rounded-md bg-gray-200 dark:bg-gray-800" />
      </div>

      <template v-else>
        <section class="border-y border-gray-200 py-5 dark:border-gray-700">
          <label class="flex cursor-pointer items-start gap-3">
            <input
              v-model="policy.enabled"
              type="checkbox"
              class="mt-1 h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500"
            />
            <span>
              <span class="block text-sm font-medium text-gray-900 dark:text-white">
                {{ t('publicTransit.cacheConfig.enabled') }}
              </span>
              <span class="mt-1 block text-sm text-gray-600 dark:text-gray-300">
                {{ t('publicTransit.cacheConfig.enabledHint') }}
              </span>
            </span>
          </label>
        </section>

        <section class="space-y-5">
          <div>
            <h2 class="text-base font-semibold text-gray-900 dark:text-white">
              {{ t('publicTransit.cacheConfig.normalRange') }}
            </h2>
            <p class="mt-1 text-sm text-gray-600 dark:text-gray-300">
              {{ t('publicTransit.cacheConfig.normalHint') }}
            </p>
          </div>
          <div class="grid gap-5 sm:grid-cols-2">
            <label class="block text-sm font-medium text-gray-700 dark:text-gray-200">
              {{ t('publicTransit.cacheConfig.minimumRate') }}
              <span class="mt-2 flex items-center gap-2">
                <input v-model.number="policy.minimum_rate" type="number" min="0" max="100" step="0.1" class="cache-number" />
                <span class="text-gray-500">%</span>
              </span>
            </label>
            <label class="block text-sm font-medium text-gray-700 dark:text-gray-200">
              {{ t('publicTransit.cacheConfig.maximumRate') }}
              <span class="mt-2 flex items-center gap-2">
                <input v-model.number="policy.maximum_rate" type="number" min="0" max="100" step="0.1" class="cache-number" />
                <span class="text-gray-500">%</span>
              </span>
            </label>
          </div>
        </section>

        <section class="space-y-5 border-t border-gray-200 pt-5 dark:border-gray-700">
          <div>
            <h2 class="text-base font-semibold text-gray-900 dark:text-white">
              {{ t('publicTransit.cacheConfig.lowRange') }}
            </h2>
            <p class="mt-1 text-sm text-gray-600 dark:text-gray-300">
              {{ t('publicTransit.cacheConfig.lowHint') }}
            </p>
          </div>
          <div class="grid gap-5 sm:grid-cols-2">
            <label class="block text-sm font-medium text-gray-700 dark:text-gray-200">
              {{ t('publicTransit.cacheConfig.lowMinimum') }}
              <span class="mt-2 flex items-center gap-2">
                <input v-model.number="policy.low_rate_min" type="number" min="0" max="100" step="0.1" class="cache-number" />
                <span class="text-gray-500">%</span>
              </span>
            </label>
            <label class="block text-sm font-medium text-gray-700 dark:text-gray-200">
              {{ t('publicTransit.cacheConfig.lowMaximum') }}
              <span class="mt-2 flex items-center gap-2">
                <input v-model.number="policy.low_rate_max" type="number" min="0" max="100" step="0.1" class="cache-number" />
                <span class="text-gray-500">%</span>
              </span>
            </label>
          </div>
        </section>

        <p v-if="!valid" class="text-sm text-red-600 dark:text-red-400" role="alert">
          {{ t('publicTransit.cacheConfig.validation') }}
        </p>
      </template>
    </section>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import { getPublicTransitCachePolicy, updatePublicTransitCachePolicy } from '@/api/admin/settings'
import type { PublicTransitCachePolicy } from '@/api/admin/settings'
import { useAppStore } from '@/stores/app'

const { t } = useI18n()
const appStore = useAppStore()
const loading = ref(true)
const saving = ref(false)
const policy = ref<PublicTransitCachePolicy>({
  enabled: true,
  minimum_rate: 80,
  maximum_rate: 92,
  low_rate_min: 75,
  low_rate_max: 80,
})

const valid = computed(() => {
  const values = [policy.value.low_rate_min, policy.value.low_rate_max, policy.value.minimum_rate, policy.value.maximum_rate]
  return values.every(value => Number.isFinite(value) && value >= 0 && value <= 100)
    && policy.value.low_rate_min < policy.value.low_rate_max
    && policy.value.low_rate_max <= policy.value.minimum_rate
    && policy.value.minimum_rate <= policy.value.maximum_rate
})

onMounted(async () => {
  try {
    policy.value = await getPublicTransitCachePolicy()
  } catch (error: any) {
    appStore.showError(error?.response?.data?.detail || t('publicTransit.cacheConfig.loadFailed'))
  } finally {
    loading.value = false
  }
})

async function save() {
  if (!valid.value || saving.value) return
  saving.value = true
  try {
    policy.value = await updatePublicTransitCachePolicy(policy.value)
    appStore.showSuccess(t('publicTransit.cacheConfig.saved'))
  } catch (error: any) {
    appStore.showError(error?.response?.data?.detail || t('publicTransit.cacheConfig.saveFailed'))
  } finally {
    saving.value = false
  }
}
</script>

<style scoped>
.cache-number {
  @apply h-10 w-full rounded-md border border-gray-300 bg-white px-3 text-sm text-gray-900 outline-none focus:border-primary-500 focus:ring-2 focus:ring-primary-500/20 dark:border-gray-600 dark:bg-dark-900 dark:text-white;
}
</style>

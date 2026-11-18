<template>
  <div class="flex flex-wrap items-center justify-between gap-3">
    <p class="max-w-xl text-sm text-gray-500 dark:text-gray-400">
      {{ t(`${keyPrefix}.syncHint`) }}
    </p>
    <button type="button" class="btn btn-primary shrink-0" :disabled="syncing" :aria-busy="syncing" @click="sync">
      {{ t(`${keyPrefix}.${syncing ? 'syncing' : 'syncModels'}`) }}
    </button>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { syncModelPlaza, syncAvailableChannelModels } from '@/api/modelPlaza'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'

const { t } = useI18n()
const appStore = useAppStore()
const syncing = ref(false)
const props = withDefaults(defineProps<{ target?: 'plaza' | 'channels' }>(), { target: 'plaza' })
const keyPrefix = computed(() => `admin.settings.features.${props.target === 'channels' ? 'availableChannels' : 'modelPlaza'}`)

async function sync() {
  if (syncing.value) return
  syncing.value = true
  try {
    const result = await (props.target === 'channels' ? syncAvailableChannelModels() : syncModelPlaza())
    appStore.showSuccess(t(`${keyPrefix.value}.syncSuccess`, {
      groups: result.group_count, models: result.model_count
    }))
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t(`${keyPrefix.value}.syncFailed`)))
  } finally {
    syncing.value = false
  }
}
</script>

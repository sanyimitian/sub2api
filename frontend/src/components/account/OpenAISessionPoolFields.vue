<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import Toggle from '@/components/common/Toggle.vue'

interface PreviewCredentials {
  platform: string
  type: string
  base_url?: string
  api_key: string
  model_mapping?: Record<string, string>
}

const enabled = defineModel<boolean>('enabled', { default: false })
const models = defineModel<string[]>('models', { default: () => ['gpt-6-sol'] })
const props = withDefaults(defineProps<{
  fallbackModels?: string[]
  accountId?: number
  previewCredentials?: PreviewCredentials
  preserveUnavailable?: boolean
}>(), { fallbackModels: () => ['gpt-6-sol'], preserveUnavailable: true })

const id = `session-pool-models-${Math.random().toString(36).slice(2, 9)}`
const { t } = useI18n()
const isOpen = ref(false)
const search = ref('')
const loading = ref(false)
const loadedModels = ref<string[]>([])
const modelsLoaded = ref(false)
const loadFailed = ref(false)
let loadRequest = 0

const availableModels = computed(() => {
  const available = modelsLoaded.value ? loadedModels.value : props.fallbackModels
  return [...new Set([...available, ...models.value])]
})
const filteredModels = computed(() => {
  const query = search.value.trim().toLowerCase()
  if (!query) return availableModels.value
  return availableModels.value.filter(model => model.toLowerCase().includes(query))
})
const selectedLabel = computed(() => models.value.length
  ? models.value.join(', ')
  : t('admin.accounts.sessionPool.noModelsSelected'))

async function loadModels() {
  if (!props.accountId && !props.previewCredentials?.api_key.trim()) {
    loadedModels.value = []
    modelsLoaded.value = false
    return
  }
  const request = ++loadRequest
  loading.value = true
  modelsLoaded.value = false
  loadFailed.value = false
  try {
    let fetched: string[]
    if (props.accountId) {
      const result = await adminAPI.accounts.getAvailableModels(props.accountId)
      fetched = result.map(model => model.id)
    } else if (props.previewCredentials?.api_key.trim()) {
      const result = await adminAPI.accounts.syncUpstreamModelsPreview(props.previewCredentials)
      fetched = result.models
    } else {
      fetched = []
    }
    if (request === loadRequest) {
      loadedModels.value = [...new Set(fetched.map(model => model.trim()).filter(Boolean))]
      modelsLoaded.value = true
      if (!props.preserveUnavailable) {
        const selected = models.value.filter(model => loadedModels.value.includes(model))
        models.value = selected.length > 0
          ? selected
          : [loadedModels.value.includes('gpt-6-sol') ? 'gpt-6-sol' : loadedModels.value[0]].filter((model): model is string => Boolean(model))
      }
    }
  } catch {
    if (request === loadRequest) {
      loadFailed.value = true
      loadedModels.value = []
    }
  } finally {
    if (request === loadRequest) loading.value = false
  }
}

function toggleModel(model: string) {
  if (models.value.includes(model)) {
    if (models.value.length <= 1) return
    models.value = models.value.filter(value => value !== model)
    return
  }
  if (models.value.length >= 20) return
  models.value = [...models.value, model]
}

function onOutsideClick(event: MouseEvent) {
  const target = event.target
  if (target instanceof Element && !target.closest(`[data-session-pool-picker="${id}"]`)) isOpen.value = false
}

watch([
  () => enabled.value,
  () => props.accountId,
  () => props.previewCredentials?.api_key,
  () => props.previewCredentials?.base_url
], ([isEnabled]) => {
  if (!isEnabled) return
  void loadModels()
}, { immediate: true })

onMounted(() => document.addEventListener('click', onOutsideClick))
onUnmounted(() => {
  loadRequest++
  document.removeEventListener('click', onOutsideClick)
})
</script>

<template>
  <div class="space-y-3 rounded-lg border border-gray-200 p-4 dark:border-dark-600" data-testid="openai-session-pool-fields">
    <div class="flex items-center justify-between gap-4">
      <div>
        <label :id="id + '-label'" class="input-label mb-1">{{ t('admin.accounts.sessionPool.title') }}</label>
        <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.accounts.sessionPool.hint') }}</p>
      </div>
      <Toggle v-model="enabled" :aria-labelledby="id + '-label'" data-testid="openai-session-pool-enabled" />
    </div>
    <div v-if="enabled" class="space-y-1.5">
      <label :for="id" class="input-label">{{ t('admin.accounts.sessionPool.models') }}</label>
      <div class="relative" :data-session-pool-picker="id">
        <div class="flex items-stretch gap-1">
        <button
          :id="id"
          type="button"
          class="input flex min-h-10 min-w-0 flex-1 items-center justify-between gap-3 text-left"
          :aria-expanded="isOpen"
          aria-haspopup="listbox"
          data-testid="openai-session-pool-models"
          @click="isOpen = !isOpen"
        >
          <span class="min-w-0 flex-1 truncate" :class="models.length ? 'font-mono' : 'text-gray-400'">{{ selectedLabel }}</span>
          <span class="shrink-0 text-xs text-gray-500">{{ loading ? t('common.loading') : models.length + '/20' }}</span>
        </button>
          <button
            type="button"
            class="shrink-0 rounded-md border border-gray-300 px-2 text-gray-500 hover:bg-gray-100 hover:text-primary-600 dark:border-dark-500 dark:hover:bg-dark-600"
            :aria-label="t('admin.accounts.sessionPool.refreshModels')"
            :title="t('admin.accounts.sessionPool.refreshModels')"
            @click.stop="loadModels"
          >
            <svg class="h-4 w-4" :class="loading && 'animate-spin'" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
              <path stroke-linecap="round" stroke-linejoin="round" d="M20 7v5h-5M4 17v-5h5" />
              <path stroke-linecap="round" stroke-linejoin="round" d="M5.6 9A7 7 0 0 1 18 6l2 2M4 16l2 2a7 7 0 0 0 12.4-3" />
            </svg>
          </button>
        </div>
        <div v-if="isOpen" class="absolute left-0 right-0 z-50 mt-1 overflow-hidden rounded-md border border-gray-200 bg-white shadow-lg dark:border-dark-600 dark:bg-dark-700">
          <div class="border-b border-gray-200 p-2 dark:border-dark-600">
            <input v-model="search" type="search" class="input w-full text-sm" :placeholder="t('admin.accounts.sessionPool.searchModels')" />
          </div>
          <div class="max-h-56 overflow-y-auto py-1" role="listbox" aria-multiselectable="true">
            <label
              v-for="model in filteredModels"
              :key="model"
              class="flex cursor-pointer items-center gap-2 px-3 py-2 text-sm hover:bg-gray-50 dark:hover:bg-dark-600"
              :class="!models.includes(model) && models.length >= 20 && 'cursor-not-allowed opacity-50'"
            >
              <input
                type="checkbox"
                class="h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500"
                :checked="models.includes(model)"
                :disabled="!models.includes(model) && models.length >= 20"
                @change="toggleModel(model)"
              />
              <span class="min-w-0 flex-1 truncate font-mono">{{ model }}</span>
              <span v-if="modelsLoaded && !loadedModels.includes(model) && models.includes(model)" class="text-xs text-amber-600">{{ t('admin.accounts.sessionPool.unavailable') }}</span>
            </label>
            <p v-if="filteredModels.length === 0" class="px-3 py-3 text-center text-sm text-gray-500">{{ t('admin.accounts.sessionPool.noModels') }}</p>
          </div>
          <p v-if="loading" class="border-t border-gray-200 px-3 py-2 text-xs text-gray-500 dark:border-dark-600">{{ t('admin.accounts.sessionPool.loadingModels') }}</p>
          <p v-else-if="loadFailed" class="border-t border-gray-200 px-3 py-2 text-xs text-amber-700 dark:border-dark-600 dark:text-amber-400">{{ t('admin.accounts.sessionPool.loadFailed') }}</p>
        </div>
      </div>
      <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.accounts.sessionPool.modelsHint') }}</p>
    </div>
  </div>
</template>

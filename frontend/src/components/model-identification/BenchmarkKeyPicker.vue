<template>
  <BaseDialog :show="show" :title="t('modelBenchmark.selectVersion')" @close="emit('close')">
    <div class="space-y-4">
      <p class="text-sm leading-6 text-gray-500">{{ t('modelBenchmark.ownKeysHint') }}</p>
      <form class="flex gap-2" @submit.prevent="load(1)">
        <input v-model.trim="search" class="input min-w-0 flex-1" :aria-label="t('modelBenchmark.searchKeys')" :placeholder="t('modelBenchmark.searchKeys')" maxlength="100" />
        <button class="btn btn-secondary" type="submit" :disabled="loading || selecting !== null">{{ t('common.search') }}</button>
      </form>
      <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
      <p v-if="loading" role="status" class="text-sm text-gray-500">{{ t('modelBenchmark.loading') }}</p>
      <p v-else-if="!keys.length" class="py-5 text-center text-sm text-gray-500">{{ t('modelBenchmark.noOwnKeys') }}</p>
      <div v-else class="max-h-96 space-y-2 overflow-auto">
        <button v-for="key in keys" :key="key.id" type="button" :data-key-id="key.id" :disabled="selecting !== null || unavailable(key)" class="flex w-full items-center justify-between gap-3 rounded-lg border border-gray-200 p-3 text-left hover:border-primary-400 disabled:cursor-not-allowed disabled:opacity-50 dark:border-dark-700" @click="select(key)">
          <span class="min-w-0"><span class="block truncate text-sm font-medium">{{ key.name }}</span><span class="mt-1 block text-xs text-gray-500">{{ key.group?.name || t('modelBenchmark.defaultGroup') }} · #{{ key.id }}</span></span>
          <span class="shrink-0 text-xs text-primary-600">{{ selecting === key.id ? t('modelBenchmark.loading') : unavailable(key) ? t('modelBenchmark.keyUnavailable') : t('modelBenchmark.useKey') }}</span>
        </button>
      </div>
      <div v-if="total > 20" class="flex items-center justify-end gap-3 text-sm">
        <button type="button" class="btn btn-secondary" :disabled="loading || selecting !== null || page <= 1" @click="load(page - 1)">{{ t('modelBenchmark.previous') }}</button>
        <span>{{ page }}</span>
        <button type="button" class="btn btn-secondary" :disabled="loading || selecting !== null || page * 20 >= total" @click="load(page + 1)">{{ t('modelBenchmark.next') }}</button>
      </div>
    </div>
  </BaseDialog>
</template>

<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { keysAPI } from '@/api/keys'
import { useAuthStore } from '@/stores/auth'
import type { ApiKey } from '@/types'

const props = defineProps<{ show: boolean }>()
const emit = defineEmits<{ close: []; select: [key: { id: number; name: string; key: string }] }>()
const { t } = useI18n()
const auth = useAuthStore()
type KeySummary = Pick<ApiKey, 'id' | 'user_id' | 'name' | 'status' | 'expires_at' | 'quota' | 'quota_used' | 'group'>
const keys = ref<KeySummary[]>([])
const search = ref('')
const loading = ref(false)
const selecting = ref<number | null>(null)
const error = ref('')
const page = ref(1)
const total = ref(0)
let request: AbortController | undefined
let generation = 0
function unavailable(key: KeySummary) {
  return key.status !== 'active' || (key.expires_at && Date.parse(key.expires_at) <= Date.now()) || (key.quota > 0 && key.quota_used >= key.quota)
}
async function load(nextPage = 1) {
  request?.abort()
  request = new AbortController()
  const current = ++generation
  loading.value = true
  error.value = ''
  try {
    const result = await keysAPI.list(nextPage, 20, { search: search.value, status: 'active' }, { signal: request.signal })
    if (current !== generation) return
    // Keep only metadata in the picker; fetch the selected key on demand.
    keys.value = result.items.filter(key => key.user_id === auth.user?.id).map(({ id, user_id, name, status, expires_at, quota, quota_used, group }) => ({ id, user_id, name, status, expires_at, quota, quota_used, group }))
    total.value = result.total
    page.value = nextPage
  } catch {
    if (current === generation) error.value = t('modelBenchmark.loadKeysError')
  } finally {
    if (current === generation) loading.value = false
  }
}
async function select(summary: KeySummary) {
  if (selecting.value !== null || unavailable(summary)) return
  selecting.value = summary.id
  const current = generation
  try {
    const key = await keysAPI.getById(summary.id)
    if (!props.show || current !== generation) return
    if (key.user_id !== auth.user?.id || unavailable(key) || !key.key) throw new Error('Unavailable key')
    emit('select', { id: key.id, name: key.name, key: key.key })
  } catch {
    if (current === generation) error.value = t('modelBenchmark.loadKeysError')
  } finally {
    if (current === generation) selecting.value = null
  }
}
function reset() { ++generation; request?.abort(); keys.value = []; selecting.value = null; loading.value = false; error.value = '' }
watch(() => props.show, show => { if (show) void load(1); else reset() }, { immediate: true })
onBeforeUnmount(reset)
</script>

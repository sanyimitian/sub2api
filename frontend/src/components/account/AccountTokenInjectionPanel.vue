<template>
  <section class="space-y-4" data-testid="token-injection-panel">
    <p class="rounded-lg bg-amber-50 p-3 text-sm text-amber-900 dark:bg-amber-900/20 dark:text-amber-200">
      {{ t('admin.accounts.tokenInjection.costHint') }}
    </p>
    <div class="space-y-1.5">
      <label class="text-sm font-medium text-gray-700 dark:text-gray-300">{{ t('admin.accounts.tokenInjection.protocol') }}</label>
      <Select v-model="protocol" :options="protocolOptions" :disabled="running" />
    </div>
    <div class="flex flex-wrap items-center gap-3">
      <button v-if="!running" type="button" class="btn btn-primary" :disabled="!model.trim()" @click="start">
        {{ t('admin.accounts.tokenInjection.start') }}
      </button>
      <button v-else type="button" class="btn btn-secondary" @click="cancel">{{ t('admin.accounts.tokenInjection.cancel') }}</button>
      <span v-if="running" role="status" class="text-sm text-gray-500">{{ t('admin.accounts.tokenInjection.progress', { current: progress, total: 5 }) }}</span>
      <button v-if="result" type="button" class="btn btn-secondary" @click="copyReport">{{ t('admin.accounts.tokenInjection.copy') }}</button>
    </div>
    <p v-if="error" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ error }}</p>
    <p v-if="cancelled" class="text-sm text-gray-500">{{ t('admin.accounts.tokenInjection.cancelled') }}</p>
    <div v-if="target" class="text-xs text-gray-500 dark:text-gray-400">
      {{ target.requested_model }} → {{ target.upstream_model }} · {{ target.protocol }} · {{ target.tokenizer }}
    </div>
    <div v-if="result" class="rounded-xl border border-gray-200 p-4 dark:border-dark-500">
      <div class="text-sm font-medium text-gray-600 dark:text-gray-300">{{ t('admin.accounts.tokenInjection.estimate') }}</div>
      <div v-if="result.status === 'stable' && result.estimate_tokens !== null" class="mt-2 text-3xl font-semibold tabular-nums text-gray-900 dark:text-gray-100">
        ≈ {{ formatNumber(result.estimate_tokens) }} <span class="text-sm font-normal text-gray-500">{{ t('admin.accounts.tokenInjection.unit') }}</span>
      </div>
      <p v-else class="mt-2 font-medium text-amber-700 dark:text-amber-300">{{ t('admin.accounts.tokenInjection.inconclusive') }}</p>
      <div class="mt-3 flex flex-wrap gap-x-4 gap-y-1 text-xs text-gray-500 dark:text-gray-400">
        <span>{{ t('admin.accounts.tokenInjection.slope') }} {{ result.slope.toFixed(3) }}</span>
        <span>{{ t('admin.accounts.tokenInjection.residual') }} {{ result.max_residual.toFixed(1) }}</span>
        <span>{{ t('admin.accounts.tokenInjection.repeatDelta') }} {{ formatNumber(result.repeat_delta) }}</span>
      </div>
      <p v-for="warning in result.warnings" :key="warning" class="mt-2 text-sm text-amber-700 dark:text-amber-300">{{ warningText(warning) }}</p>
    </div>
    <div v-if="samples.length" class="overflow-x-auto rounded-lg border border-gray-200 dark:border-dark-500">
      <table class="w-full whitespace-nowrap text-right text-xs tabular-nums">
        <thead class="bg-gray-50 text-gray-600 dark:bg-dark-700 dark:text-gray-300">
          <tr>
            <th class="p-2 text-left">{{ t('admin.accounts.tokenInjection.sample') }}</th>
            <th class="p-2">{{ t('admin.accounts.tokenInjection.local') }}</th>
            <th class="p-2">{{ t('admin.accounts.tokenInjection.reported') }}</th>
            <th class="p-2">{{ t('admin.accounts.tokenInjection.extra') }}</th>
            <th class="p-2">{{ t('admin.accounts.tokenInjection.cacheRead') }}</th>
            <th class="p-2">{{ t('admin.accounts.tokenInjection.cacheWrite') }}</th>
            <th class="p-2">{{ t('admin.accounts.tokenInjection.firstToken') }}</th>
            <th class="p-2">{{ t('admin.accounts.tokenInjection.duration') }}</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-gray-100 text-gray-700 dark:divide-dark-500 dark:text-gray-200">
          <tr v-for="sample in samples" :key="sample.index">
            <td class="p-2 text-left">{{ sample.index }}{{ sample.repeat ? ' · ' + t('admin.accounts.tokenInjection.repeat') : '' }}</td>
            <td class="p-2">{{ formatNumber(sample.local_tokens) }}</td>
            <td class="p-2">{{ formatNumber(sample.input_tokens) }}</td>
            <td class="p-2 font-medium">{{ formatNumber(sample.extra_tokens) }}</td>
            <td class="p-2">{{ formatNumber(sample.cache_read_tokens) }}</td>
            <td class="p-2">{{ formatNumber(sample.cache_write_tokens) }}</td>
            <td class="p-2">{{ seconds(sample.first_token_ms) }}</td>
            <td class="p-2">{{ seconds(sample.duration_ms) }}</td>
          </tr>
        </tbody>
      </table>
    </div>
    <details v-if="samples.length" class="text-xs text-gray-500 dark:text-gray-400">
      <summary class="cursor-pointer">{{ t('admin.accounts.tokenInjection.rawUsage') }}</summary>
      <pre class="mt-2 max-h-60 overflow-auto rounded-lg bg-gray-50 p-3 dark:bg-dark-700">{{ JSON.stringify(samples.map(s => ({ index: s.index, model: s.response_model, usage: s.raw_usage })), null, 2) }}</pre>
    </details>
    <p class="text-xs leading-relaxed text-gray-500 dark:text-gray-400">{{ t('admin.accounts.tokenInjection.method') }}</p>
  </section>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Select from '@/components/common/Select.vue'
import { useClipboard } from '@/composables/useClipboard'
import { streamTokenInjectionTest, type TokenInjectionEvent, type TokenInjectionProtocol, type TokenInjectionResult, type TokenInjectionSample } from '@/api/admin/accountTokenInjection'
import type { Account } from '@/types'

const props = defineProps<{ account: Account; model: string }>()
const emit = defineEmits<{ (event: 'running', value: boolean): void }>()
const { t } = useI18n()
const { copyToClipboard } = useClipboard()
const protocol = ref<TokenInjectionProtocol>('auto')
const running = ref(false)
const cancelled = ref(false)
const progress = ref(0)
const error = ref('')
const target = ref<TokenInjectionResult | null>(null)
const result = ref<TokenInjectionResult | null>(null)
const samples = ref<TokenInjectionSample[]>([])
let controller: AbortController | null = null
const protocolOptions = computed(() => [
  { value: 'auto', label: t('admin.accounts.tokenInjection.autoProtocol') },
  { value: 'responses', label: 'Responses' },
  ...(props.account.type === 'apikey' ? [{ value: 'chat', label: 'Chat Completions' }] : [])
])
const formatNumber = (value: number) => value.toLocaleString()
const seconds = (value: number | null) => value === null ? '—' : (value / 1000).toFixed(2) + 's'
function warningText(code: string) {
  const messages: Record<string, string> = {
    slope_mismatch: t('admin.accounts.tokenInjection.slopeMismatch'),
    unstable_usage: t('admin.accounts.tokenInjection.unstableUsage'),
    model_changed: t('admin.accounts.tokenInjection.modelChanged'),
    negative_overhead: t('admin.accounts.tokenInjection.negativeOverhead')
  }
  return messages[code] || code
}
function reset() {
  error.value = ''
  cancelled.value = false
  progress.value = 0
  target.value = null
  result.value = null
  samples.value = []
}
function cancel() {
  controller?.abort()
  controller = null
  running.value = false
  cancelled.value = true
  emit('running', false)
}
function handleEvent(event: TokenInjectionEvent) {
  switch (event.type) {
    case 'injection_start': target.value = event.data; break
    case 'injection_progress': progress.value = event.data.index; break
    case 'injection_sample': samples.value.push(event.data); break
    case 'injection_complete': result.value = event.data; samples.value = event.data.samples; break
  }
}
async function start() {
  if (running.value || !props.model.trim()) return
  reset()
  const active = new AbortController()
  controller = active
  running.value = true
  emit('running', true)
  try {
    await streamTokenInjectionTest(props.account.id, props.model, protocol.value, event => {
      if (controller === active) handleEvent(event)
    }, active.signal)
  } catch (cause) {
    if (controller === active && !active.signal.aborted) error.value = cause instanceof Error ? cause.message : t('admin.accounts.tokenInjection.failed')
  } finally {
    if (controller === active) {
      controller = null
      running.value = false
      emit('running', false)
    }
  }
}
function copyReport() {
  copyToClipboard(JSON.stringify(result.value, null, 2), t('admin.accounts.outputCopied'))
}
watch([() => props.account.id, () => props.model, protocol], () => {
  if (running.value) cancel()
  reset()
})
onBeforeUnmount(() => { if (running.value) cancel() })
</script>

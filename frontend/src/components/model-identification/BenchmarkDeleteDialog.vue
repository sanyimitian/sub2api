<template>
  <BaseDialog :show="!!run" :title="t('modelBenchmark.deleteResult')" width="narrow" @close="close">
    <div v-if="run" class="space-y-3">
      <p class="break-words text-sm font-medium">{{ run.model }} · {{ new Date(run.created_at).toLocaleString() }}</p>
      <p class="text-sm text-gray-600 dark:text-gray-300">{{ t('modelBenchmark.deleteResultHint') }}</p>
      <p v-if="error" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ error }}</p>
    </div>
    <template #footer>
      <div class="flex justify-end gap-3">
        <button type="button" class="btn btn-secondary" :disabled="pending" @click="close">{{ t('common.cancel') }}</button>
        <button type="button" class="btn btn-danger" :disabled="pending" data-testid="confirm-delete-result" @click="remove">{{ t(pending ? 'modelBenchmark.deletingResult' : 'modelBenchmark.deleteResult') }}</button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { benchmarks, type BenchmarkRun } from '@/api/admin/model-benchmarks'
import BaseDialog from '@/components/common/BaseDialog.vue'

const props = defineProps<{ run: BenchmarkRun | null }>()
const emit = defineEmits<{ close: []; deleted: [id: string] }>()
const { t } = useI18n()
const pending = ref(false), error = ref('')
let controller: AbortController | undefined
watch(() => props.run, () => { controller?.abort(); pending.value = false; error.value = '' })
function close() { if (!pending.value) emit('close') }
async function remove() {
  if (!props.run || pending.value) return
  const id = props.run.id
  pending.value = true; error.value = ''
  const request = new AbortController(); controller = request
  try {
    await benchmarks.deleteRun(id, request.signal)
    if (!request.signal.aborted) { emit('deleted', id); emit('close') }
  } catch (cause) {
    if (!request.signal.aborted) {
      const failure = cause as { response?: { data?: { message?: string } } }
      error.value = failure?.response?.data?.message || t('modelBenchmark.deleteResultError')
    }
  } finally { if (!request.signal.aborted) pending.value = false }
}
onBeforeUnmount(() => controller?.abort())
</script>

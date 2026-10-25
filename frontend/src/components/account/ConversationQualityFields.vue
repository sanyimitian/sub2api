<script setup lang="ts">
import { computed, onUnmounted, ref, useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import type { AdminGroup } from '@/types'
import { CONVERSATION_CANDY_PROMPT, CONVERSATION_CANDY_EXPECTED_ANSWER, defaultConversationQualityConfig, type ConversationQualityConfig } from '@/utils/openaiSessionPool'

const config = defineModel<ConversationQualityConfig>({ required: true })
const { t } = useI18n()
const id = useId()
const groups = ref<AdminGroup[]>([])
const judgeModels = ref<string[]>([])
const groupsLoading = ref(true)
const groupsFailed = ref(false)
const modelsLoading = ref(false)
const modelsFailed = ref(false)
let alive = true
let modelRequest = 0
const selectedGroup = computed({
  get: () => config.value.judge.group_id || '',
  set: (value: number | string) => { config.value.judge.group_id = Number(value) }
})
const hasSelectedGroup = computed(() => groups.value.some(group => group.id === config.value.judge.group_id))

async function loadGroups() {
  groupsLoading.value = true
  groupsFailed.value = false
  try {
    const result = await adminAPI.groups.getAll()
    if (alive) groups.value = result.filter(group => group.status === 'active')
  } catch {
    if (alive) groupsFailed.value = true
  } finally {
    if (alive) groupsLoading.value = false
  }
}

async function loadJudgeModels() {
  const request = ++modelRequest
  judgeModels.value = []
  modelsFailed.value = false
  const groupID = config.value.judge.group_id
  if (!groupID) { modelsLoading.value = false; return }
  modelsLoading.value = true
  try {
    const result = await adminAPI.groups.getModelAllowlistCandidates(groupID)
    if (alive && request === modelRequest) judgeModels.value = result
  } catch {
    if (alive && request === modelRequest) modelsFailed.value = true
  } finally {
    if (alive && request === modelRequest) modelsLoading.value = false
  }
}

function useCandyQuestion() {
  config.value = { ...config.value, prompt: CONVERSATION_CANDY_PROMPT, expected_answer: CONVERSATION_CANDY_EXPECTED_ANSWER }
}

function resetJudgePrompt() {
  config.value = { ...config.value, judge: { ...config.value.judge, prompt: defaultConversationQualityConfig().judge.prompt } }
}

watch(() => config.value.judge.group_id, loadJudgeModels, { immediate: true })
void loadGroups()
onUnmounted(() => { alive = false; modelRequest++ })
</script>

<template>
  <fieldset class="space-y-3 border-t border-gray-200 pt-3 dark:border-dark-600" data-testid="conversation-quality-fields">
    <legend class="sr-only">{{ t('admin.accounts.sessionPool.qualityProbe') }}</legend>
    <div>
      <label :for="id + '-kind'" class="input-label">{{ t('admin.accounts.sessionPool.questionKind') }}</label>
      <select :id="id + '-kind'" v-model="config.question_kind" class="input" data-testid="conversation-quality-kind">
        <option value="candy">{{ t('admin.accounts.sessionPool.fixedQuestion') }}</option>
        <option value="random_reasoning">{{ t('admin.accounts.sessionPool.randomQuestion') }}</option>
      </select>
      <p v-if="config.question_kind === 'random_reasoning'" class="mt-1 text-xs text-gray-600 dark:text-gray-300">{{ t('admin.accounts.sessionPool.randomHint') }}</p>
    </div>
    <template v-if="config.question_kind === 'candy'">
      <div>
        <div class="mb-1 flex flex-wrap items-center justify-between gap-2">
          <label :for="id + '-question'" class="input-label mb-0">{{ t('admin.accounts.sessionPool.question') }}</label>
          <button type="button" class="text-xs text-primary-700 hover:underline dark:text-primary-300" @click="useCandyQuestion">{{ t('admin.accounts.sessionPool.useCandy') }}</button>
        </div>
        <textarea :id="id + '-question'" v-model.trim="config.prompt" required maxlength="16000" rows="5" class="input text-sm" data-testid="conversation-quality-question" />
      </div>
      <div>
        <label :for="id + '-answer'" class="input-label">{{ t('admin.accounts.sessionPool.expectedAnswer') }}</label>
        <textarea :id="id + '-answer'" v-model.trim="config.expected_answer" required maxlength="4000" rows="2" class="input text-sm" />
      </div>
    </template>
    <div>
      <label :for="id + '-effort'" class="input-label">{{ t('admin.accounts.sessionPool.reasoningEffort') }}</label>
      <select :id="id + '-effort'" v-model="config.reasoning_effort" class="input">
        <option value="">{{ t('admin.accounts.sessionPool.defaultEffort') }}</option>
        <option v-for="effort in ['none', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max', 'ultra']" :key="effort" :value="effort">{{ t(`admin.accounts.sessionPool.efforts.${effort}`) }}</option>
      </select>
    </div>
    <div class="grid gap-3 sm:grid-cols-2">
      <div>
        <div class="mb-1 flex items-center justify-between gap-2">
          <label :for="id + '-group'" class="input-label mb-0">{{ t('admin.accounts.sessionPool.judgeGroup') }}</label>
          <button v-if="groupsFailed" type="button" class="text-xs text-primary-700 hover:underline dark:text-primary-300" @click="loadGroups">{{ t('admin.accounts.sessionPool.retry') }}</button>
        </div>
        <select :id="id + '-group'" v-model="selectedGroup" required class="input" data-testid="conversation-quality-judge-group">
          <option disabled value="">{{ groupsLoading ? t('common.loading') : t('admin.accounts.sessionPool.selectJudgeGroup') }}</option>
          <option v-if="config.judge.group_id && !hasSelectedGroup" :value="config.judge.group_id">#{{ config.judge.group_id }}</option>
          <option v-for="group in groups" :key="group.id" :value="group.id">{{ group.name }} #{{ group.id }}</option>
        </select>
        <p v-if="groupsFailed" role="alert" class="mt-1 text-xs text-amber-700 dark:text-amber-300">{{ t('admin.accounts.sessionPool.groupsLoadFailed') }}</p>
      </div>
      <div>
        <label :for="id + '-judge-model'" class="input-label">{{ t('admin.accounts.sessionPool.judgeModel') }}</label>
        <input :id="id + '-judge-model'" v-model.trim="config.judge.model_id" :list="id + '-judge-models'" required maxlength="100" class="input" :placeholder="t('admin.accounts.sessionPool.selectJudgeModel')" data-testid="conversation-quality-judge-model" />
        <datalist :id="id + '-judge-models'"><option v-for="model in judgeModels" :key="model" :value="model" /></datalist>
        <p v-if="modelsLoading" class="mt-1 text-xs text-gray-600 dark:text-gray-300" role="status">{{ t('admin.accounts.sessionPool.loadingModels') }}</p>
        <p v-if="modelsFailed" class="mt-1 text-xs text-amber-700 dark:text-amber-300" role="alert">{{ t('admin.accounts.sessionPool.judgeModelsLoadFailed') }} <button type="button" class="underline" @click="loadJudgeModels">{{ t('admin.accounts.sessionPool.retry') }}</button></p>
      </div>
    </div>
    <div>
      <div class="mb-1 flex flex-wrap items-center justify-between gap-2">
        <label :for="id + '-judge-prompt'" class="input-label mb-0">{{ t('admin.accounts.sessionPool.judgePrompt') }}</label>
        <button type="button" class="text-xs text-primary-700 hover:underline dark:text-primary-300" @click="resetJudgePrompt">{{ t('admin.accounts.sessionPool.resetJudgePrompt') }}</button>
      </div>
      <textarea :id="id + '-judge-prompt'" v-model.trim="config.judge.prompt" required maxlength="16000" rows="3" class="input text-sm" />
      <p class="mt-1 text-xs text-gray-600 dark:text-gray-300">{{ t('admin.accounts.sessionPool.judgeHint') }}</p>
    </div>
  </fieldset>
</template>

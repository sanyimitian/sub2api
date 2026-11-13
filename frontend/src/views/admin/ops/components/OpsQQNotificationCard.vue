<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { opsAPI, type QQNotificationConfig } from '@/api/admin/ops'
import { useAppStore } from '@/stores/app'
import Toggle from '@/components/common/Toggle.vue'

const { t } = useI18n()
const appStore = useAppStore()
const config = ref<QQNotificationConfig | null>(null)
const token = ref('')
const clearToken = ref(false)
const busy = ref(false)
const loading = ref(false)

async function load() {
  loading.value = true
  try { config.value = await opsAPI.getQQNotificationConfig() }
  catch { appStore.showError(t('admin.ops.qq.loadFailed')) }
  finally { loading.value = false }
}
onMounted(load)

async function save(test = false) {
  if (!config.value) return
  busy.value = true
  try {
    config.value = await opsAPI.updateQQNotificationConfig({
      enabled: config.value.enabled, api_url: config.value.api_url, group_id: config.value.group_id,
      access_token: token.value || undefined, clear_access_token: clearToken.value
    })
    token.value = ''
    clearToken.value = false
    if (test) await opsAPI.testQQNotification()
    appStore.showSuccess(t(test ? 'admin.ops.qq.testSuccess' : 'admin.ops.qq.saveSuccess'))
  } catch (err: any) {
    appStore.showError(err?.response?.data?.detail || err?.response?.data?.message || t('admin.ops.qq.saveFailed'))
  } finally { busy.value = false }
}
</script>

<template>
  <section class="space-y-4 rounded-xl border border-gray-200 p-4 dark:border-dark-700">
    <h3 class="font-bold text-gray-900 dark:text-white">{{ t('admin.ops.qq.title') }}</h3>
    <p class="text-xs text-gray-500">{{ t('admin.ops.qq.description') }}</p>
    <div v-if="loading" class="text-sm text-gray-500">{{ t('common.loading') }}</div>
    <button v-else-if="!config" class="btn btn-secondary" type="button" @click="load">{{ t('common.refresh') }}</button>
    <template v-else>
      <div class="flex items-center justify-between">
        <label>{{ t('admin.ops.qq.enabled') }}</label>
        <Toggle v-model="config.enabled" />
      </div>
      <div>
        <label class="input-label" for="ops-qq-url">{{ t('admin.ops.qq.apiURL') }}</label>
        <input id="ops-qq-url" v-model="config.api_url" class="input" type="url" placeholder="http://127.0.0.1:3000" />
      </div>
      <div>
        <label class="input-label" for="ops-qq-group">{{ t('admin.ops.qq.groupID') }}</label>
        <input id="ops-qq-group" v-model="config.group_id" class="input" type="text" inputmode="numeric" />
      </div>
      <div>
        <label class="input-label" for="ops-qq-token">{{ t('admin.ops.qq.token') }}</label>
        <input id="ops-qq-token" v-model="token" class="input" type="password" autocomplete="new-password" :placeholder="config.access_token_configured ? t('admin.ops.qq.tokenConfigured') : ''" />
        <label v-if="config.access_token_configured" class="mt-2 flex items-center gap-2 text-xs text-gray-500">
          <input v-model="clearToken" type="checkbox" />{{ t('admin.ops.qq.clearToken') }}
        </label>
      </div>
      <div class="flex flex-wrap gap-2">
        <button type="button" class="btn btn-secondary" :disabled="busy" @click="save(false)">{{ t('admin.ops.qq.save') }}</button>
        <button type="button" class="btn btn-secondary" :disabled="busy" @click="save(true)">{{ t('admin.ops.qq.test') }}</button>
      </div>
    </template>
  </section>
</template>

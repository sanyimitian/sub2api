<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { opsAPI, type QQNotificationConfig, type QQWebhookStatus } from '@/api/admin/ops'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import Toggle from '@/components/common/Toggle.vue'

defineProps<{ disabled?: boolean }>()

const { t } = useI18n()
const appStore = useAppStore()
const config = ref<QQNotificationConfig | null>(null)
const token = ref('')
const clearToken = ref(false)
const appSecret = ref('')
const clearAppSecret = ref(false)
const busy = ref(false)
const errorMessage = ref('')
const loading = ref(false)
const webhook = ref<QQWebhookStatus | null>(null)
const webhookLoading = ref(false)
const canSave = computed(() => !!config.value && !loading.value && !busy.value)
const callbackURL = computed(() => window.location.origin + '/api/v1/ops/qq-bot/webhook')
const currentWebhook = computed(() => webhook.value?.app_id === config.value?.app_id ? webhook.value : null)

async function refreshWebhook() {
  webhookLoading.value = true
  try { webhook.value = await opsAPI.getQQWebhookStatus() }
  catch { appStore.showError(t('admin.ops.qq.webhookLoadFailed')) }
  finally { webhookLoading.value = false }
}

watch(() => config.value?.provider, () => {
  token.value = ''
  clearToken.value = false
  appSecret.value = ''
  clearAppSecret.value = false
  webhook.value = null
})

async function load() {
  loading.value = true
  try {
    const saved = await opsAPI.getQQNotificationConfig()
    config.value = { ...saved, provider: saved.provider || 'onebot' }
    if (config.value.provider === 'qq_official') await refreshWebhook()
  }
  catch { appStore.showError(t('admin.ops.qq.loadFailed')) }
  finally { loading.value = false }
}
onMounted(load)

async function save(test = false, notify = true): Promise<boolean> {
  if (!config.value || !canSave.value) return false
  busy.value = true
  errorMessage.value = ''
  try {
    config.value = await opsAPI.updateQQNotificationConfig({
      enabled: config.value.enabled, provider: config.value.provider,
      api_url: config.value.api_url, group_id: config.value.group_id,
      access_token: config.value.provider === 'onebot' ? token.value || undefined : undefined,
      clear_access_token: config.value.provider === 'onebot' && clearToken.value,
      app_id: config.value.app_id || '', group_openid: config.value.group_openid || '',
      app_secret: config.value.provider === 'qq_official' ? appSecret.value || undefined : undefined,
      clear_app_secret: config.value.provider === 'qq_official' && clearAppSecret.value
    })
    token.value = ''
    clearToken.value = false
    appSecret.value = ''
    clearAppSecret.value = false
    if (test) await opsAPI.testQQNotification()
    if (notify) appStore.showSuccess(t(test ? 'admin.ops.qq.testSuccess' : 'admin.ops.qq.saveSuccess'))
    if (config.value.provider === 'qq_official') await refreshWebhook()
    return true
  } catch (err: unknown) {
    const message = extractApiErrorMessage(err, t('admin.ops.qq.saveFailed'))
    errorMessage.value = message.startsWith('QQ official bot returned ') && message.includes('(code 40034105)')
      ? t('admin.ops.qq.officialPermissionDenied')
      : message
    appStore.showError(errorMessage.value)
    return false
  } finally { busy.value = false }
}

defineExpose({ save, canSave })
</script>

<template>
  <section class="space-y-4 rounded-xl border border-gray-200 p-4 dark:border-dark-700">
    <h3 class="font-bold text-gray-900 dark:text-white">{{ t('admin.ops.qq.title') }}</h3>
    <p class="text-xs text-gray-500">{{ t('admin.ops.qq.description') }}</p>
    <p v-if="errorMessage" role="alert" class="rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-400">{{ errorMessage }}</p>
    <div v-if="loading" class="text-sm text-gray-500">{{ t('common.loading') }}</div>
    <button v-else-if="!config" class="btn btn-secondary" type="button" @click="load">{{ t('common.refresh') }}</button>
    <fieldset v-else class="space-y-4" :disabled="busy || disabled">
      <div class="flex items-center justify-between">
        <label>{{ t('admin.ops.qq.enabled') }}</label>
        <Toggle v-model="config.enabled" />
      </div>
      <div>
        <label class="input-label" for="ops-qq-provider">{{ t('admin.ops.qq.provider') }}</label>
        <select id="ops-qq-provider" v-model="config.provider" class="input">
          <option value="onebot">{{ t('admin.ops.qq.onebot') }}</option>
          <option value="qq_official">{{ t('admin.ops.qq.official') }}</option>
        </select>
      </div>
      <template v-if="config.provider === 'qq_official'">
        <p class="text-xs text-gray-500">{{ t('admin.ops.qq.officialHint') }}</p>
        <div>
          <label class="input-label" for="ops-qq-app-id">{{ t('admin.ops.qq.appID') }}</label>
          <input id="ops-qq-app-id" v-model="config.app_id" class="input" type="text" inputmode="numeric" autocomplete="off" />
        </div>
        <div>
          <label class="input-label" for="ops-qq-app-secret">{{ t('admin.ops.qq.appSecret') }}</label>
          <input id="ops-qq-app-secret" v-model="appSecret" class="input" type="password" autocomplete="new-password" :placeholder="config.app_secret_configured ? t('admin.ops.qq.secretConfigured') : ''" />
          <label v-if="config.app_secret_configured" class="mt-2 flex items-center gap-2 text-xs text-gray-500">
            <input id="ops-qq-clear-secret" v-model="clearAppSecret" type="checkbox" />{{ t('admin.ops.qq.clearSecret') }}
          </label>
        </div>
        <div>
          <label class="input-label" for="ops-qq-group-openid">{{ t('admin.ops.qq.groupOpenID') }}</label>
          <input id="ops-qq-group-openid" v-model="config.group_openid" class="input" type="text" autocomplete="off" />
          <p class="mt-1 text-xs text-gray-500">{{ t('admin.ops.qq.groupOpenIDHint') }}</p>
        </div>
        <div class="space-y-3 rounded-lg bg-gray-50 p-3 dark:bg-dark-800">
          <label class="input-label" for="ops-qq-callback">{{ t('admin.ops.qq.callbackURL') }}</label>
          <input id="ops-qq-callback" :value="callbackURL" class="input" type="text" readonly @focus="($event.target as HTMLInputElement).select()" />
          <p class="text-xs text-gray-500">{{ t('admin.ops.qq.callbackSetup') }}</p>
          <p class="text-xs text-gray-500">{{ t('admin.ops.qq.callbackEvents') }}</p>
          <p class="text-sm" :class="currentWebhook?.ready ? 'text-green-600' : 'text-amber-600'">
            {{ t(currentWebhook?.ready ? 'admin.ops.qq.callbackReady' : 'admin.ops.qq.callbackNotReady') }}
          </p>
          <button id="ops-qq-refresh-groups" type="button" class="btn btn-secondary" :disabled="webhookLoading" @click="refreshWebhook">
            {{ t(webhookLoading ? 'common.loading' : 'admin.ops.qq.refreshGroups') }}
          </button>
          <p v-if="currentWebhook?.last_event_at" class="text-xs text-gray-500">
            {{ t('admin.ops.qq.lastEvent') }}: {{ new Date(currentWebhook.last_event_at).toLocaleString() }}
          </p>
          <ul v-if="currentWebhook?.groups.length" class="space-y-2">
            <li v-for="group in currentWebhook.groups" :key="group.group_openid" class="flex flex-wrap items-center gap-2 text-sm">
              <code class="break-all">{{ group.group_openid }}</code>
              <button type="button" class="btn btn-secondary" :data-group-openid="group.group_openid" @click="config.group_openid = group.group_openid">{{ t('admin.ops.qq.useGroup') }}</button>
            </li>
          </ul>
          <p v-else class="text-xs text-gray-500">{{ t('admin.ops.qq.noGroups') }}</p>
        </div>
      </template>
      <template v-else>
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
      </template>
      <div class="flex flex-wrap gap-2">
        <button type="button" class="btn btn-secondary" :disabled="busy" @click="save(false)">{{ t('admin.ops.qq.save') }}</button>
        <button type="button" class="btn btn-secondary" :disabled="busy" @click="save(true)">{{ t('admin.ops.qq.test') }}</button>
      </div>
    </fieldset>
  </section>
</template>

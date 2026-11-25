<script setup lang="ts">
import { defineAsyncComponent, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { getAuthRegionAccess, type RegionLoginBlockReason } from '@/api/authRegion'
import RegionAccessBlocked from '@/components/common/RegionAccessBlocked.vue'

// 预检完成前不加载或挂载登录表单，避免密码输入及第三方登录入口提前出现。
const LoginView = defineAsyncComponent(() => import('./LoginView.vue'))
const { t } = useI18n()
const stage = ref<'checking' | 'blocked' | 'login'>('checking')
const reason = ref<RegionLoginBlockReason>('REGION_LOOKUP_UNAVAILABLE')
const retrying = ref(false)
let requestVersion = 0
let pendingRequest: AbortController | undefined

function cancelCheck() {
  requestVersion += 1
  pendingRequest?.abort()
  pendingRequest = undefined
  retrying.value = false
}

async function checkAccess() {
  cancelCheck()
  const version = requestVersion
  const controller = new AbortController()
  pendingRequest = controller
  retrying.value = true
  try {
    const result = await getAuthRegionAccess(controller.signal)
    if (version !== requestVersion) return
    if (result.allowed === true) {
      stage.value = 'login'
    } else {
      reason.value = result.reason === 'MAINLAND_ACCESS_DENIED' ? result.reason : 'REGION_LOOKUP_UNAVAILABLE'
      stage.value = 'blocked'
    }
  } catch {
    if (version !== requestVersion) return
    reason.value = 'REGION_LOOKUP_UNAVAILABLE'
    stage.value = 'blocked'
  } finally {
    if (version === requestVersion) {
      pendingRequest = undefined
      retrying.value = false
    }
  }
}

function onLoginBlocked(event: Event) {
  cancelCheck()
  const detail = (event as CustomEvent<{ reason?: unknown }>).detail
  reason.value = detail?.reason === 'MAINLAND_ACCESS_DENIED' ? detail.reason : 'REGION_LOOKUP_UNAVAILABLE'
  // 卸载原登录组件，清除其内存中的密码及临时二次验证状态。
  stage.value = 'blocked'
}

onMounted(() => {
  window.addEventListener('region-login-blocked', onLoginBlocked)
  void checkAccess()
})

onBeforeUnmount(() => {
  cancelCheck()
  window.removeEventListener('region-login-blocked', onLoginBlocked)
})
</script>

<template>
  <div v-if="stage === 'checking'" class="flex min-h-dvh items-center justify-center bg-gray-50 p-6 dark:bg-dark-950">
    <p role="status" class="text-sm text-gray-600 dark:text-gray-300">{{ t('regionAccess.pendingLabel') }}</p>
  </div>
  <LoginView v-else-if="stage === 'login'" />
  <RegionAccessBlocked v-else :reason="reason" :retrying="retrying" @retry="checkAccess" />
</template>

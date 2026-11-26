<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { getSiteRegionAccess, type RegionLoginBlockReason } from '@/api/authRegion'
import { getSetupStatus } from '@/api/setup'
import { getSiteRegionBlockReason } from '@/utils/siteRegionAccess'
import RegionAccessBlocked from './RegionAccessBlocked.vue'

const router = useRouter()
const { t } = useI18n()
const pending = ref(true)
const checking = ref(false)
const reason = ref<RegionLoginBlockReason | null>(null)
let generation = 0
let timer: ReturnType<typeof setInterval> | undefined
let removeRouteHook: (() => void) | undefined
const controller = new AbortController()

async function check() {
  if (checking.value) return
  const current = ++generation
  checking.value = true
  try {
    const result = await getSiteRegionAccess(controller.signal)
    if (current !== generation) return
    reason.value = result.allowed ? null : result.reason ?? 'REGION_LOOKUP_UNAVAILABLE'
  } catch (error) {
    if (current !== generation || controller.signal.aborted) return
    // 首次安装尚未注册业务路由；只有安装状态明确确认后才显示安装界面。
    if ((error as { status?: number }).status === 404) {
      try {
        const setup = await getSetupStatus()
        if (current !== generation) return
        if (setup.needs_setup) {
          reason.value = null
          return
        }
      } catch { /* 无法确认安装状态时保持访问受限 */ }
    }
    if (current !== generation || controller.signal.aborted) return
    const errorReason = getSiteRegionBlockReason(error)
    if (errorReason) {
      reason.value = errorReason
    } else if (pending.value) {
      reason.value = 'REGION_LOOKUP_UNAVAILABLE'
    }
    // 后续断网、限流等传输故障保留上次判定，避免卸载页面并丢失表单。
    // 已有拒绝只能通过成功校验解除；业务请求始终由服务端重新检查。
  } finally {
    if (!controller.signal.aborted) {
      pending.value = false
      checking.value = false
    }
  }
}

function onBlocked(event: Event) {
  const value = (event as CustomEvent<{ reason?: string }>).detail?.reason
  if (value !== 'MAINLAND_ACCESS_DENIED' && value !== 'REGION_LOOKUP_UNAVAILABLE') return
  // 业务请求已被拒绝时，较早发出的成功预检不能重新显示受保护内容。
  generation++
  reason.value = value
  pending.value = false
}

function checkWhenVisible() {
  if (document.visibilityState === 'visible') void check()
}

onMounted(() => {
  window.addEventListener('region-site-blocked', onBlocked)
  window.addEventListener('focus', checkWhenVisible)
  document.addEventListener('visibilitychange', checkWhenVisible)
  removeRouteHook = router.afterEach(() => { void check() })
  timer = setInterval(checkWhenVisible, 15_000)
  void check()
})

onBeforeUnmount(() => {
  controller.abort()
  generation++
  if (timer) clearInterval(timer)
  removeRouteHook?.()
  window.removeEventListener('region-site-blocked', onBlocked)
  window.removeEventListener('focus', checkWhenVisible)
  document.removeEventListener('visibilitychange', checkWhenVisible)
})
</script>

<template>
  <main v-if="pending" class="flex min-h-screen items-center justify-center bg-gray-50 p-6 dark:bg-dark-950" aria-busy="true">
    <p role="status" class="text-sm text-gray-600 dark:text-gray-400">{{ t('regionAccess.sitePending') }}</p>
  </main>
  <RegionAccessBlocked v-else-if="reason" :reason="reason" :retrying="checking" site-wide @retry="check" />
  <slot v-else />
</template>

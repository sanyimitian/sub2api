<template>
  <div class="hub-page text-white">
    <div class="hub-starfield" aria-hidden="true">
      <i class="hub-starfield-layer hub-starfield-layer--a" :style="{ boxShadow: starLayerA }" />
      <i class="hub-starfield-layer hub-starfield-layer--b" :style="{ boxShadow: starLayerB }" />
    </div>
    <header class="hub-header pointer-events-none">
      <nav class="hub-nav pointer-events-auto">
        <router-link to="/" class="hub-nav-brand">
          <img
            v-if="siteLogo"
            :src="siteLogo"
            alt=""
            class="h-8 w-8 shrink-0 rounded-lg object-contain"
          />
          <span class="truncate">{{ siteName }}</span>
        </router-link>

        <div class="hub-nav-links">
          <router-link :to="consolePath" class="hub-nav-link" :class="{ 'hub-nav-link--active': isNavActive(consolePath) }">
            {{ t('home.dashboard') }}
          </router-link>
          <router-link
            v-if="showModelPlazaEntry"
            to="/model-plaza"
            class="hub-nav-link"
            :class="{ 'hub-nav-link--active': isNavActive('/model-plaza') }"
          >
            {{ t('nav.modelPlaza') }}
          </router-link>
          <a
            v-if="docUrl"
            :href="docUrl"
            target="_blank"
            rel="noopener noreferrer"
            class="hub-nav-link"
          >
            {{ t('home.docs') }}
          </a>
        </div>

        <div class="hub-nav-spacer" aria-hidden="true" />

        <div class="hub-header-divider" aria-hidden="true" />

        <div class="hub-header-tools">
          <LocaleSwitcher compact />
          <button
            type="button"
            class="hub-header-icon-btn"
            :title="isDark ? t('home.switchToLight') : t('home.switchToDark')"
            @click="toggleTheme"
          >
            <Icon v-if="isDark" name="sun" size="md" />
            <Icon v-else name="moon" size="md" />
          </button>
          <AnnouncementBell v-if="isAuthenticated" class="hub-announcement-bell" />
          <router-link
            :to="isAuthenticated ? dashboardPath : '/login'"
            class="hub-header-login"
          >
            {{ isAuthenticated ? t('home.dashboard') : t('home.login') }}
          </router-link>
        </div>
      </nav>
    </header>

    <!-- 中间行：左文案 + 右 3D，整行在顶栏与图例之间垂直居中 -->
    <div class="hub-body">
      <section class="hub-copy font-sans">
        <p class="inline-flex items-center gap-2 rounded-full border border-white/10 bg-white/[0.06] px-3 py-1.5 text-[12px] leading-none text-white/55">
          <span class="h-1.5 w-1.5 shrink-0 rounded-full bg-emerald-400" />
          {{ t('home.hub.badge') }}
        </p>
        <h1 class="mt-5 text-[36px] font-bold leading-[1.2] tracking-tight sm:mt-6 sm:text-[44px] xl:text-[50px]">
          <span class="block text-white">{{ t('home.hub.titleLine1') }}</span>
          <span class="block text-zinc-300">{{ t('home.hub.titleLine2') }}</span>
          <span class="block text-zinc-300">{{ t('home.hub.titleLine3') }}</span>
        </h1>
        <p class="mt-5 text-[15px] leading-[1.6] text-zinc-300">
          {{ t('home.hub.lead') }}
        </p>
        <p class="mt-3 text-[13px] leading-[1.65] text-zinc-400">
          {{ t('home.hub.detail') }}
        </p>
        <div class="mt-6 flex flex-col items-start">
          <router-link
            to="/keys"
            class="group inline-flex items-center gap-2 rounded-full bg-white px-6 py-3 text-sm font-bold text-black no-underline transition hover:bg-zinc-200"
          >
            {{ t('home.hub.recharge') }}
            <svg class="hub-trial-arrow h-4 w-4" viewBox="0 0 16 16" fill="none" aria-hidden="true">
              <path d="M3 8h10M9 4l4 4-4 4" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" />
            </svg>
          </router-link>
          <div class="mt-5 flex items-center gap-8">
            <router-link
              :to="consolePath"
              class="group inline-flex items-center gap-1.5 text-sm text-white/70 no-underline transition hover:text-white"
            >
              {{ t('home.hub.console') }}
              <svg class="h-3.5 w-3.5 transition-transform duration-200 group-hover:translate-x-1" viewBox="0 0 16 16" fill="none" aria-hidden="true">
                <path d="M3 8h10M9 4l4 4-4 4" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" />
              </svg>
            </router-link>
            <router-link
              to="/monitor"
              class="group inline-flex items-center gap-1.5 text-sm text-white/70 no-underline transition hover:text-white"
            >
              {{ t('home.hub.pricing') }}
              <svg class="h-3.5 w-3.5 transition-transform duration-200 group-hover:translate-x-1" viewBox="0 0 16 16" fill="none" aria-hidden="true">
                <path d="M3 8h10M9 4l4 4-4 4" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" />
              </svg>
            </router-link>
          </div>
        </div>

        <div class="mt-9 flex items-stretch sm:mt-10">
          <div
            v-for="(metric, index) in metrics"
            :key="metric.label"
            class="flex min-w-0 items-stretch"
          >
            <div
              v-if="index > 0"
              class="mx-5 w-px self-stretch bg-white/15 sm:mx-7"
              aria-hidden="true"
            />
            <div class="min-w-0">
              <p class="text-[26px] font-bold leading-none tracking-tight text-white sm:text-[30px]">
                {{ metric.value }}
              </p>
              <p class="mt-2 text-[12px] leading-none text-white/45">
                {{ metric.label }}
              </p>
            </div>
          </div>
        </div>
      </section>

      <section ref="visualRef" class="hub-visual">
        <canvas ref="canvasRef" class="hub-canvas" />
        <div
          v-for="label in labels"
          v-show="label.visible"
          :key="`node-${label.name}`"
          class="hub-node-label pointer-events-none"
          :style="nodeLabelStyle(label)"
        >
          <i class="hub-node-dot" :style="{ backgroundColor: softenLabelColor(label.color) }" />
          {{ label.name }}
        </div>
        <div
          v-if="webglError"
          class="absolute inset-0 z-10 flex items-center justify-center px-4 text-center text-sm text-white/70"
        >
          {{ t('home.hub.webglError') }}
        </div>
      </section>
    </div>

    <footer class="hub-legend pointer-events-none">
      <div class="hub-legend-list">
        <span class="hub-legend-item hub-legend-item--muted">
          <i class="hub-legend-dot" :style="{ backgroundColor: hubPurple }" />
          {{ t('home.hub.center') }}
        </span>
        <span
          v-for="node in hubNodes"
          :key="node.name"
          class="hub-legend-item"
          :style="{ color: softenLabelColor(node.color) }"
        >
          <i class="hub-legend-dot" :style="{ backgroundColor: node.color }" />
          {{ node.name }}
        </span>
      </div>
    </footer>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute } from 'vue-router'
import LocaleSwitcher from '@/components/common/LocaleSwitcher.vue'
import AnnouncementBell from '@/components/common/AnnouncementBell.vue'
import Icon from '@/components/icons/Icon.vue'
import { useAppStore, useAuthStore } from '@/stores'
import { sanitizeUrl } from '@/utils/url'
import { FeatureFlags, isFeatureFlagEnabled } from '@/utils/featureFlags'
import { HUB_NODES, HUB_PURPLE } from './hub/hubPhysics'
import { createHubView, type HubNodeLabel, type HubView } from './hub/hubScene'

const hubNodes = HUB_NODES
const hubPurple = HUB_PURPLE

const { t } = useI18n()
const route = useRoute()
const appStore = useAppStore()
const authStore = useAuthStore()

const canvasRef = ref<HTMLCanvasElement | null>(null)
const visualRef = ref<HTMLElement | null>(null)
const webglError = ref(false)
const labels = ref<HubNodeLabel[]>([])
const isDark = ref(document.documentElement.classList.contains('dark'))

function softenLabelColor(hex: string): string {
  const value = hex.replace('#', '')
  if (value.length !== 6) return hex
  const r = Number.parseInt(value.slice(0, 2), 16)
  const g = Number.parseInt(value.slice(2, 4), 16)
  const b = Number.parseInt(value.slice(4, 6), 16)
  const mix = 0.32
  const nr = Math.round(r * (1 - mix))
  const ng = Math.round(g * (1 - mix))
  const nb = Math.round(b * (1 - mix))
  return `rgb(${nr}, ${ng}, ${nb})`
}

function nodeLabelStyle(label: HubNodeLabel) {
  const tint = softenLabelColor(label.color)
  return {
    left: `${label.x}px`,
    top: `${label.y}px`,
    transform: 'translate(-50%, 0)',
    color: tint,
    textShadow: `0 0 3px ${tint}33`,
  }
}

function toggleTheme() {
  isDark.value = !isDark.value
  document.documentElement.classList.toggle('dark', isDark.value)
  localStorage.setItem('theme', isDark.value ? 'dark' : 'light')
}

function makeStarShadows(count: number, seed: number): string {
  const dots: string[] = []
  let n = seed
  const next = () => {
    n = (n * 16807) % 2147483647
    return n / 2147483647
  }
  for (let index = 0; index < count; index += 1) {
    const x = (next() * 100).toFixed(2)
    const y = (next() * 100).toFixed(2)
    const alpha = (0.4 + next() * 0.6).toFixed(2)
    const blur = (1.8 + next() * 2.2).toFixed(1)
    dots.push(`${x}vw ${y}vh ${blur}px rgba(226, 236, 255, ${alpha})`)
  }
  return dots.join(', ')
}

const starLayerA = makeStarShadows(70, 17)
const starLayerB = makeStarShadows(46, 91)

const siteName = computed(() => appStore.cachedPublicSettings?.site_name || appStore.siteName || 'Codebot')
const siteLogo = computed(() =>
  sanitizeUrl(appStore.cachedPublicSettings?.site_logo || appStore.siteLogo || '', {
    allowRelative: true,
    allowDataUrl: true,
  }),
)
const docUrl = computed(() => sanitizeUrl(appStore.cachedPublicSettings?.doc_url || appStore.docUrl || ''))
const isAuthenticated = computed(() => authStore.isAuthenticated)
const dashboardPath = computed(() => (authStore.isAdmin ? '/admin/dashboard' : '/dashboard'))
const consolePath = computed(() => (isAuthenticated.value ? dashboardPath.value : '/login'))
const modelPlazaEnabled = computed(() => isFeatureFlagEnabled(FeatureFlags.modelPlaza))
const modelPlazaRequiresAuth = computed(
  () => appStore.cachedPublicSettings?.model_plaza_require_auth === true,
)
const showModelPlazaEntry = computed(
  () => modelPlazaEnabled.value && (isAuthenticated.value || !modelPlazaRequiresAuth.value),
)

function isNavActive(path: string) {
  return route.path === path || route.path.startsWith(`${path}/`)
}

const metrics = computed(() => [
  { value: t('home.hub.metric1Value'), label: t('home.hub.metric1Label') },
  { value: t('home.hub.metric2Value'), label: t('home.hub.metric2Label') },
  { value: t('home.hub.metric3Value'), label: t('home.hub.metric3Label') },
])

let view: HubView | null = null
let frameId = 0
let resizeObserver: ResizeObserver | null = null

function onPointerMove(event: PointerEvent) {
  const box = visualRef.value?.getBoundingClientRect()
  if (!box || box.width < 1 || box.height < 1) return
  const x = ((event.clientX - box.left) / box.width) * 2 - 1
  const y = ((event.clientY - box.top) / box.height) * 2 - 1
  view?.setPointer(
    Math.min(1, Math.max(-1, x)),
    Math.min(1, Math.max(-1, y)),
  )
}

function onWheel(event: WheelEvent) {
  const box = visualRef.value?.getBoundingClientRect()
  if (!box) return
  const inside = event.clientX >= box.left && event.clientX <= box.right
    && event.clientY >= box.top && event.clientY <= box.bottom
  if (!inside) return
  event.preventDefault()
  view?.addDistance(event.deltaY * 0.004)
}

function resize() {
  const host = visualRef.value
  if (!host) return
  view?.resize(host.clientWidth, host.clientHeight)
}

function frame(now: number) {
  frameId = window.requestAnimationFrame(frame)
  view?.render(now)
  if (view) {
    labels.value = view.getLabels().map((label) => ({ ...label }))
  }
}

onMounted(() => {
  const canvas = canvasRef.value
  if (!canvas) return
  view = createHubView(canvas)
  if (!view) {
    webglError.value = true
    return
  }
  resize()
  resizeObserver = new ResizeObserver(() => resize())
  if (visualRef.value) resizeObserver.observe(visualRef.value)
  window.addEventListener('resize', resize)
  window.addEventListener('pointermove', onPointerMove)
  window.addEventListener('wheel', onWheel, { passive: false })
  frameId = window.requestAnimationFrame(frame)
})

onBeforeUnmount(() => {
  window.cancelAnimationFrame(frameId)
  window.removeEventListener('resize', resize)
  window.removeEventListener('pointermove', onPointerMove)
  window.removeEventListener('wheel', onWheel)
  resizeObserver?.disconnect()
  resizeObserver = null
  view?.dispose()
  view = null
})
</script>

<style scoped>
.hub-trial-arrow {
  animation: hub-trial-arrow-nudge 0.9s ease-in-out infinite;
}

@keyframes hub-trial-arrow-nudge {
  0%,
  100% {
    transform: translateX(0);
  }
  50% {
    transform: translateX(5px);
  }
}

/* fixed 铺满视口，不受外层文档流高度影响 */
.hub-page {
  position: fixed;
  inset: 0;
  z-index: 0;
  display: grid;
  grid-template-rows: auto 1fr auto;
  width: 100%;
  height: 100%;
  overflow: hidden;
  background: #000;
}

.hub-starfield {
  position: absolute;
  inset: 0;
  z-index: 0;
  pointer-events: none;
  overflow: hidden;
}

.hub-starfield-layer {
  position: absolute;
  top: 0;
  left: 0;
  width: 3.5px;
  height: 3.5px;
  border-radius: 50%;
  background: transparent;
}

.hub-starfield-layer--a {
  animation: hub-star-twinkle 2.6s ease-in-out infinite;
}

.hub-starfield-layer--b {
  animation: hub-star-twinkle 3.8s ease-in-out infinite reverse;
}

@keyframes hub-star-twinkle {
  0%,
  100% {
    opacity: 0.28;
  }
  50% {
    opacity: 1;
  }
}

.hub-header {
  position: relative;
  z-index: 20;
}

.hub-nav {
  display: flex;
  align-items: center;
  gap: 28px;
  width: 100%;
  max-width: 1440px;
  margin: 0 auto;
  padding: 14px 32px;
  box-sizing: border-box;
}

.hub-nav-brand {
  display: flex;
  min-width: 0;
  flex-shrink: 0;
  align-items: center;
  gap: 10px;
  font-size: 0.875rem;
  font-weight: 600;
  color: rgb(255 255 255 / 0.92);
  text-decoration: none;
}

.hub-nav-links {
  display: none;
  align-items: center;
  gap: 22px;
}

@media (min-width: 768px) {
  .hub-nav-links {
    display: flex;
  }
}

.hub-nav-link {
  font-size: 0.875rem;
  font-weight: 400;
  line-height: 1;
  color: rgb(255 255 255 / 0.52);
  text-decoration: none;
  transition: color 0.15s ease;
}

.hub-nav-link:hover {
  color: rgb(255 255 255 / 0.88);
}

.hub-nav-link--active {
  color: rgb(255 255 255 / 0.92);
}

.hub-nav-spacer {
  flex: 1 1 auto;
  min-width: 12px;
}

.hub-header-divider {
  display: none;
  width: 1px;
  height: 18px;
  flex-shrink: 0;
  background: rgb(255 255 255 / 0.14);
}

@media (min-width: 768px) {
  .hub-header-divider {
    display: block;
  }
}

.hub-header-tools {
  display: flex;
  flex-shrink: 0;
  align-items: center;
  gap: 4px;
}

.hub-header-icon-btn {
  display: inline-flex;
  height: 36px;
  width: 36px;
  flex-shrink: 0;
  align-items: center;
  justify-content: center;
  border-radius: 8px;
  color: rgb(255 255 255 / 0.72);
  transition: background-color 0.15s ease, color 0.15s ease;
}

.hub-header-icon-btn:hover {
  background: rgb(255 255 255 / 0.1);
  color: rgb(255 255 255 / 1);
}

/* 与 36px 图标按钮同高、同圆角 */
.hub-header-tools :deep(.relative > button) {
  display: inline-flex;
  height: 36px;
  width: 36px;
  min-width: 36px;
  align-items: center;
  justify-content: center;
  gap: 0;
  border-radius: 8px;
  padding: 0;
  font-size: 0.875rem;
  font-weight: 500;
  line-height: 1;
  color: rgb(255 255 255 / 0.72);
  background: transparent;
  transition: background-color 0.15s ease, color 0.15s ease;
}

.hub-header-tools :deep(.relative > button:hover) {
  background: rgb(255 255 255 / 0.1);
  color: rgb(255 255 255 / 1);
}

.hub-header-tools :deep(.relative > button .text-gray-400) {
  color: rgb(255 255 255 / 0.45);
}

.hub-header-tools :deep(.hub-announcement-bell > button) {
  display: inline-flex;
  height: 36px;
  width: 36px;
  align-items: center;
  justify-content: center;
  border-radius: 8px;
  color: rgb(255 255 255 / 0.72);
  background: transparent;
  transition: background-color 0.15s ease, color 0.15s ease;
}

.hub-header-tools :deep(.hub-announcement-bell > button:hover) {
  background: rgb(255 255 255 / 0.1);
  color: rgb(255 255 255 / 1);
  transform: none;
}

.hub-header-tools :deep(.hub-announcement-bell > button.text-blue-600),
.hub-header-tools :deep(.hub-announcement-bell > button.dark\:text-blue-400) {
  color: rgb(147 197 253);
}

.hub-header-login {
  display: inline-flex;
  height: 24px;
  flex-shrink: 0;
  align-items: center;
  justify-content: center;
  border-radius: 9999px;
  margin-left: 6px;
  padding: 0 10px;
  font-size: 0.75rem;
  font-weight: 500;
  line-height: 1;
  color: #000;
  background: #fff;
  text-decoration: none;
  transition: background-color 0.15s ease;
}

.hub-header-login:hover {
  background: rgb(255 255 255 / 0.88);
}

/* 中间 1fr：整块略居中，左文案靠近中缝，右 3D 仍占右侧 */
.hub-body {
  position: relative;
  z-index: 1;
  min-height: 0;
  width: min(1520px, calc(100% - 5vw));
  margin: 0 auto;
  display: grid;
  grid-template-columns: minmax(300px, 500px) minmax(0, 1fr);
  align-items: center;
  column-gap: clamp(20px, 2.5vw, 48px);
  padding-left: clamp(48px, 11vw, 160px);
  padding-right: clamp(12px, 1.5vw, 28px);
  box-sizing: border-box;
}

@media (min-width: 1024px) {
  .hub-body {
    padding-left: clamp(72px, 13vw, 200px);
  }
}

.hub-copy {
  width: 100%;
  text-align: left;
}

.hub-visual {
  position: relative;
  width: 100%;
  height: min(820px, 78vh);
  min-height: 520px;
  overflow: hidden;
}

.hub-node-label {
  position: absolute;
  z-index: 6;
  display: inline-flex;
  align-items: center;
  gap: 5px;
  font-family: 'Space Grotesk', 'Inter', 'SF Pro Display', 'PingFang SC', 'Microsoft YaHei', sans-serif;
  font-size: 11px;
  font-weight: 400;
  letter-spacing: 0.02em;
  line-height: 1;
  white-space: nowrap;
}

.hub-node-dot {
  width: 6px;
  height: 6px;
  flex-shrink: 0;
  border-radius: 9999px;
}

.hub-canvas {
  position: absolute;
  inset: 0;
  display: block;
  width: 100%;
  height: 100%;
}

.hub-legend {
  position: relative;
  z-index: 20;
  display: flex;
  justify-content: center;
  padding: 14px 24px 26px;
}

.hub-legend-list {
  display: flex;
  max-width: min(960px, 100%);
  flex-wrap: wrap;
  align-items: center;
  justify-content: center;
  gap: 18px 26px;
  font-family: 'Space Grotesk', 'Inter', 'SF Pro Display', 'PingFang SC', 'Microsoft YaHei', sans-serif;
  font-size: 15px;
  font-weight: 400;
  letter-spacing: 0.02em;
  line-height: 1;
}

.hub-legend-item {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  white-space: nowrap;
}

.hub-legend-item--muted {
  color: rgb(255 255 255 / 0.45);
}

.hub-legend-dot {
  width: 10px;
  height: 10px;
  flex-shrink: 0;
  border-radius: 9999px;
}

@media (max-width: 1023px) {
  .hub-body {
    grid-template-columns: 1fr;
    row-gap: 1.25rem;
    align-content: center;
    overflow: auto;
    padding: 0.5rem 5vw;
  }

  .hub-visual {
    height: min(420px, 46vh);
    min-height: 280px;
  }
}
</style>

<template>
  <div class="synapse-page">
    <nav class="synapse-nav" aria-label="Main navigation">
      <div class="synapse-menu synapse-menu-links">
        <router-link :to="consolePath">{{ t('home.dashboard') }}</router-link>
        <router-link v-if="showModelPlazaEntry" to="/model-plaza">{{ t('nav.modelPlaza') }}</router-link>
        <router-link v-if="showModelPlazaEntry" to="/model-plaza">{{ t('home.hub.pricing') }}</router-link>
        <a v-if="docUrl" :href="docUrl" target="_blank" rel="noopener noreferrer">{{ t('home.hub.guide') }}</a>
        <LocaleSwitcher text-only class="synapse-menu-control" />
        <AnnouncementBell v-if="isAuthenticated" text-only class="synapse-menu-control" />
        <router-link v-else to="/login">{{ t('nav.messages') }}</router-link>
      </div>
      <router-link :to="consolePath" class="synapse-login">
        {{ isAuthenticated ? t('home.dashboard') : t('home.login') }}
      </router-link>
    </nav>

    <main>
      <section id="top" ref="heroRef" class="synapse-hero">
        <video ref="heroVideoRef" :src="videoHero" poster="/synapse-hero-poster.jpg" class="synapse-video synapse-hero-video" :class="{ 'is-ready': heroFrameReady }" muted playsinline preload="auto" aria-hidden="true" tabindex="-1" />
        <div class="synapse-hero-shade" aria-hidden="true" />
        <div class="synapse-dot-grid" aria-hidden="true" />
        <div class="synapse-watermark" aria-hidden="true">TRANSCENDENCE</div>
        <div class="synapse-hero-content">
          <div class="synapse-hero-bottom">
            <div class="synapse-hero-copy">
              <h1>
                <span class="synapse-line">{{ t('home.hub.titleLine1') }}</span>
                <span class="synapse-line">{{ t('home.hub.titleLine2') }}</span>
                <span class="synapse-line">{{ t('home.hub.titleLine3') }}</span>
              </h1>
              <p>{{ t('home.hub.lead') }}</p>
              <p>{{ t('home.hub.detail') }}</p>
              <div class="synapse-hero-actions">
                <router-link to="/keys" class="synapse-trial-link">
                  {{ t('home.hub.recharge') }}
                  <Icon name="arrowRight" size="sm" :stroke-width="2" aria-hidden="true" />
                </router-link>
              </div>
            </div>
            <h2 class="synapse-hero-right"><span class="synapse-line">CodeBot</span></h2>
          </div>
        </div>
      </section>

      <section id="metrics" class="synapse-metrics">
        <video :src="videoMetrics" class="synapse-video" autoplay muted loop playsinline preload="metadata" aria-hidden="true" tabindex="-1" />
        <div class="synapse-metrics-shade" aria-hidden="true" />
        <div class="synapse-metrics-content">
          <p class="synapse-section-label">Supported AI Platforms</p>
          <div class="synapse-platform-grid">
            <div v-for="platform in platforms" :key="platform.name" class="synapse-platform">
              <span class="synapse-platform-icon" :class="`synapse-platform-icon--${platform.name.toLowerCase()}`" aria-hidden="true">
                <PlatformIcon :platform="platform.icon" size="lg" />
              </span>
              <span class="synapse-platform-name">{{ platform.name }}</span>
            </div>
          </div>
        </div>
        <p class="synapse-footer-note">CodeBot 始终全力以赴，力求为您带来更优质的服务体验。</p>
      </section>
    </main>

  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore, useAuthStore } from '@/stores'
import { sanitizeUrl } from '@/utils/url'
import { FeatureFlags, isFeatureFlagEnabled } from '@/utils/featureFlags'
import PlatformIcon from '@/components/common/PlatformIcon.vue'
import LocaleSwitcher from '@/components/common/LocaleSwitcher.vue'
import AnnouncementBell from '@/components/common/AnnouncementBell.vue'
import Icon from '@/components/icons/Icon.vue'
import type { GroupPlatform } from '@/types'

const videoHero = 'https://d8j0ntlcm91z4.cloudfront.net/user_38xzZboKViGWJOttwIXH07lWA1P/hf_20260622_083515_290e5a10-0b95-41af-a5e2-32b6389baa4d.mp4'
const videoMetrics = 'https://d8j0ntlcm91z4.cloudfront.net/user_38xzZboKViGWJOttwIXH07lWA1P/hf_20260622_095810_ecea3dd2-fc5e-4e41-8696-4219290b6589.mp4'
const platforms: { name: string; icon: GroupPlatform }[] = [
  { name: 'Codex', icon: 'openai' },
  { name: 'Claude', icon: 'anthropic' },
  { name: 'Gemini', icon: 'gemini' },
  { name: 'DeepSeek', icon: 'deepseek' },
]

const { t } = useI18n()
const appStore = useAppStore()
const authStore = useAuthStore()
const isAuthenticated = computed(() => authStore.isAuthenticated)
const consolePath = computed(() => !isAuthenticated.value ? '/login' : authStore.isAdmin ? '/admin/dashboard' : '/dashboard')
const showModelPlazaEntry = computed(() =>
  (!appStore.publicSettingsLoaded || isFeatureFlagEnabled(FeatureFlags.modelPlaza))
  && (isAuthenticated.value || appStore.cachedPublicSettings?.model_plaza_require_auth !== true),
)
const docUrl = computed(() =>
  sanitizeUrl(appStore.cachedPublicSettings?.doc_url || appStore.docUrl || '')
  || (!appStore.publicSettingsLoaded ? 'https://github.com/Wei-Shaw/sub2api/tree/main/docs' : ''),
)

const heroRef = ref<HTMLElement | null>(null)
const heroVideoRef = ref<HTMLVideoElement | null>(null)
const heroFrameReady = ref(false)
let lastX: number | null = null
let targetTime = 0
let seekPending = false
const heroStartTime = 1.75
const heroMinTime = 0.25

function onVideoReady() {
  const video = heroVideoRef.value
  if (!video || heroFrameReady.value || seekPending || !Number.isFinite(video.duration) || video.duration <= 0) return
  video.pause()
  targetTime = Math.min(video.duration - 0.001, heroStartTime)
  seekPending = true
  video.currentTime = targetTime
}

function flushSeek() {
  const video = heroVideoRef.value
  if (!video || !Number.isFinite(video.duration) || video.duration <= 0 || seekPending) return
  if (Math.abs(video.currentTime - targetTime) < 0.012) return
  seekPending = true
  video.currentTime = Math.max(0, Math.min(video.duration - 0.001, targetTime))
}

function onVideoSeeked() {
  seekPending = false
  heroFrameReady.value = true
  flushSeek()
}

function onMouseMove(event: MouseEvent) {
  const hero = heroRef.value
  const video = heroVideoRef.value
  if (!hero || !video || hero.getBoundingClientRect().bottom <= 0) {
    lastX = null
    return
  }
  if (lastX === null) {
    lastX = event.clientX
    return
  }
  const deltaX = event.clientX - lastX
  lastX = event.clientX
  if (!Number.isFinite(video.duration) || video.duration <= 0) return
  targetTime = Math.max(Math.min(video.duration - 0.001, heroMinTime), Math.min(video.duration - 0.001, targetTime + (deltaX / window.innerWidth) * video.duration * 0.8))
  flushSeek()
}

onMounted(() => {
  authStore.checkAuth()
  if (!appStore.publicSettingsLoaded) void appStore.fetchPublicSettings()
  const video = heroVideoRef.value
  video?.addEventListener('loadedmetadata', onVideoReady)
  video?.addEventListener('seeked', onVideoSeeked)
  if (video && video.readyState >= HTMLMediaElement.HAVE_METADATA) onVideoReady()
  window.addEventListener('mousemove', onMouseMove)
})

onBeforeUnmount(() => {
  heroVideoRef.value?.removeEventListener('loadedmetadata', onVideoReady)
  heroVideoRef.value?.removeEventListener('seeked', onVideoSeeked)
  window.removeEventListener('mousemove', onMouseMove)
})
</script>

<style scoped>
@import url('https://fonts.googleapis.com/css2?family=Anton+SC&family=Space+Mono:wght@400;700&display=swap');

.synapse-page { min-height: 100vh; overflow-x: hidden; background: #000; color: #fff; font-family: 'Space Mono', monospace; }
.synapse-page *, .synapse-page *::before, .synapse-page *::after { box-sizing: border-box; }
.synapse-page a { text-decoration: none; }
.synapse-page a:focus-visible, .synapse-page button:focus-visible { outline: 2px solid white; outline-offset: 3px; }
.synapse-nav { position: fixed; z-index: 100; inset: 0 0 auto; display: flex; align-items: center; justify-content: space-between; height: 80px; padding: 0 32px; animation: synapse-fade 0.8s 0.8s both; }
.synapse-menu { display: inline-flex; min-width: 0; height: 48px; align-items: center; border-radius: 14px; background: rgb(255 255 255 / 0.15); color: #fff; backdrop-filter: blur(12px); }
.synapse-menu-links { gap: 26px; padding: 0 20px; white-space: nowrap; }
.synapse-menu-links a, .synapse-menu-links :deep(.synapse-menu-control > button) { color: rgb(255 255 255 / .85); font-size: 14px; }
.synapse-menu-links a:hover, .synapse-menu-links :deep(.synapse-menu-control > button:hover) { color: #fff; }
.synapse-login { display: inline-flex; height: 48px; flex-shrink: 0; align-items: center; justify-content: center; border-radius: 9999px; background: #fff; padding: 0 20px; color: #111; font-size: 14px; font-weight: 700; transition: transform .2s, background .2s; }
.synapse-login:hover { transform: scale(1.03); background: #e2e2e6; }
.synapse-hero { position: relative; height: 100vh; height: 100dvh; min-height: 480px; overflow: hidden; background: #000 url('/synapse-hero-poster.jpg') center / cover no-repeat; }
.synapse-video { position: absolute; inset: 0; width: 100%; height: 100%; object-fit: cover; }
.synapse-hero-video { opacity: 0; transition: opacity .3s ease; }
.synapse-hero-video.is-ready { opacity: 1; }
.synapse-hero-shade, .synapse-metrics-shade, .synapse-dot-grid { position: absolute; inset: 0; pointer-events: none; }
.synapse-hero-shade { background: rgb(0 0 0 / .25); }
.synapse-dot-grid { opacity: .05; background-image: radial-gradient(#fff 1px, transparent 1px); background-size: 24px 24px; }
.synapse-watermark { position: absolute; top: calc(50% + 50px); left: 50%; transform: translate(-50%, -50%); white-space: nowrap; font-family: 'Anton SC', sans-serif; font-size: clamp(120px, 30vw, 521px); letter-spacing: -4px; opacity: .1; background: radial-gradient(circle, rgb(142 127 148 / 0) 0%, #8e7f94 70%); background-clip: text; -webkit-text-fill-color: transparent; pointer-events: none; }
.synapse-hero-content { position: relative; z-index: 2; display: flex; height: 100%; flex-direction: column; justify-content: flex-end; padding: 96px 32px 48px; animation: synapse-fade 1s .8s both; }
.synapse-hero-bottom { display: flex; align-items: flex-end; justify-content: space-between; gap: 30px; }
.synapse-hero-copy { display: flex; min-width: 0; max-width: min(60%, 700px); flex-direction: column; gap: 16px; }
.synapse-hero h1, .synapse-hero h2 { margin: 0; color: #fff; font-size: clamp(40px, 10vw, 100px); font-weight: 400; line-height: .95; letter-spacing: -.03em; }
.synapse-hero-copy h1 { font-family: Inter, system-ui, sans-serif; font-size: clamp(32px, 4.3vw, 64px); font-weight: 700; line-height: 1.2; letter-spacing: 0; }
.synapse-line { display: block; }
.synapse-hero-copy p { max-width: 620px; margin: 0; color: rgb(255 255 255 / .75); font-family: Inter, system-ui, sans-serif; font-size: 15px; line-height: 1.65; }
.synapse-hero-copy p + p { color: rgb(255 255 255 / .55); font-size: 13px; }
.synapse-hero-actions { margin-top: 8px; font-family: Inter, system-ui, sans-serif; }
.synapse-trial-link { display: inline-flex; min-height: 54px; align-items: center; justify-content: center; gap: 18px; border-radius: 9999px; background: #fff; padding: 0 28px; color: #111; font-size: 16px; font-weight: 700; transition: background .2s, transform .2s; }
.synapse-trial-link:hover { background: #e9e9ed; transform: translateY(-2px); }
.synapse-hero-right { flex-shrink: 0; text-align: right; }
.synapse-metrics { position: relative; display: flex; min-height: 100vh; min-height: 100dvh; align-items: center; justify-content: center; overflow: hidden; background: #000; padding: 128px 24px; }
.synapse-metrics-shade { background: rgb(0 0 0 / .3); }
.synapse-metrics-content { position: relative; z-index: 1; width: min(100%, 1152px); }
.synapse-section-label { margin: 0 0 80px; color: rgb(255 255 255 / .7); font-size: 14px; letter-spacing: .2em; text-align: center; text-shadow: 0 2px 16px rgb(0 0 0 / .8); text-transform: uppercase; }
.synapse-platform-grid { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 16px; }
.synapse-platform { display: flex; min-width: 0; align-items: center; justify-content: center; gap: 14px; color: #fff; }
.synapse-platform-icon { display: inline-flex; flex: 0 0 auto; color: #a9bcd5; }
.synapse-platform-icon :deep(svg) { width: clamp(40px, 3.3vw, 48px); height: clamp(40px, 3.3vw, 48px); }
.synapse-platform-icon--claude { color: #d88c62; }
.synapse-platform-icon--gemini { color: #7aa6f2; }
.synapse-platform-icon--deepseek { color: #5f8dff; }
.synapse-platform-name { font-family: Inter, system-ui, sans-serif; font-size: clamp(32px, 3vw, 44px); font-weight: 500; line-height: 1.1; text-shadow: 0 2px 18px rgb(0 0 0 / .6); }
.synapse-footer-note { position: absolute; z-index: 1; right: 24px; bottom: 24px; left: 24px; margin: 0; color: rgb(255 255 255 / .65); font-family: Inter, system-ui, sans-serif; font-size: 12px; line-height: 1.6; text-align: center; text-shadow: 0 2px 12px rgb(0 0 0 / .8); }
@keyframes synapse-fade { from { opacity: 0; transform: translateY(16px); } to { opacity: 1; transform: translateY(0); } }
@media (max-width: 1199px) and (min-width: 768px) { .synapse-platform-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 52px 24px; } }
@media (max-width: 767px) {
  .synapse-nav { padding: 0 20px; }
  .synapse-hero-content { padding: 80px 20px 32px; }
  .synapse-hero-bottom { flex-direction: column; align-items: stretch; gap: 26px; }
  .synapse-hero-copy { max-width: 100%; }
  .synapse-hero-right { text-align: left; }
  .synapse-hero-copy p { font-size: 13px; }
  .synapse-trial-link { min-height: 48px; padding: 0 24px; font-size: 14px; }
  .synapse-platform-grid { grid-template-columns: 1fr; gap: 36px; }
  .synapse-section-label { margin-bottom: 56px; }
  .synapse-footer-note { bottom: 20px; font-size: 11px; }
}
@media (max-width: 639px) {
  .synapse-nav { height: 64px; padding: 0 16px; }
  .synapse-menu { height: 36px; border-radius: 10px; }
  .synapse-menu-links { gap: 12px; padding: 0 12px; }
  .synapse-menu-links a, .synapse-menu-links :deep(.synapse-menu-control > button) { font-size: 11px; }
  .synapse-login { height: 36px; padding: 0 14px; font-size: 12px; }
  .synapse-hero-shade { background: linear-gradient(to top, rgb(0 0 0 / .75), rgb(0 0 0 / .35) 55%, rgb(0 0 0 / .15)); }
  .synapse-hero h1, .synapse-hero h2 { font-size: clamp(40px, 10vw, 68px); }
  .synapse-hero-copy p { max-width: 100%; }
}
@media (max-width: 559px) { .synapse-menu-links { display: grid; height: auto; min-height: 52px; grid-template-columns: repeat(2, max-content); gap: 4px 10px; padding: 6px 10px; } }
@media (max-width: 359px) { .synapse-nav { padding: 0 8px; } .synapse-menu-links { gap: 3px 4px; padding: 5px 4px; } .synapse-menu-links a, .synapse-menu-links :deep(.synapse-menu-control > button) { font-size: 10px; } .synapse-login { padding: 0 8px; font-size: 11px; } }
@media (max-width: 440px) { .synapse-hero { height: auto; min-height: 100dvh; } .synapse-hero-content { min-height: 100dvh; } }
@media (max-height: 500px) and (min-width: 440px) { .synapse-hero-bottom { flex-direction: row; } .synapse-hero h1, .synapse-hero h2 { font-size: clamp(38px, 6vw, 52px); } .synapse-hero-copy p { font-size: 11px; } }
@media (prefers-reduced-motion: reduce) { .synapse-nav, .synapse-hero-content { animation: none; } .synapse-login { transition: none; } }
</style>

<template>
  <AppLayout>
    <div class="space-y-6 text-gray-900 dark:text-gray-100">
      <header class="space-y-2">
        <p v-if="!userMode" class="text-sm font-medium text-gray-500">{{ t('modelIdentification.management') }}</p>
        <h1 class="text-2xl font-bold">{{ t('modelBenchmark.title') }}</h1>
        <p v-if="!userMode" class="text-sm text-gray-500 dark:text-gray-400">{{ t('modelBenchmark.subtitle') }}</p>
      </header>
      <IdentificationTabs active="dilution" :admin="!userMode" />
      <p v-if="userMode" class="text-sm text-gray-500 dark:text-gray-400" data-testid="benchmark-reference-notice">{{ t('modelBenchmark.referenceTitle') }}</p>
      <div v-if="!userMode" class="flex flex-wrap gap-2" role="tablist" :aria-label="t('modelBenchmark.title')">
        <button type="button" role="tab" :aria-selected="activeTab === 'tests'" class="btn" :class="activeTab === 'tests' ? 'btn-primary' : 'btn-secondary'" @click="activeTab = 'tests'">{{ t('modelBenchmark.testManagement') }}</button>
        <button type="button" role="tab" :aria-selected="activeTab === 'display'" class="btn" :class="activeTab === 'display' ? 'btn-primary' : 'btn-secondary'" @click="activeTab = 'display'">{{ t('modelBenchmark.displaySettings') }}</button>
        <button type="button" role="tab" :aria-selected="activeTab === 'users'" class="btn" :class="activeTab === 'users' ? 'btn-primary' : 'btn-secondary'" @click="activeTab = 'users'">{{ t('modelBenchmark.userRecords') }}</button>
      </div>
      <BenchmarkDisplaySettings v-if="!userMode && activeTab === 'display'" />
      <BenchmarkUserRecords v-if="!userMode && activeTab === 'users'" />
      <div v-show="userMode || activeTab === 'tests'" class="space-y-6">
      <div v-if="loadError" role="alert" class="rounded-xl bg-red-50 p-4 text-sm text-red-700 dark:bg-red-950/30 dark:text-red-300">
        {{ loadError }} <button class="ml-3 underline" :disabled="loading" @click="reload()">{{ t('modelBenchmark.retry') }}</button>
      </div>
      <p v-if="loading && !state" role="status">{{ t('modelBenchmark.loading') }}</p>
      <section v-if="state?.resources && (!userMode || state.resources.blocked)" role="status" class="space-y-1 rounded-xl border border-amber-200 bg-amber-50 p-4 text-sm text-amber-900 dark:border-amber-900 dark:bg-amber-950/20 dark:text-amber-200" data-testid="resource-status">
        <p class="font-semibold">{{ t(state.resources.blocked ? 'modelBenchmark.resourcePaused' : 'modelBenchmark.resourceProtection') }}</p>
        <p v-if="state.resources.message">{{ state.resources.message }}</p>
        <p v-if="!userMode && state.resources.memory_percent != null" class="text-xs">{{ t('modelBenchmark.resourceMetrics', { memory: state.resources.memory_percent, disk: state.resources.disk_free_gb }) }}</p>
        <p v-if="state.resources.blocked" class="text-xs">{{ t('modelBenchmark.resourcePauseHint') }}</p>
      </section>
      <section v-if="state && !state.ready" role="status" class="space-y-2 rounded-xl bg-amber-50 p-4 text-sm text-amber-800 dark:bg-amber-950/30 dark:text-amber-200" data-testid="environment-status">
        <p class="font-semibold">{{ t('modelBenchmark.environmentPreparing') }}</p>
        <p>{{ state.message }}</p>
        <template v-if="state.environment">
          <progress :value="state.environment.progress" max="100" class="h-2 w-full accent-primary-600" :aria-label="t('modelBenchmark.environmentProgress')" />
          <p v-if="state.environment.next_retry_at" class="text-xs">{{ t('modelBenchmark.environmentNextRetry', { time: formatDate(state.environment.next_retry_at) }) }}</p>
          <button v-if="!userMode && state.environment.status === 'failed'" type="button" class="btn btn-secondary" :disabled="!!busy" data-testid="environment-retry" @click="retryEnvironment">{{ t('modelBenchmark.environmentRetry') }}</button>
        </template>
      </section>

      <section v-if="userMode" class="space-y-5 rounded-2xl border border-gray-200 bg-white p-4 dark:border-dark-700 dark:bg-dark-900 sm:p-6" :aria-label="t('modelBenchmark.showcase')" data-testid="site-results-section">
        <div class="flex flex-wrap items-center justify-between gap-3 border-b border-gray-100 pb-4 dark:border-dark-800"><div class="flex items-center gap-3"><span class="flex h-9 w-9 items-center justify-center rounded-lg bg-gray-100 text-xs font-semibold tracking-wider text-gray-500 dark:bg-dark-800">01</span><h2 class="text-lg font-semibold">{{ t('modelBenchmark.showcase') }}</h2></div><button class="btn btn-secondary" :disabled="loading || !!busy" @click="reload()">{{ t('modelBenchmark.refreshResults') }}</button></div>
        <p v-if="!sharedDisplays.length && !loading" class="py-5 text-center text-sm text-gray-500">{{ t('modelBenchmark.noSharedResults') }}</p>
        <div v-for="display in sharedDisplays" :key="display.id" class="space-y-3" :data-display-id="display.id">
          <div class="flex flex-wrap items-center justify-between gap-2"><h3 class="font-semibold">{{ display.name }}</h3><span class="text-xs text-gray-500">{{ t(display.source_type === 'local' ? 'modelBenchmark.localSource' : 'modelBenchmark.remoteSource') }}<span v-if="display.last_success_at"> · {{ t('modelBenchmark.lastFetch', { time: formatDate(display.last_success_at) }) }}</span></span></div>
          <p v-if="display.stale" class="text-xs text-amber-700" role="status">{{ t('modelBenchmark.sourceStale') }}</p>
          <p v-else-if="display.pending" class="text-sm text-gray-500">{{ t('modelBenchmark.fetchingSource') }}</p>
          <p v-else-if="!display.items.length" class="text-sm text-gray-500">{{ t('modelBenchmark.noSourceResults') }}</p>
        <BenchmarkResultGallery :items="display.items" :display-name="display.name" />
        </div>
        <p class="text-xs text-gray-500">{{ t('modelBenchmark.passRateHint') }}</p>
      </section>
      <BenchmarkBankSettings v-if="!userMode" :state="state" :busy="!!busy" @update="updateBank" @activate="activateBank" />
      <section v-if="canUsePersonalTests" :class="userMode ? 'space-y-5 rounded-2xl border border-primary-200 bg-primary-50/40 p-4 dark:border-primary-900/60 dark:bg-primary-950/10 sm:p-6' : 'space-y-6'" :aria-label="userMode ? t('modelBenchmark.myTests') : undefined" :data-testid="userMode ? 'my-tests-section' : undefined">
      <div v-if="userMode" class="flex flex-wrap items-center justify-between gap-3 border-b border-primary-100 pb-4 dark:border-primary-900/50"><div class="flex items-center gap-3"><span class="flex h-9 w-9 items-center justify-center rounded-lg bg-primary-100 text-xs font-semibold tracking-wider text-primary-700 dark:bg-primary-900/40 dark:text-primary-300">02</span><h2 class="text-lg font-semibold">{{ t('modelBenchmark.myTests') }}</h2></div><span v-if="state?.quota" class="rounded-full bg-white px-3 py-1 text-xs text-primary-700 dark:bg-dark-900 dark:text-primary-300">{{ t('modelBenchmark.quotaRemaining', { remaining: state.quota.remaining, limit: state.quota.limit }) }}</span></div>
      <div :class="userMode ? 'space-y-3' : 'grid gap-6 xl:grid-cols-2'">
        <section v-if="userMode" class="flex flex-wrap items-center justify-between gap-x-6 gap-y-2 px-1 py-1 text-sm" :aria-label="t('modelBenchmark.suite')">
          <div class="flex flex-wrap items-center gap-x-3 gap-y-1">
            <h2 class="font-semibold">{{ t('modelBenchmark.suite') }}</h2>
            <span v-if="state?.suite" class="text-xs text-gray-500 dark:text-gray-400" :title="state.suite.fingerprint">{{ state.suite.day }} · v{{ state.suite.revision }}</span>
            <span class="text-xs text-primary-600 dark:text-primary-400">{{ t('modelBenchmark.total', { count: countTotal }) }}</span>
          </div>
          <div class="flex flex-wrap gap-x-4 gap-y-1 text-xs text-gray-500 dark:text-gray-400">
            <span v-for="category in categories" :key="category">{{ t(`modelBenchmark.categories.${category}`) }} <strong class="font-medium text-gray-900 dark:text-gray-100">{{ counts[category] }}</strong></span>
            <span v-for="difficulty in codingDifficulties" :key="difficulty">{{ t(`modelBenchmark.difficulties.${difficulty}`) }} {{ codingCounts[difficulty] }}</span>
          </div>
        </section>
        <section class="benchmark-card space-y-4" :aria-label="t('modelBenchmark.form')">
          <h2 class="text-lg font-semibold">{{ t('modelBenchmark.form') }}</h2>
          <div v-if="userMode" class="space-y-1 rounded-lg bg-primary-50 p-3 text-sm dark:bg-primary-900/20" role="status">
            <p>{{ t('modelBenchmark.quotaHint', { concurrency: state?.execution?.max_concurrency ?? 5 }) }}</p>
            <p v-if="state?.quota?.remaining === 0 && state.quota.next_available_at" class="text-xs">{{ t('modelBenchmark.quotaReset', { time: formatDate(state.quota.next_available_at) }) }}</p>
            <p v-if="userHasActive" class="text-sm font-medium text-amber-800 dark:text-amber-300" data-testid="active-test-notice">{{ t('modelBenchmark.activeTestHint') }} <button v-if="state?.active_run" type="button" class="underline" :disabled="!!busy" @click="showDetail(state.active_run.id)">{{ t('modelBenchmark.details') }}</button></p>
          </div>
          <form :class="userMode ? 'grid gap-4 md:grid-cols-2' : 'space-y-4'" @submit.prevent="submitTarget">
            <div class="space-y-1 text-sm">
              <div class="flex items-center justify-between gap-2"><label for="benchmark-api-url">{{ t('modelBenchmark.url') }}</label><button type="button" class="text-xs font-medium text-primary-600 hover:underline" :disabled="!!busy" @click="showKeyPicker = true">{{ t('modelBenchmark.selectVersion') }}</button></div>
              <input id="benchmark-api-url" v-model.trim="form.base_url" name="base_url" type="url" required maxlength="2048" class="input w-full" placeholder="https://api.example.com/v1" @input="clearSelectedKey" />
            </div>
            <label class="block space-y-1 text-sm">
              <span>{{ t('modelBenchmark.apiKey') }}</span>
              <input v-model="form.api_key" name="api_key" type="password" required maxlength="4096" autocomplete="new-password" spellcheck="false" class="input w-full" placeholder="sk-…" @input="selectedKeyName = ''" />
              <span class="block text-xs text-gray-500">{{ t('modelBenchmark.keyHint') }}</span>
              <span class="block text-xs text-amber-700 dark:text-amber-300">{{ t('modelBenchmark.keySafetyHint') }}</span>
            </label>
            <p v-if="selectedKeyName" class="text-xs text-primary-600" :class="{ 'md:col-span-2': userMode }">{{ t('modelBenchmark.selectedKey', { name: selectedKeyName }) }}</p>
            <div class="grid gap-4 sm:grid-cols-2" :class="{ 'md:col-span-2': userMode }">
              <label class="block space-y-1 text-sm">
                <span>{{ t('modelBenchmark.model') }}</span>
                <input v-model.trim="form.model" name="model" required maxlength="200" class="input w-full" placeholder="gpt-6.1-sol" />
              </label>
              <label class="block space-y-1 text-sm">
                <span>{{ t('modelBenchmark.effort') }}</span>
                <select v-model="form.effort" name="effort" class="input w-full">
                  <option v-for="effort in efforts" :key="effort" :value="effort">{{ t(`modelBenchmark.efforts.${effort}`) }}</option>
                </select>
              </label>
            </div>
            <p class="text-xs leading-5 text-gray-500 dark:text-gray-400" :class="{ 'md:col-span-2': userMode }">{{ t('modelBenchmark.protocol') }}</p>
            <label v-if="!userMode" class="flex items-center gap-2 text-sm"><input v-model="scheduledMode" name="scheduled_mode" type="checkbox" />{{ t('modelBenchmark.setSchedule') }}</label>
            <div v-if="!userMode && scheduledMode" class="rounded-lg bg-gray-50 p-3 dark:bg-dark-800">
              <BenchmarkScheduleFields v-model="scheduleForm" :timezone="state?.timezone || 'Asia/Shanghai'" />
              <p class="mt-3 text-xs leading-5 text-gray-500">{{ t('modelBenchmark.scheduleHint', { concurrency: state?.execution?.max_concurrency ?? 5 }) }}</p>
            </div>
            <div :class="{ 'flex justify-end border-t border-gray-100 pt-4 dark:border-dark-800 md:col-span-2': userMode }"><button type="submit" class="btn btn-primary" :disabled="!canStart || (scheduledMode && (!scheduleForm.name.trim() || !scheduleForm.cron_expression.trim()))">{{ t(busy === 'create' ? 'modelBenchmark.starting' : scheduledMode ? 'modelBenchmark.createSchedule' : 'modelBenchmark.start') }}</button></div>
          </form>

        </section>

        <section v-if="!userMode" class="benchmark-card space-y-4" :aria-label="t('modelBenchmark.suite')">
          <div class="flex flex-wrap items-center justify-between gap-2">
            <h2 class="text-lg font-semibold">{{ t('modelBenchmark.suite') }}</h2>
            <span class="rounded-full bg-primary-50 px-3 py-1 text-sm font-medium text-primary-700 dark:bg-primary-900/30 dark:text-primary-300">{{ t('modelBenchmark.total', { count: countTotal }) }}</span>
          </div>
          <p class="text-sm leading-6 text-gray-500 dark:text-gray-400">{{ t('modelBenchmark.suiteHint', { timezone: state?.timezone || 'Asia/Shanghai' }) }}</p>
          <div v-if="state?.suite" class="rounded-lg bg-gray-50 p-3 text-sm dark:bg-dark-800">
            <div>{{ t('modelBenchmark.suiteVersion') }}: <strong>{{ state.suite.day }} · v{{ state.suite.revision }}</strong></div>
            <code class="text-xs text-gray-500" :title="state.suite.fingerprint">{{ state.suite.fingerprint.slice(0, 16) }}</code>
          </div>
          <form class="space-y-4" @submit.prevent="changeSuite(true)">
            <fieldset :disabled="!state?.ready || !!busy">
              <legend class="mb-2 text-sm font-medium">{{ t('modelBenchmark.counts') }}</legend>
              <div class="grid grid-cols-3 gap-3">
                <label v-for="category in categories" :key="category" class="block space-y-1 text-sm">
                  <span>{{ t(`modelBenchmark.categories.${category}`) }}</span>
                  <input v-model.number="counts[category]" :name="category" :readonly="category === 'coding'" type="number" min="0" :max="state?.capacities[category] ?? 100" step="1" required class="input w-full" />
                  <span v-if="state" class="block text-xs text-gray-500">{{ t('modelBenchmark.capacity', { count: state.capacities[category] }) }}</span>
                </label>
              </div>
              <div class="mt-4 space-y-2">
                <p class="text-sm font-medium">{{ t('modelBenchmark.codingDifficulty') }}</p>
                <div class="grid grid-cols-2 gap-3">
                  <label v-for="difficulty in codingDifficulties" :key="difficulty" class="block space-y-1 text-sm">
                    <span>{{ t(`modelBenchmark.difficulties.${difficulty}`) }}</span>
                    <input v-model.number="codingCounts[difficulty]" :name="`coding_${difficulty}`" type="number" min="0" :max="state?.coding_capacities?.[difficulty] ?? 100" step="1" required class="input w-full" @input="updateCodingTotal" />
                    <span v-if="state?.coding_capacities" class="block text-xs text-gray-500">{{ t('modelBenchmark.capacity', { count: state.coding_capacities[difficulty] }) }}</span>
                  </label>
                </div>
                <p class="text-xs text-gray-500">{{ t('modelBenchmark.codingDifficultyHint') }}</p>
              </div>
            </fieldset>
            <p v-if="!validCounts" class="text-sm text-red-600">{{ t('modelBenchmark.countError') }}</p>
            <p v-else-if="dirtyCounts" class="text-sm text-amber-700 dark:text-amber-300">{{ t('modelBenchmark.unsaved') }} <button type="button" class="underline" @click="resetCounts">{{ t('modelBenchmark.reset') }}</button></p>
            <div class="flex flex-wrap gap-2">
              <button type="submit" class="btn btn-secondary" :disabled="!state?.ready || !!busy || !dirtyCounts || !validCounts">{{ t('modelBenchmark.saveCounts') }}</button>
              <button type="button" class="btn btn-secondary" :disabled="!state?.ready || !!busy || dirtyCounts" @click="changeSuite(false)">{{ t('modelBenchmark.refreshSuite') }}</button>
            </div>
          </form>
          <form v-if="!userMode" class="space-y-3 border-t border-gray-200 pt-4 dark:border-dark-700" @submit.prevent="saveConcurrency">
            <label class="block space-y-1 text-sm"><span class="font-medium">{{ t('modelBenchmark.maxConcurrency') }}</span><input v-model.number="maxConcurrency" name="max_concurrency" type="number" min="1" max="100" step="1" required class="input w-full" /></label>
            <p class="text-xs leading-5 text-gray-500">{{ t('modelBenchmark.concurrencyHint') }}</p>
            <p v-if="state?.execution" class="text-xs text-gray-500">{{ t('modelBenchmark.executionState', { running: state.execution.running, queued: state.execution.queued }) }}</p>
            <button type="submit" class="btn btn-secondary" :disabled="!!busy || !state?.execution || !concurrencyDirty || !validConcurrency">{{ t('common.save') }}</button>
          </form>
          <div class="space-y-2 border-t border-gray-200 pt-4 dark:border-dark-700">
            <div class="flex items-center justify-between gap-4">
              <span id="user-testing-label" class="text-sm font-medium">{{ t('modelBenchmark.userTestingEnabled') }}</span>
              <button type="button" role="switch" aria-labelledby="user-testing-label" :aria-checked="state?.execution?.user_testing_enabled === true" data-testid="user-testing-toggle" class="relative inline-flex h-6 w-11 shrink-0 rounded-full transition-colors disabled:opacity-40" :class="state?.execution?.user_testing_enabled ? 'bg-primary-600' : 'bg-gray-300 dark:bg-dark-600'" :disabled="!!busy || !state?.execution" @click="toggleUserTesting"><span class="mt-0.5 h-5 w-5 rounded-full bg-white shadow transition-transform" :class="state?.execution?.user_testing_enabled ? 'translate-x-5' : 'translate-x-0.5'" /></button>
            </div>
            <p class="text-xs leading-5 text-gray-500">{{ t('modelBenchmark.userTestingHint') }}</p>
          </div>
        </section>
      </div>

      <section v-if="!userMode" class="benchmark-card space-y-4" :aria-label="t('modelBenchmark.schedules')">
        <div class="flex flex-wrap items-center justify-between gap-3"><h2 class="text-lg font-semibold">{{ t('modelBenchmark.schedules') }}</h2><span class="text-xs text-gray-500">{{ state?.timezone || 'Asia/Shanghai' }}</span></div>
        <p class="text-xs leading-5 text-gray-500">{{ t('modelBenchmark.scheduleHint', { concurrency: state?.execution?.max_concurrency ?? 5 }) }}</p>
        <p class="text-xs leading-5 text-gray-500">{{ t('modelBenchmark.runScheduleHint') }}</p>
        <p v-if="!schedules.length" class="py-4 text-center text-sm text-gray-500">{{ t('modelBenchmark.noSchedules') }}</p>
        <div v-else class="overflow-x-auto">
          <table class="w-full text-left text-sm">
            <thead class="border-b border-gray-200 text-gray-500 dark:border-dark-700"><tr><th class="pb-3 pr-4">{{ t('modelBenchmark.scheduleName') }}</th><th class="pb-3 pr-4">{{ t('modelBenchmark.frequency') }}</th><th class="pb-3 pr-4">{{ t('modelBenchmark.nextRun') }}</th><th class="pb-3 pr-4">{{ t('modelBenchmark.lastRun') }}</th><th class="pb-3 pr-4">{{ t('modelBenchmark.publicResults') }}</th><th class="pb-3">{{ t('modelBenchmark.actions') }}</th></tr></thead>
            <tbody class="divide-y divide-gray-100 dark:divide-dark-800">
              <tr v-for="schedule in schedules" :key="schedule.id" :data-schedule-id="schedule.id">
                <td class="max-w-xs py-4 pr-4"><div class="font-semibold">{{ schedule.name }}</div><div class="mt-1 text-xs text-gray-500">{{ schedule.model }} · {{ t(`modelBenchmark.efforts.${schedule.effort}`) }}</div><div class="break-all text-xs text-gray-500">{{ schedule.base_url }}</div></td>
                <td class="py-4 pr-4"><span>{{ scheduleFrequency(schedule.cron_expression) }}</span><div class="mt-1 text-xs" :class="schedule.enabled ? 'text-emerald-600' : 'text-gray-500'">{{ t(schedule.enabled ? 'modelBenchmark.scheduleEnabled' : 'modelBenchmark.schedulePaused') }}</div></td>
                <td class="whitespace-nowrap py-4 pr-4 text-xs">{{ schedule.next_run_at ? formatScheduledTime(schedule.next_run_at) : schedule.enabled && schedule.active_run_id && schedule.cron_expression.startsWith('@every') ? t('modelBenchmark.waitingInterval') : '—' }}</td>
                <td class="max-w-xs py-4 pr-4 text-xs"><div>{{ schedule.last_attempt_at ? formatScheduledTime(schedule.last_attempt_at) : '—' }}</div><p v-if="schedule.last_error" class="mt-1 text-amber-700 dark:text-amber-300">{{ schedule.last_error }}</p><button v-if="schedule.last_run_id" class="mt-1 text-primary-600 hover:underline" :disabled="!!busy" @click="showDetail(schedule.last_run_id)">{{ t('modelBenchmark.details') }}</button></td>
                <td class="py-4 pr-4"><button type="button" role="switch" :aria-checked="schedule.public_results" :aria-label="t('modelBenchmark.publicResults')" class="rounded-full px-3 py-1 text-xs font-medium" :class="schedule.public_results ? 'bg-emerald-50 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300' : 'bg-gray-100 text-gray-600 dark:bg-dark-800 dark:text-gray-300'" :disabled="!!busy" @click="togglePublicResults(schedule)">{{ t(schedule.public_results ? 'modelBenchmark.publicYes' : 'modelBenchmark.publicNo') }}</button></td>
                <td class="py-4"><div class="flex flex-wrap items-center gap-3"><button type="button" :data-testid="`run-schedule-${schedule.id}`" class="btn btn-primary whitespace-nowrap text-xs" :disabled="!!busy || !state?.ready || state?.resources?.blocked || !!schedule.active_run_id" @click="runSchedule(schedule)">{{ t(schedule.active_run_id ? 'modelBenchmark.scheduleRunning' : 'modelBenchmark.runSchedule') }}</button><button class="text-primary-600 hover:underline" :disabled="!!busy" @click="toggleSchedule(schedule)">{{ t(schedule.enabled ? 'modelBenchmark.pauseSchedule' : 'modelBenchmark.resumeSchedule') }}</button><button class="text-primary-600 hover:underline" :disabled="!!busy" @click="editSchedule(schedule)">{{ t('modelBenchmark.editSchedule') }}</button><button class="text-red-600 hover:underline" :disabled="!!busy" @click="deletingSchedule = schedule">{{ t('modelBenchmark.deleteSchedule') }}</button></div></td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>

      <section class="benchmark-card space-y-4" :aria-label="t('modelBenchmark.history')">
        <div class="flex flex-wrap items-center justify-between gap-3">
          <h2 class="text-lg font-semibold">{{ t(userMode ? 'modelBenchmark.myHistory' : 'modelBenchmark.history') }}</h2>
          <button class="btn btn-secondary" :disabled="loading || !!busy" @click="reload()">{{ t('modelBenchmark.refreshResults') }}</button>
        </div>
        <p class="text-xs leading-5 text-gray-500 dark:text-gray-400">{{ t('modelBenchmark.passRateHint') }} {{ t('modelBenchmark.rerunHint') }}</p>
        <p class="text-xs leading-5 text-gray-500 dark:text-gray-400">{{ t('modelBenchmark.retentionHint') }}</p>
        <p v-if="!runs.length && !loading" class="py-8 text-center text-sm text-gray-500">{{ t('modelBenchmark.empty') }}</p>
        <div v-else class="overflow-x-auto">
          <table class="w-full text-left text-sm">
            <thead class="border-b border-gray-200 text-gray-500 dark:border-dark-700">
              <tr>
                <th class="pb-3 pr-4">{{ t('modelBenchmark.model') }} / {{ t('modelBenchmark.url') }}</th>
                <th class="pb-3 pr-4">{{ t('modelBenchmark.suiteVersion') }}</th>
                <th class="pb-3 pr-4">{{ t('modelBenchmark.status') }}</th>
                <th class="pb-3 pr-4">{{ t('modelBenchmark.passRate') }}</th>
                <th class="pb-3 pr-4">{{ t('modelBenchmark.elapsed') }}</th>
                <th class="pb-3">{{ t('modelBenchmark.actions') }}</th>
              </tr>
            </thead>
            <tbody class="divide-y divide-gray-100 dark:divide-dark-800">
              <tr v-for="run in runs" :key="run.id" :data-run-id="run.id">
                <td class="max-w-xs py-4 pr-4">
                  <div class="break-words font-semibold">{{ run.model }} <span class="text-xs font-normal text-gray-500">{{ t(`modelBenchmark.efforts.${run.effort}`) }}</span></div>
                  <div class="mt-1 break-all text-xs text-gray-500">{{ run.base_url }}</div>
                  <div class="mt-1 text-xs text-gray-500">{{ formatDate(run.created_at) }}</div>
                  <span v-if="run.schedule_id" class="text-xs text-primary-600">{{ t('modelBenchmark.scheduledRun') }}</span>
                </td>
                <td class="whitespace-nowrap py-4 pr-4 text-xs"><div>{{ run.suite_day }} · v{{ run.suite_revision }}</div><code :title="run.suite_fingerprint">{{ run.suite_fingerprint.slice(0, 8) }}</code></td>
                <td class="min-w-36 py-4 pr-4">
                  <span :class="statusColor(run.status)">{{ t(`modelBenchmark.statuses.${run.status}`) }}</span>
                  <div class="mt-1 text-xs text-gray-500">{{ t('modelBenchmark.completed', { completed: run.completed, total: run.total }) }}</div>
                  <div v-if="run.status === 'queued' && run.queue_position" class="mt-1 text-xs text-primary-600">{{ t('modelBenchmark.queuePosition', { position: run.queue_position }) }}</div>
                  <progress v-if="isActive(run)" :value="run.completed" :max="run.total" class="mt-2 h-1.5 w-full" :aria-label="t('modelBenchmark.status')" />
                </td>
                <td class="min-w-36 py-4 pr-4">
                  <strong class="text-xl" :class="benchmarkScoreClass(run.pass_rate, run.status)" data-testid="pass-rate">{{ run.status === 'completed' && run.pass_rate !== null ? `${run.pass_rate.toFixed(1)}%` : '—' }}</strong>
                  <div class="mt-1 text-xs text-gray-500">{{ t('modelBenchmark.passed', { passed: run.passed, total: run.total }) }}</div>
                  <div v-for="category in categories" :key="category" class="text-xs" :class="benchmarkScoreClass(benchmarkCategoryScore(run.categories[category].passed, run.categories[category].total), run.status)">{{ t(`modelBenchmark.categories.${category}`) }} {{ run.categories[category].passed }}/{{ run.categories[category].total }}</div>
                  <template v-if="run.coding_difficulties"><div v-for="difficulty in codingDifficulties" :key="difficulty" class="text-xs" :class="benchmarkScoreClass(benchmarkCategoryScore(run.coding_difficulties[difficulty].passed, run.coding_difficulties[difficulty].total), run.status)">{{ t(`modelBenchmark.difficulties.${difficulty}`) }} {{ run.coding_difficulties[difficulty].passed }}/{{ run.coding_difficulties[difficulty].total }}</div></template>
                </td>
                <td class="whitespace-nowrap py-4 pr-4 text-xs">{{ t('modelBenchmark.seconds', { count: Math.round(run.elapsed_seconds) }) }}</td>
                <td class="py-4">
                  <div class="flex min-w-24 flex-col items-start gap-2">
                    <button class="text-primary-600 hover:underline dark:text-primary-400" :disabled="!!busy" @click="showDetail(run.id)">{{ t('modelBenchmark.details') }}</button>
                    <button v-if="!isActive(run)" class="text-primary-600 hover:underline disabled:opacity-40 dark:text-primary-400" :disabled="!!busy || !state?.ready || state?.resources?.blocked || !hasQuota || userHasActive || hasActiveRerun(run.id)" @click="rerun(run.id)">{{ t('modelBenchmark.rerun') }}</button>
                    <button v-else class="text-red-600 hover:underline disabled:opacity-40 dark:text-red-400" :disabled="!!busy || run.status === 'cancelling'" @click="cancel(run.id)">{{ t('modelBenchmark.cancel') }}</button>
                  </div>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
        <div v-if="totalRuns > 0" class="overflow-x-auto"><Pagination :total="totalRuns" :page="page" :page-size="20" :show-page-size-selector="false" show-jump @update:page="changePage" /></div>
      </section>

      <section v-if="detail" ref="detailPanel" tabindex="-1" class="benchmark-card space-y-4" :aria-label="t('modelBenchmark.questionResults')">
        <div class="flex flex-wrap items-center justify-between gap-3">
          <h2 class="text-lg font-semibold">{{ t('modelBenchmark.questionResults') }} · {{ detail.model }}</h2>
          <button class="btn btn-secondary" @click="closeDetail">{{ t('modelBenchmark.close') }}</button>
        </div>
        <p class="text-sm">{{ t(`modelBenchmark.statuses.${detail.status}`) }} · {{ detail.suite_day }} v{{ detail.suite_revision }} · {{ t('modelBenchmark.tokens', { count: detail.usage.total_tokens }) }}</p>
        <p class="text-sm">{{ t('modelBenchmark.passRate') }}: <strong class="text-2xl" :class="benchmarkScoreClass(detail.pass_rate, detail.status)" data-testid="detail-pass-rate">{{ detail.status === 'completed' && detail.pass_rate !== null ? detail.pass_rate.toFixed(1) + '%' : '—' }}</strong></p>
        <p v-if="detail.error" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ detail.error }}</p>
        <p v-if="detail.api_errors || detail.grading_errors" class="text-sm text-red-600 dark:text-red-400">{{ t('modelBenchmark.errors', { api: detail.api_errors, grading: detail.grading_errors }) }}</p>
        <p v-if="!detail.results?.length" class="text-sm text-gray-500">{{ t('modelBenchmark.noResults') }}</p>
        <div v-for="result in detail.results" :key="result.id" class="rounded-lg border border-gray-200 p-4 dark:border-dark-700">
          <div class="flex flex-wrap justify-between gap-2 text-sm">
            <span class="font-medium">{{ result.index }}. {{ result.title }} <span class="text-xs text-gray-500">{{ t(`modelBenchmark.categories.${result.category}`) }}</span> <span v-if="result.category === 'coding' && (result.difficulty === 'medium' || result.difficulty === 'hard')" class="rounded bg-gray-100 px-2 py-0.5 text-xs font-normal text-gray-600 dark:bg-dark-800 dark:text-gray-300">{{ t(`modelBenchmark.difficulties.${result.difficulty}`) }}</span></span>
            <span :class="result.passed ? 'text-emerald-600 dark:text-emerald-400' : 'text-red-600 dark:text-red-400'">{{ t(`modelBenchmark.resultStatuses.${result.status}`) }}</span>
          </div>
          <p class="mt-1 text-xs text-gray-500">{{ result.message }} · {{ t('modelBenchmark.seconds', { count: result.elapsed_seconds }) }} <span v-if="result.test_count"> · {{ t('modelBenchmark.tests', { passed: result.tests_passed, total: result.test_count }) }}</span></p>
          <details v-if="result.answer" class="mt-3 text-sm">
            <summary class="cursor-pointer text-primary-600 dark:text-primary-400">{{ t('modelBenchmark.answer') }}</summary>
            <p v-if="result.answer_truncated" class="mt-2 text-xs text-gray-500">{{ t('modelBenchmark.truncated') }}</p>
            <pre class="mt-2 max-h-96 overflow-auto whitespace-pre-wrap break-words rounded bg-gray-50 p-3 text-xs dark:bg-dark-800">{{ result.answer }}</pre>
          </details>
        </div>
      </section>
      <p v-if="!userMode" class="text-xs leading-6 text-gray-500 dark:text-gray-400">{{ t('modelBenchmark.caveat') }}</p>
      </section>
      </div>
      <BenchmarkKeyPicker :show="showKeyPicker" @close="showKeyPicker = false" @select="selectOwnKey" />
      <BaseDialog :show="!!editingSchedule" :title="t('modelBenchmark.editSchedule')" @close="editingSchedule = null">
        <form class="space-y-4" @submit.prevent="saveSchedule">
          <BenchmarkScheduleFields v-model="editForm" :timezone="state?.timezone || 'Asia/Shanghai'" />
          <button type="submit" class="btn btn-primary" :disabled="!!busy">{{ t('common.save') }}</button>
        </form>
      </BaseDialog>
      <ConfirmDialog :show="!!deletingSchedule" :title="t('modelBenchmark.deleteSchedule')" :message="t('modelBenchmark.deleteScheduleHint')" danger @cancel="deletingSchedule = null" @confirm="removeSchedule" />
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import IdentificationTabs from '@/components/model-identification/IdentificationTabs.vue'
import BenchmarkKeyPicker from '@/components/model-identification/BenchmarkKeyPicker.vue'
import BenchmarkScheduleFields from '@/components/model-identification/BenchmarkScheduleFields.vue'
import BenchmarkDisplaySettings from '@/components/model-identification/BenchmarkDisplaySettings.vue'
import BenchmarkBankSettings from '@/components/model-identification/BenchmarkBankSettings.vue'
import BenchmarkResultGallery from '@/components/model-identification/BenchmarkResultGallery.vue'
import BenchmarkUserRecords from '@/components/model-identification/BenchmarkUserRecords.vue'
import { benchmarkScoreClass, benchmarkCategoryScore } from '@/utils/benchmarkScore'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Pagination from '@/components/common/Pagination.vue'
import { useAppStore } from '@/stores/app'
import { buildGatewayUrl } from '@/api/url'
import { benchmarks as adminBenchmarks, type BenchmarkRun, type BenchmarkState, type BenchmarkSchedule, type ScheduleSettings, type Category, type Counts, type Effort, type CodingCounts, type CodingDifficulty } from '@/api/admin/model-benchmarks'

import { userBenchmarks, type SharedDisplay } from '@/api/model-benchmarks'

const { userMode = false } = defineProps<{ userMode?: boolean }>()
const benchmarks = userMode ? userBenchmarks : adminBenchmarks
const sharedDisplays = ref<SharedDisplay[]>([])
const activeTab = ref<'tests' | 'display' | 'users'>('tests')
const maxConcurrency = ref(5)
const { t } = useI18n()
const app = useAppStore()
const categories: Category[] = ['coding', 'math', 'reasoning']
const codingDifficulties: CodingDifficulty[] = ['medium', 'hard']
const codingCounts = reactive<CodingCounts>({ medium: 10, hard: 10 })
const efforts: Effort[] = ['low', 'medium', 'high', 'xhigh']
const form = reactive({ base_url: '', api_key: '', model: '', effort: 'medium' as Effort })
const showKeyPicker = ref(false)
const selectedKeyName = ref('')
const scheduledMode = ref(false)
const scheduleForm = ref<ScheduleSettings>({ name: '', cron_expression: '@every 30m', enabled: true, public_results: false })
const schedules = ref<BenchmarkSchedule[]>([])
const editingSchedule = ref<BenchmarkSchedule | null>(null)
const deletingSchedule = ref<BenchmarkSchedule | null>(null)
const editForm = ref<ScheduleSettings>({ name: '', cron_expression: '0 9 * * *', enabled: true, public_results: false })
const counts = reactive<Counts>({ coding: 20, math: 5, reasoning: 5 })
const state = ref<BenchmarkState | null>(null)
const concurrencyDirty = computed(() => maxConcurrency.value !== state.value?.execution?.max_concurrency)
const validConcurrency = computed(() => Number.isInteger(maxConcurrency.value) && maxConcurrency.value >= 1 && maxConcurrency.value <= 100)
const runs = ref<BenchmarkRun[]>([])
const detail = ref<BenchmarkRun | null>(null)
const detailPanel = ref<HTMLElement | null>(null)
const page = ref(1)
const totalRuns = ref(0)
const loading = ref(false)
const busy = ref('')
const loadError = ref('')
const controller = new AbortController()
let timer: ReturnType<typeof setTimeout> | undefined
let revision = 0
let detailRevision = 0
const countTotal = computed(() => categories.reduce((sum, c) => sum + Number(counts[c] || 0), 0))
const savedCodingCounts = computed(() => state.value?.coding_counts ?? { medium: Math.ceil((state.value?.counts.coding ?? 20) / 2), hard: Math.floor((state.value?.counts.coding ?? 20) / 2) })
const dirtyCounts = computed(() => !!state.value && (categories.some(c => counts[c] !== state.value?.counts[c]) || codingDifficulties.some(d => codingCounts[d] !== savedCodingCounts.value[d])))
const validCounts = computed(() => countTotal.value >= 1 && countTotal.value <= 100 && categories.every(c => Number.isInteger(counts[c]) && counts[c] >= 0 && counts[c] <= (state.value?.capacities[c] ?? 100)) && codingDifficulties.every(d => Number.isInteger(codingCounts[d]) && codingCounts[d] >= 0 && codingCounts[d] <= (state.value?.coding_capacities?.[d] ?? 100)) && codingCounts.medium + codingCounts.hard === counts.coding)
const hasQuota = computed(() => !userMode || (state.value?.quota?.remaining ?? 0) > 0)
const canUsePersonalTests = computed(() => !userMode || state.value?.execution?.user_testing_enabled === true)
const userHasActive = computed(() => userMode && (!!state.value?.active_run || runs.value.some(isActive)))
const canStart = computed(() => canUsePersonalTests.value && !userHasActive.value && hasQuota.value && state.value?.ready && !state.value?.resources?.blocked && !busy.value && !dirtyCounts.value && !!form.base_url && !!form.api_key.trim() && !!form.model)
const isActive = (run: BenchmarkRun) => ['queued', 'running', 'cancelling'].includes(run.status)
const hasActiveRerun = (id: string) => runs.value.some(run => run.rerun_of === id && isActive(run))
const statusColor = (status: string) => status === 'completed' ? 'text-emerald-600 dark:text-emerald-400' : ['failed', 'interrupted'].includes(status) ? 'text-red-600 dark:text-red-400' : 'text-gray-600 dark:text-gray-300'
const formatDate = (value: string) => new Date(value).toLocaleString()
const formatScheduledTime = (value: string) => new Date(value).toLocaleString(undefined, { timeZone: state.value?.timezone || 'Asia/Shanghai' })
function scheduleFrequency(expression: string) {
  const interval = expression.match(/^@every\s+(\d+)(m|h)$/)
  return interval ? t('modelBenchmark.intervalSummary', { value: interval[1], unit: t(`modelBenchmark.${interval[2] === 'h' ? 'hours' : 'minutes'}`) }) : expression
}
function clearSelectedKey() { if (selectedKeyName.value) { form.api_key = ''; selectedKeyName.value = '' } }
function selectOwnKey(key: { name: string; key: string }) {
  const url = new URL(app.apiBaseUrl || buildGatewayUrl('/v1'), window.location.origin)
  if (url.pathname === '/') url.pathname = '/v1'
  form.base_url = url.href.replace(/\/+$/, '')
  form.api_key = key.key
  selectedKeyName.value = key.name
  showKeyPicker.value = false
}
function resetCounts() { if (state.value) { Object.assign(counts, state.value.counts); Object.assign(codingCounts, savedCodingCounts.value) } }
function updateCodingTotal() { counts.coding = Number(codingCounts.medium || 0) + Number(codingCounts.hard || 0) }
function errorMessage(error: unknown, fallback: string) {
  const e = error as { response?: { data?: { message?: string } }; message?: string }
  return e?.response?.data?.message || e?.message || t(fallback)
}
async function reload() {
  const current = ++revision
  const selected = detail.value?.id
  const selectedRevision = detailRevision
  loading.value = true
  const stateRequest = benchmarks.state(controller.signal)
  const personalTestsAllowed = stateRequest.then(value => !userMode || value.execution?.user_testing_enabled === true)
  const results = await Promise.allSettled([
    stateRequest,
    personalTestsAllowed.then(allowed => allowed ? benchmarks.list(page.value, controller.signal) : { items: [] as BenchmarkRun[], total: 0, page: 1, page_size: 20 }),
    personalTestsAllowed.then(allowed => allowed && selected ? benchmarks.detail(selected, controller.signal) : null),
    userMode ? Promise.resolve({ items: [] as BenchmarkSchedule[] }) : adminBenchmarks.schedules(controller.signal),
    userMode ? userBenchmarks.showcase(controller.signal) : Promise.resolve({ items: [] as SharedDisplay[] })
  ])
  if (controller.signal.aborted || current !== revision) return
  const [suite, history, selectedRun, plans, shared] = results
  if (suite.status === 'fulfilled') {
    const preserve = dirtyCounts.value
    const preserveConcurrency = !!state.value?.execution && concurrencyDirty.value
    state.value = suite.value
    if (!preserveConcurrency) maxConcurrency.value = state.value.execution?.max_concurrency ?? 5
    if (!preserve) resetCounts()
  }
  if (history.status === 'fulfilled') { runs.value = history.value.items; totalRuns.value = history.value.total; page.value = history.value.page ?? page.value }
  if (selectedRun.status === 'fulfilled' && detailRevision === selectedRevision) detail.value = selectedRun.value
  if (!canUsePersonalTests.value) {
    detail.value = null
    showKeyPicker.value = false
    form.api_key = ''
    selectedKeyName.value = ''
  }
  if (plans.status === 'fulfilled') schedules.value = plans.value.items
  if (shared.status === 'fulfilled') sharedDisplays.value = shared.value.items
  else if (userMode) sharedDisplays.value = []
  loadError.value = results.some(r => r.status === 'rejected') ? t('modelBenchmark.loadError') : ''
  loading.value = false
}
async function action(name: string, work: () => Promise<void>) {
  if (busy.value) return
  busy.value = name
  ++revision
  try { await work() } catch (error) {
    if (!controller.signal.aborted) app.showError(errorMessage(error, 'modelBenchmark.actionError'))
  } finally {
    if (!controller.signal.aborted) { await reload(); busy.value = '' }
  }
}
async function submitTarget() {
  if (!canStart.value || !state.value?.suite) return
  await action('create', async () => {
    if (!userMode && scheduledMode.value) {
      await adminBenchmarks.createSchedule({ ...form, ...scheduleForm.value }, controller.signal)
      app.showSuccess(t('modelBenchmark.scheduleCreated'))
    } else {
      const run = await benchmarks.create({ ...form, suite_id: state.value!.suite!.id }, controller.signal)
      page.value = 1
      detail.value = run
      ++detailRevision
      app.showSuccess(t('modelBenchmark.created'))
    }
    form.api_key = ''
    selectedKeyName.value = ''
  })
}
async function saveConcurrency() {
  if (userMode || !validConcurrency.value) return
  await action('execution', async () => {
    const execution = await adminBenchmarks.setExecution(maxConcurrency.value, controller.signal)
    if (state.value) state.value.execution = execution
    app.showSuccess(t('modelBenchmark.concurrencySaved'))
  })
}
async function toggleUserTesting() {
  if (userMode || !state.value?.execution) return
  const enabled = !state.value.execution.user_testing_enabled
  await action('user-testing', async () => {
    const execution = await adminBenchmarks.setUserTesting(enabled, controller.signal)
    if (state.value) state.value.execution = execution
    app.showSuccess(t('modelBenchmark.userTestingSaved'))
  })
}
function editSchedule(schedule: BenchmarkSchedule) {
  editingSchedule.value = schedule
  editForm.value = { name: schedule.name, cron_expression: schedule.cron_expression, enabled: schedule.enabled, public_results: schedule.public_results }
}
async function runSchedule(schedule: BenchmarkSchedule) {
  if (userMode || !state.value?.ready || schedule.active_run_id) return
  await action('schedule', async () => {
    const run = await adminBenchmarks.runSchedule(schedule.id, controller.signal)
    schedule.active_run_id = run.id
    detail.value = run
    ++detailRevision
    page.value = 1
    app.showSuccess(t('modelBenchmark.scheduleQueued'))
  })
}
async function saveSchedule() {
  if (!editingSchedule.value) return
  const id = editingSchedule.value.id
  await action('schedule', async () => { await adminBenchmarks.updateSchedule(id, editForm.value, controller.signal); editingSchedule.value = null; app.showSuccess(t('modelBenchmark.scheduleUpdated')) })
}
async function togglePublicResults(schedule: BenchmarkSchedule) {
  await action('schedule', async () => { await adminBenchmarks.updateSchedule(schedule.id, { public_results: !schedule.public_results }, controller.signal); app.showSuccess(t('modelBenchmark.scheduleUpdated')) })
}
async function toggleSchedule(schedule: BenchmarkSchedule) {
  await action('schedule', async () => { await adminBenchmarks.updateSchedule(schedule.id, { enabled: !schedule.enabled }, controller.signal); app.showSuccess(t('modelBenchmark.scheduleUpdated')) })
}
async function removeSchedule() {
  if (!deletingSchedule.value) return
  const id = deletingSchedule.value.id
  await action('schedule', async () => { await adminBenchmarks.deleteSchedule(id, controller.signal); deletingSchedule.value = null; app.showSuccess(t('modelBenchmark.scheduleDeleted')) })
}
async function changeSuite(save: boolean) {
  if (userMode || !state.value?.ready || !state.value.suite || (save && !validCounts.value)) return
  await action('suite', async () => {
    const suite = save
      ? await adminBenchmarks.configure({ ...counts }, state.value!.suite!.id, controller.signal, { ...codingCounts })
      : await adminBenchmarks.refresh(state.value!.suite!.id, controller.signal)
    state.value = { ...state.value!, suite, counts: suite.counts, coding_counts: suite.coding_counts }
    resetCounts()
    app.showSuccess(t('modelBenchmark.refreshed'))
  })
}
async function rerun(id: string) {
  if (!canUsePersonalTests.value || !hasQuota.value || userHasActive.value) return
  await action(id, async () => {
    detail.value = await benchmarks.rerun(id, controller.signal)
    ++detailRevision
    page.value = 1
    app.showSuccess(t('modelBenchmark.created'))
  })
}
async function retryEnvironment() {
  if (userMode) return
  await action('environment', async () => { await adminBenchmarks.retryEnvironment(controller.signal) })
}
async function updateBank() {
  if (userMode || !state.value?.ready) return
  await action('bank', async () => { const status = await adminBenchmarks.updateBank(controller.signal); if (state.value) state.value.bank_update = status })
}
async function activateBank(version: string) {
  if (userMode || !state.value?.ready) return
  await action('bank', async () => { await adminBenchmarks.activateBank(version, controller.signal); app.showSuccess(t('modelBenchmark.bankActivated')) })
}
async function cancel(id: string) {
  await action(id, async () => {
    await benchmarks.cancel(id, controller.signal)
    app.showSuccess(t('modelBenchmark.cancelHint'))
  })
}
async function showDetail(id: string) {
  await action('detail', async () => {
    detail.value = await benchmarks.detail(id, controller.signal)
    ++detailRevision
    await nextTick()
    detailPanel.value?.focus({ preventScroll: true })
    detailPanel.value?.scrollIntoView({ behavior: 'smooth', block: 'start' })
  })
}
function closeDetail() { detail.value = null; ++detailRevision }
async function changePage(next: number) {
  if (loading.value || busy.value || next < 1 || next > Math.ceil(totalRuns.value / 20)) return
  page.value = next
  await reload()
}
async function poll() {
  if (!busy.value && !loading.value) await reload()
  if (!controller.signal.aborted) timer = setTimeout(poll, 5000)
}
onMounted(poll)
onBeforeUnmount(() => { controller.abort(); clearTimeout(timer); form.api_key = ''; ++revision })
</script>

<style scoped>
.benchmark-card { @apply min-w-0 rounded-xl border border-gray-200 bg-white p-5 dark:border-dark-700 dark:bg-dark-900; }
</style>

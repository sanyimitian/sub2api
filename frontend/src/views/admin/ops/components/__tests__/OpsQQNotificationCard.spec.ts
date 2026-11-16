import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, shallowMount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import zhOps from '@/i18n/locales/zh/admin/ops'
import enOps from '@/i18n/locales/en/admin/ops'
import OpsQQNotificationCard from '../OpsQQNotificationCard.vue'

const mocks = vi.hoisted(() => ({ get: vi.fn(), update: vi.fn(), test: vi.fn(), webhook: vi.fn(), error: vi.fn(), success: vi.fn() }))
vi.mock('vue-i18n', async (importOriginal) => ({
  ...await importOriginal<typeof import('vue-i18n')>(), useI18n: () => ({ t: (key: string) => key })
}))
vi.mock('@/api/admin/ops', () => ({ opsAPI: {
  getQQNotificationConfig: mocks.get, updateQQNotificationConfig: mocks.update, testQQNotification: mocks.test, getQQWebhookStatus: mocks.webhook
} }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError: mocks.error, showSuccess: mocks.success }) }))
enableAutoUnmount(afterEach)

describe('QQ notification settings', () => {
  it('renders every QQ settings label with the real translation compiler', () => {
    for (const messages of [zhOps.ops.qq, enOps.ops.qq]) {
      const i18n = createI18n({ legacy: false, locale: 'test', messages: { test: messages } })
      for (const [key, value] of Object.entries(messages)) {
        expect(i18n.global.t(key)).toBe(value)
      }
      i18n.dispose()
    }
  })
  const config = {
    enabled: true, provider: 'onebot', api_url: 'http://localhost:3000', group_id: '123456',
    access_token_configured: true, app_id: '', app_secret_configured: false, group_openid: ''
  }
  const official = { ...config, provider: 'qq_official', app_id: '100001', app_secret_configured: true, group_openid: 'GROUP_A1' }
  beforeEach(() => {
    vi.clearAllMocks()
    mocks.get.mockResolvedValue({ ...config })
    mocks.update.mockResolvedValue({ ...config })
    mocks.test.mockResolvedValue(undefined)
    mocks.webhook.mockResolvedValue({ app_id: '100001', callback_path: '/api/v1/ops/qq-bot/webhook', ready: true, groups: [] })
  })
  it('preserves the saved token when editing other fields', async () => {
    const wrapper = shallowMount(OpsQQNotificationCard)
    await flushPromises()
    expect((wrapper.get('#ops-qq-token').element as HTMLInputElement).value).toBe('')
    await wrapper.get('#ops-qq-group').setValue('987654')
    await wrapper.findAll('button').find(b => b.text() === 'admin.ops.qq.save')!.trigger('click')
    await flushPromises()
    expect(mocks.update).toHaveBeenCalledWith(expect.objectContaining({ group_id: '987654', access_token: undefined, clear_access_token: false }))
    expect(mocks.test).not.toHaveBeenCalled()
  })
  it('saves before testing and clears the typed token after save', async () => {
    const wrapper = shallowMount(OpsQQNotificationCard)
    await flushPromises()
    await wrapper.get('#ops-qq-token').setValue('new-token')
    await wrapper.findAll('button').find(b => b.text() === 'admin.ops.qq.test')!.trigger('click')
    await flushPromises()
    expect(mocks.update).toHaveBeenCalledWith(expect.objectContaining({ access_token: 'new-token' }))
    expect(mocks.test).toHaveBeenCalledOnce()
    expect(mocks.update.mock.invocationCallOrder[0]).toBeLessThan(mocks.test.mock.invocationCallOrder[0])
    expect((wrapper.get('#ops-qq-token').element as HTMLInputElement).value).toBe('')
  })
  it('does not send a test when saving fails', async () => {
    mocks.update.mockRejectedValue(new Error('failed'))
    const wrapper = shallowMount(OpsQQNotificationCard)
    await flushPromises()
    await wrapper.findAll('button').find(b => b.text() === 'admin.ops.qq.test')!.trigger('click')
    await flushPromises()
    expect(mocks.test).not.toHaveBeenCalled()
    expect(mocks.error).toHaveBeenCalled()
  })
  it('loads legacy settings as OneBot', async () => {
    mocks.get.mockResolvedValue({ ...config, provider: undefined })
    const wrapper = shallowMount(OpsQQNotificationCard)
    await flushPromises()
    expect((wrapper.get('#ops-qq-provider').element as HTMLSelectElement).value).toBe('onebot')
    expect(wrapper.find('#ops-qq-token').exists()).toBe(true)
    expect(wrapper.find('#ops-qq-app-secret').exists()).toBe(false)
  })
  it.each([
    [{ status: 400, message: 'QQ official bot returned HTTP 400 (code 40034105): allow proactive messages' }, 'admin.ops.qq.officialPermissionDenied'],
    [{ status: 400, message: 'QQ official bot returned HTTP 429 (code 40034100): proactive message rate limit reached' }, 'QQ official bot returned HTTP 429 (code 40034100): proactive message rate limit reached'],
    [{ response: { data: { detail: 'legacy error detail' } } }, 'legacy error detail'],
    [{}, 'admin.ops.qq.saveFailed']
  ])('shows the specific test failure after saving successfully (%j)', async (error, expected) => {
    mocks.test.mockRejectedValue(error)
    const wrapper = shallowMount(OpsQQNotificationCard)
    await flushPromises()
    await wrapper.findAll('button').find(b => b.text() === 'admin.ops.qq.test')!.trigger('click')
    await flushPromises()
    expect(mocks.update).toHaveBeenCalledOnce()
    expect(mocks.test).toHaveBeenCalledOnce()
    expect(mocks.error).toHaveBeenCalledWith(expected)
    expect(wrapper.get('[role="alert"]').text()).toBe(expected)
    expect(mocks.success).not.toHaveBeenCalled()
    mocks.test.mockResolvedValue(undefined)
    await wrapper.findAll('button').find(b => b.text() === 'admin.ops.qq.test')!.trigger('click')
    await flushPromises()
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
  })
  it('preserves the official secret when editing the target group', async () => {
    mocks.get.mockResolvedValue({ ...official })
    mocks.update.mockResolvedValue({ ...official })
    const wrapper = shallowMount(OpsQQNotificationCard)
    await flushPromises()
    expect(wrapper.find('#ops-qq-url').exists()).toBe(false)
    expect(wrapper.find('#ops-qq-token').exists()).toBe(false)
    expect((wrapper.get('#ops-qq-app-secret').element as HTMLInputElement).value).toBe('')
    await wrapper.get('#ops-qq-group-openid').setValue('GROUP_B2')
    await wrapper.findAll('button').find(b => b.text() === 'admin.ops.qq.save')!.trigger('click')
    await flushPromises()
    expect(mocks.update).toHaveBeenCalledWith(expect.objectContaining({
      provider: 'qq_official', app_id: '100001', group_openid: 'GROUP_B2',
      app_secret: undefined, clear_app_secret: false, access_token: undefined
    }))
  })
  it('saves official credentials before testing and clears the secret input', async () => {
    mocks.update.mockResolvedValue({ ...official })
    const wrapper = shallowMount(OpsQQNotificationCard)
    await flushPromises()
    await wrapper.get('#ops-qq-provider').setValue('qq_official')
    await wrapper.get('#ops-qq-app-id').setValue('100001')
    await wrapper.get('#ops-qq-app-secret').setValue('new-secret')
    await wrapper.get('#ops-qq-group-openid').setValue('GROUP_A1')
    await wrapper.findAll('button').find(b => b.text() === 'admin.ops.qq.test')!.trigger('click')
    await flushPromises()
    expect(mocks.update).toHaveBeenCalledWith(expect.objectContaining({
      provider: 'qq_official', app_id: '100001', app_secret: 'new-secret', group_openid: 'GROUP_A1'
    }))
    expect(mocks.test).toHaveBeenCalledOnce()
    expect(mocks.update.mock.invocationCallOrder[0]).toBeLessThan(mocks.test.mock.invocationCallOrder[0])
    expect((wrapper.get('#ops-qq-app-secret').element as HTMLInputElement).value).toBe('')
  })
  it('clears a saved official secret explicitly', async () => {
    mocks.get.mockResolvedValue({ ...official, enabled: false })
    mocks.update.mockResolvedValue({ ...official, enabled: false, app_secret_configured: false })
    const wrapper = shallowMount(OpsQQNotificationCard)
    await flushPromises()
    await wrapper.get('#ops-qq-clear-secret').setValue(true)
    await wrapper.findAll('button').find(b => b.text() === 'admin.ops.qq.save')!.trigger('click')
    await flushPromises()
    expect(mocks.update).toHaveBeenCalledWith(expect.objectContaining({ clear_app_secret: true }))
    expect(wrapper.find('#ops-qq-clear-secret').exists()).toBe(false)
  })
  it('discards unsaved secrets when switching providers', async () => {
    const wrapper = shallowMount(OpsQQNotificationCard)
    await flushPromises()
    await wrapper.get('#ops-qq-token').setValue('unsaved-token')
    await wrapper.get('#ops-qq-provider').setValue('qq_official')
    await wrapper.get('#ops-qq-app-secret').setValue('unsaved-secret')
    await wrapper.get('#ops-qq-provider').setValue('onebot')
    expect((wrapper.get('#ops-qq-token').element as HTMLInputElement).value).toBe('')
    await wrapper.get('#ops-qq-provider').setValue('qq_official')
    expect((wrapper.get('#ops-qq-app-secret').element as HTMLInputElement).value).toBe('')
  })
  it('discovers and selects a group without automatically sending or saving', async () => {
    mocks.get.mockResolvedValue({ ...official, enabled: false, group_openid: '' })
    const wrapper = shallowMount(OpsQQNotificationCard)
    await flushPromises()
    expect((wrapper.get('#ops-qq-callback').element as HTMLInputElement).value).toBe(window.location.origin + '/api/v1/ops/qq-bot/webhook')
    mocks.webhook.mockResolvedValue({ app_id: '100001', ready: true, groups: [{ group_openid: 'FOUND_GROUP', event_type: 'GROUP_AT_MESSAGE_CREATE', last_seen_at: '2026-11-17T00:00:00Z' }] })
    await wrapper.get('#ops-qq-refresh-groups').trigger('click')
    await flushPromises()
    await wrapper.get('[data-group-openid="FOUND_GROUP"]').trigger('click')
    expect((wrapper.get('#ops-qq-group-openid').element as HTMLInputElement).value).toBe('FOUND_GROUP')
    expect(mocks.update).not.toHaveBeenCalled()
    expect(mocks.test).not.toHaveBeenCalled()
    await wrapper.get('#ops-qq-app-id').setValue('200002')
    expect(wrapper.find('[data-group-openid="FOUND_GROUP"]').exists()).toBe(false)
  })
  it('saves credentials with notifications disabled before discovering a group', async () => {
    mocks.get.mockResolvedValue({ ...official, enabled: false, group_openid: '' })
    mocks.update.mockResolvedValue({ ...official, enabled: false, group_openid: '' })
    const wrapper = shallowMount(OpsQQNotificationCard)
    await flushPromises()
    await wrapper.get('#ops-qq-app-secret').setValue('initial-secret')
    await wrapper.findAll('button').find(b => b.text() === 'admin.ops.qq.save')!.trigger('click')
    await flushPromises()
    expect(mocks.update).toHaveBeenCalledWith(expect.objectContaining({ enabled: false, group_openid: '', app_secret: 'initial-secret' }))
    expect(mocks.webhook).toHaveBeenCalledTimes(2)
    expect(mocks.test).not.toHaveBeenCalled()
  })
  it('reports callback status errors without failing configuration loading', async () => {
    mocks.get.mockResolvedValue({ ...official })
    mocks.webhook.mockRejectedValue(new Error('unavailable'))
    const wrapper = shallowMount(OpsQQNotificationCard)
    await flushPromises()
    expect(wrapper.find('#ops-qq-app-id').exists()).toBe(true)
    expect(mocks.error).toHaveBeenCalledWith('admin.ops.qq.webhookLoadFailed')
  })
})

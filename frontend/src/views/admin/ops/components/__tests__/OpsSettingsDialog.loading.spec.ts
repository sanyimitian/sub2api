import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, shallowMount } from '@vue/test-utils'

vi.mock('vue-i18n', async (importOriginal) => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({ t: (key: string) => key }),
}))
enableAutoUnmount(afterEach)

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}

import OpsSettingsDialog from '../OpsSettingsDialog.vue'
import OpsQQNotificationCard from '../OpsQQNotificationCard.vue'
const { opsAPI, showError, showSuccess } = vi.hoisted(() => ({
  opsAPI: {
    getAlertRuntimeSettings: vi.fn(), getEmailNotificationConfig: vi.fn(),
    getAdvancedSettings: vi.fn(), getMetricThresholds: vi.fn(),
    updateAlertRuntimeSettings: vi.fn(), updateEmailNotificationConfig: vi.fn(),
    updateAdvancedSettings: vi.fn(), updateMetricThresholds: vi.fn(),
    getQQNotificationConfig: vi.fn(), updateQQNotificationConfig: vi.fn(),
    getQQWebhookStatus: vi.fn(), testQQNotification: vi.fn(),
  }, showError: vi.fn(), showSuccess: vi.fn(),
}))
vi.mock('@/api/admin/ops', () => ({ opsAPI }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError, showSuccess }) }))
async function openDialog() {
  const wrapper = shallowMount(OpsSettingsDialog, {
    props: { show: false },
    global: { stubs: {
      BaseDialog: { template: '<div><slot /><slot name="footer" /></div>' },
      OpsQQNotificationCard: false, Toggle: false,
    } },
  })
  await wrapper.setProps({ show: true })
  return wrapper
}
type View = { saveAllSettings: () => Promise<void> }
describe('ops settings load guard', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    vi.spyOn(console, 'error').mockImplementation(() => {})
    opsAPI.getAlertRuntimeSettings.mockResolvedValue({ evaluation_interval_seconds: 30 })
    opsAPI.getEmailNotificationConfig.mockResolvedValue({ alert: { enabled: false, recipients: [] }, report: { enabled: false, recipients: [] } })
    opsAPI.getAdvancedSettings.mockResolvedValue({
      data_retention: { cleanup_enabled: false, error_log_retention_days: 7, minute_metrics_retention_days: 7, hourly_metrics_retention_days: 7 },
      aggregation: { aggregation_enabled: false },
      ignore_count_tokens_errors: false, ignore_context_canceled: false, ignore_no_available_accounts: false,
      ignore_insufficient_balance_errors: false, auto_refresh_enabled: false,
      display_alert_events: false, display_openai_token_stats: false,
    })
    opsAPI.getMetricThresholds.mockResolvedValue({ sla_percent_min: 98 })
    let qq = {
      enabled: false, provider: 'qq_official', api_url: '', group_id: '',
      access_token_configured: false, app_id: '100001',
      app_secret_configured: true, group_openid: 'GROUP_A1',
    }
    opsAPI.getQQNotificationConfig.mockImplementation(async () => ({ ...qq }))
    opsAPI.updateQQNotificationConfig.mockImplementation(async (update) => {
      qq = { ...qq, ...update }
      return { ...qq }
    })
    opsAPI.getQQWebhookStatus.mockResolvedValue({ app_id: '100001', ready: true, groups: [] })
  })
  afterEach(() => { vi.restoreAllMocks() })
  it('blocks saving while any settings request is still loading', async () => {
    const pending = deferred<object>()
    opsAPI.getMetricThresholds.mockReturnValueOnce(pending.promise)
    const wrapper = await openDialog()
    expect(wrapper.get('button.btn-primary').attributes('disabled')).toBeDefined()
    await (wrapper.vm as unknown as View).saveAllSettings()
    expect(opsAPI.updateMetricThresholds).not.toHaveBeenCalled()
    pending.resolve({ sla_percent_min: 98 })
    await flushPromises()
    expect(wrapper.get('button.btn-primary').attributes('disabled')).toBeUndefined()
    await wrapper.get('button.btn-primary').trigger('click')
    await flushPromises()
    expect(opsAPI.updateMetricThresholds).toHaveBeenCalledWith(expect.objectContaining({ sla_percent_min: 98 }))
    expect(wrapper.emitted('saved')).toHaveLength(1)
  })
  it('does not save default thresholds after a failed initial load', async () => {
    opsAPI.getMetricThresholds.mockRejectedValueOnce(new Error('load failed'))
    const wrapper = await openDialog()
    await flushPromises()
    expect(wrapper.get('button.btn-primary').attributes('disabled')).toBeDefined()
    await (wrapper.vm as unknown as View).saveAllSettings()
    expect(opsAPI.updateMetricThresholds).not.toHaveBeenCalled()
    expect(wrapper.emitted('saved')).toBeUndefined()
  })
  it('blocks saving stale settings after a failed reopen and allows a later retry', async () => {
    const wrapper = await openDialog()
    await flushPromises()
    await wrapper.setProps({ show: false })
    opsAPI.getMetricThresholds.mockRejectedValueOnce(new Error('refresh failed'))
    await wrapper.setProps({ show: true })
    await flushPromises()
    expect(wrapper.get('button.btn-primary').attributes('disabled')).toBeDefined()
    await (wrapper.vm as unknown as View).saveAllSettings()
    expect(opsAPI.updateAlertRuntimeSettings).not.toHaveBeenCalled()
    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true })
    await flushPromises()
    expect(wrapper.get('button.btn-primary').attributes('disabled')).toBeUndefined()
  })
  it('persists the QQ switch and target group through the dialog save and reopen', async () => {
    const wrapper = await openDialog()
    await flushPromises()
    const card = wrapper.getComponent(OpsQQNotificationCard)
    expect(card.get('[role="switch"]').attributes('aria-checked')).toBe('false')
    await card.get('[role="switch"]').trigger('click')
    await card.get('#ops-qq-group-openid').setValue('GROUP_B2')
    await wrapper.get('button.btn-primary').trigger('click')
    await flushPromises()
    expect(opsAPI.updateQQNotificationConfig).toHaveBeenCalledOnce()
    expect(opsAPI.updateQQNotificationConfig).toHaveBeenCalledWith(expect.objectContaining({
      enabled: true, group_openid: 'GROUP_B2', app_secret: undefined, clear_app_secret: false,
    }))
    expect(opsAPI.testQQNotification).not.toHaveBeenCalled()
    expect(showSuccess).toHaveBeenCalledOnce()
    expect(showSuccess).toHaveBeenCalledWith('admin.ops.settings.saveSuccess')
    expect(wrapper.emitted('close')).toHaveLength(1)
    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true })
    await flushPromises()
    const reopened = wrapper.getComponent(OpsQQNotificationCard)
    expect(reopened.get('[role="switch"]').attributes('aria-checked')).toBe('true')
    expect((reopened.get('#ops-qq-group-openid').element as HTMLInputElement).value).toBe('GROUP_B2')
    await reopened.get('[role="switch"]').trigger('click')
    await wrapper.get('button.btn-primary').trigger('click')
    await flushPromises()
    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true })
    await flushPromises()
    expect(wrapper.getComponent(OpsQQNotificationCard).get('[role="switch"]').attributes('aria-checked')).toBe('false')
  })
  it('waits for QQ settings before allowing the dialog to save', async () => {
    const pending = deferred<object>()
    opsAPI.getQQNotificationConfig.mockReturnValueOnce(pending.promise)
    const wrapper = await openDialog()
    await flushPromises()
    expect(wrapper.get('button.btn-primary').attributes('disabled')).toBeDefined()
    await (wrapper.vm as unknown as View).saveAllSettings()
    expect(opsAPI.updateAlertRuntimeSettings).not.toHaveBeenCalled()
    pending.resolve({ enabled: false, provider: 'onebot', api_url: '', group_id: '' })
    await flushPromises()
    expect(wrapper.get('button.btn-primary').attributes('disabled')).toBeUndefined()
  })
  it('keeps the dialog open on a QQ save failure and allows retry', async () => {
    opsAPI.updateQQNotificationConfig.mockRejectedValueOnce({ message: 'QQ save unavailable' })
    const wrapper = await openDialog()
    await flushPromises()
    await wrapper.getComponent(OpsQQNotificationCard).get('[role="switch"]').trigger('click')
    await wrapper.get('button.btn-primary').trigger('click')
    await flushPromises()
    expect(showError).toHaveBeenCalledWith('QQ save unavailable')
    expect(showSuccess).not.toHaveBeenCalled()
    expect(wrapper.emitted('close')).toBeUndefined()
    expect(opsAPI.updateMetricThresholds).not.toHaveBeenCalled()
    expect(wrapper.get('button.btn-primary').attributes('disabled')).toBeUndefined()
    await wrapper.get('button.btn-primary').trigger('click')
    await flushPromises()
    expect(wrapper.emitted('saved')).toHaveLength(1)
  })
  it('does not save when loading QQ configuration fails and allows refresh', async () => {
    opsAPI.getQQNotificationConfig.mockRejectedValueOnce(new Error('QQ unavailable'))
    const wrapper = await openDialog()
    await flushPromises()
    expect(wrapper.get('button.btn-primary').attributes('disabled')).toBeDefined()
    await (wrapper.vm as unknown as View).saveAllSettings()
    expect(opsAPI.updateQQNotificationConfig).not.toHaveBeenCalled()
    expect(opsAPI.updateMetricThresholds).not.toHaveBeenCalled()
    await wrapper.getComponent(OpsQQNotificationCard).get('button').trigger('click')
    await flushPromises()
    expect(wrapper.get('button.btn-primary').attributes('disabled')).toBeUndefined()
  })
  it('blocks duplicate saves while the QQ request is in flight', async () => {
    const pending = deferred<object>()
    opsAPI.updateQQNotificationConfig.mockReturnValueOnce(pending.promise)
    const wrapper = await openDialog()
    await flushPromises()
    await wrapper.get('button.btn-primary').trigger('click')
    expect(wrapper.get('button.btn-primary').attributes('disabled')).toBeDefined()
    expect(wrapper.getComponent(OpsQQNotificationCard).get('fieldset').attributes('disabled')).toBeDefined()
    await (wrapper.vm as unknown as View).saveAllSettings()
    expect(opsAPI.updateQQNotificationConfig).toHaveBeenCalledOnce()
    expect(wrapper.emitted('close')).toBeUndefined()
    pending.resolve({ enabled: true, provider: 'qq_official', app_id: '100001', group_openid: 'GROUP_A1' })
    await flushPromises()
    expect(wrapper.emitted('saved')).toHaveLength(1)
  })
})

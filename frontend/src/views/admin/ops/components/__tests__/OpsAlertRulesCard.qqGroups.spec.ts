import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, shallowMount } from '@vue/test-utils'
import { ref } from 'vue'
import OpsAlertRulesCard from '../OpsAlertRulesCard.vue'

const mocks = vi.hoisted(() => ({
  list: vi.fn(), create: vi.fn(), update: vi.fn(), webhook: vi.fn(), error: vi.fn()
}))
vi.mock('vue-i18n', async (importOriginal) => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({ t: (key: string) => key })
}))
vi.mock('@vueuse/core', () => ({ useMediaQuery: () => ref(true) }))
vi.mock('@/api', () => ({ adminAPI: { groups: { getAll: vi.fn().mockResolvedValue([]) } } }))
vi.mock('@/api/admin/ops', () => ({ opsAPI: {
  listAlertRules: mocks.list, createAlertRule: mocks.create,
  updateAlertRule: mocks.update, getQQWebhookStatus: mocks.webhook
} }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError: mocks.error, showSuccess: vi.fn() }) }))

enableAutoUnmount(afterEach)

async function mountEditor(edit = false) {
  const wrapper = shallowMount(OpsAlertRulesCard, {
    global: { stubs: { BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /><slot name="footer" /></div>' } } }
  })
  await flushPromises()
  await wrapper.findAll('button').find(b => b.text() === (edit ? 'common.edit' : 'admin.ops.alertRules.create'))!.trigger('click')
  if (!edit) {
    await wrapper.get('input[type="text"]').setValue('QQ alert')
    await wrapper.get('[data-testid="alert-notify-qq"]').setValue(true)
  }
  return wrapper
}

describe('alert rule QQ recipient groups', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mocks.list.mockResolvedValue([])
    mocks.webhook.mockResolvedValue({ groups: [{ group_openid: 'GROUP_A' }, { group_openid: 'GROUP_B' }] })
  })

  it('saves multiple groups, trims separators and removes duplicates', async () => {
    const wrapper = await mountEditor()
    await wrapper.get('#alert-qq-groups').setValue(' GROUP_A, GROUP_B\nGROUP_A，GROUP_C ')
    await wrapper.findAll('button').find(b => b.text() === 'common.save')!.trigger('click')
    expect(mocks.create).toHaveBeenCalledWith(expect.objectContaining({ notify_qq: true, qq_group_openids: ['GROUP_A', 'GROUP_B', 'GROUP_C'] }))
  })

  it('selects and deselects discovered groups while preserving manual entries', async () => {
    const wrapper = await mountEditor()
    await wrapper.get('#alert-qq-groups').setValue('MANUAL_GROUP')
    await wrapper.findAll('button').find(b => b.text() === 'admin.ops.qq.refreshGroups')!.trigger('click')
    await flushPromises()
    await wrapper.get('input[value="GROUP_A"]').setValue(true)
    await wrapper.get('input[value="GROUP_B"]').setValue(true)
    await wrapper.get('input[value="GROUP_A"]').setValue(false)
    await wrapper.findAll('button').find(b => b.text() === 'common.save')!.trigger('click')
    expect(mocks.create).toHaveBeenCalledWith(expect.objectContaining({ qq_group_openids: ['MANUAL_GROUP', 'GROUP_B'] }))
  })

  it('loads saved groups and allows clearing them to use the global default', async () => {
    mocks.list.mockResolvedValue([{
      id: 7, name: 'Saved alert', enabled: true, metric_type: 'error_rate', operator: '>', threshold: 1,
      window_minutes: 1, sustained_minutes: 2, cooldown_minutes: 10, severity: 'P1',
      notify_email: false, notify_qq: true, qq_group_openids: ['GROUP_A', 'GROUP_B']
    }])
    const wrapper = await mountEditor(true)
    expect((wrapper.get('#alert-qq-groups').element as HTMLTextAreaElement).value).toBe('GROUP_A\nGROUP_B')
    await wrapper.get('#alert-qq-groups').setValue('')
    await wrapper.findAll('button').find(b => b.text() === 'common.save')!.trigger('click')
    expect(mocks.update).toHaveBeenCalledWith(7, expect.objectContaining({ qq_group_openids: [] }))
  })

  it.each(['../invalid', 'a'.repeat(129), Array.from({ length: 101 }, (_, i) => `GROUP_${i}`).join('\n')])('rejects invalid or excessive groups', async value => {
    const wrapper = await mountEditor()
    await wrapper.get('#alert-qq-groups').setValue(value)
    await wrapper.findAll('button').find(b => b.text() === 'common.save')!.trigger('click')
    expect(mocks.create).not.toHaveBeenCalled()
    expect(mocks.error).toHaveBeenCalledWith('admin.ops.qq.invalidRuleGroups')
  })

  it('keeps manual entry usable when group discovery fails', async () => {
    mocks.webhook.mockRejectedValue(new Error('offline'))
    const wrapper = await mountEditor()
    await wrapper.findAll('button').find(b => b.text() === 'admin.ops.qq.refreshGroups')!.trigger('click')
    await flushPromises()
    await wrapper.get('#alert-qq-groups').setValue('MANUAL_GROUP')
    await wrapper.findAll('button').find(b => b.text() === 'common.save')!.trigger('click')
    expect(mocks.create).toHaveBeenCalledWith(expect.objectContaining({ qq_group_openids: ['MANUAL_GROUP'] }))
  })
})

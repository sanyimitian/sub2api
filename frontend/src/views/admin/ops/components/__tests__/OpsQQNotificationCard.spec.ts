import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, shallowMount } from '@vue/test-utils'
import OpsQQNotificationCard from '../OpsQQNotificationCard.vue'

const mocks = vi.hoisted(() => ({ get: vi.fn(), update: vi.fn(), test: vi.fn(), error: vi.fn(), success: vi.fn() }))
vi.mock('vue-i18n', async (importOriginal) => ({
  ...await importOriginal<typeof import('vue-i18n')>(), useI18n: () => ({ t: (key: string) => key })
}))
vi.mock('@/api/admin/ops', () => ({ opsAPI: {
  getQQNotificationConfig: mocks.get, updateQQNotificationConfig: mocks.update, testQQNotification: mocks.test
} }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError: mocks.error, showSuccess: mocks.success }) }))
enableAutoUnmount(afterEach)

describe('QQ notification settings', () => {
  const config = { enabled: true, api_url: 'http://localhost:3000', group_id: '123456', access_token_configured: true }
  beforeEach(() => {
    vi.clearAllMocks()
    mocks.get.mockResolvedValue({ ...config })
    mocks.update.mockResolvedValue({ ...config })
    mocks.test.mockResolvedValue(undefined)
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
})

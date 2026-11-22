import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import ModelPlazaSync from '../ModelPlazaSync.vue'

const mocks = vi.hoisted(() => ({ sync: vi.fn(), channels: vi.fn(), success: vi.fn(), error: vi.fn() }))
vi.mock('@/api/modelPlaza', () => ({ syncModelPlaza: mocks.sync, syncAvailableChannelModels: mocks.channels }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess: mocks.success, showError: mocks.error }) }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

describe('ModelPlazaSync', () => {
  beforeEach(() => vi.clearAllMocks())

  it('syncs available channels separately and only when clicked', async () => {
    mocks.channels.mockResolvedValue({ group_count: 1, model_count: 3 })
    const wrapper = mount(ModelPlazaSync, { props: { target: 'channels' } })
    await flushPromises()
    expect(mocks.channels).not.toHaveBeenCalled()
    await wrapper.get('button').trigger('click')
    await flushPromises()
    expect(mocks.channels).toHaveBeenCalledOnce()
    expect(mocks.sync).not.toHaveBeenCalled()
    expect(mocks.success).toHaveBeenCalledOnce()
    wrapper.unmount()
  })

  it('only syncs on click and prevents duplicate clicks while pending', async () => {
    let finish!: (value: unknown) => void
    mocks.sync.mockReturnValue(new Promise(resolve => { finish = resolve }))
    const wrapper = mount(ModelPlazaSync)
    await flushPromises()
    expect(mocks.sync).not.toHaveBeenCalled()
    const button = wrapper.get('button')
    await button.trigger('click')
    expect(mocks.sync).toHaveBeenCalledTimes(1)
    expect(button.attributes('disabled')).toBeDefined()
    await button.trigger('click')
    expect(mocks.sync).toHaveBeenCalledTimes(1)
    finish({ group_count: 2, model_count: 6 })
    await flushPromises()
    expect(mocks.success).toHaveBeenCalledOnce()
    expect(button.attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })

  it('reports failure and allows an explicit retry without auto retrying', async () => {
    mocks.sync.mockRejectedValue(new Error('failed'))
    const wrapper = mount(ModelPlazaSync)
    await wrapper.get('button').trigger('click')
    await flushPromises()
    expect(mocks.error).toHaveBeenCalledOnce()
    expect(mocks.success).not.toHaveBeenCalled()
    expect(mocks.sync).toHaveBeenCalledTimes(1)
    expect(wrapper.get('button').attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })
})

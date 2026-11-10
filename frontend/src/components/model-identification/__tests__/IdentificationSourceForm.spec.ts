import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import IdentificationSourceForm from '../IdentificationSourceForm.vue'
import { modelIdentificationAPI } from '@/api/modelIdentification'
vi.mock('vue-i18n', async (importOriginal) => ({
  ...await importOriginal<typeof import('vue-i18n')>(), useI18n: () => ({ t: (key: string) => key })
}))
vi.mock('@/api/modelIdentification', () => ({ modelIdentificationAPI: { source: vi.fn(), saveSource: vi.fn() } }))
enableAutoUnmount(afterEach)
beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(modelIdentificationAPI.source).mockResolvedValue({ source_url: '', read_only: false })
})
describe('鉴定结果来源配置', () => {
  it('提供可复制的公开地址并保存远程来源', async () => {
    vi.mocked(modelIdentificationAPI.saveSource).mockResolvedValue({ source_url: 'https://primary.example/api/v1/model-identification', read_only: true })
    const wrapper = mount(IdentificationSourceForm)
    await flushPromises()
    expect((wrapper.get('input[readonly]').element as HTMLInputElement).value).toContain('/api/v1/model-identification/groups')
    await wrapper.get('input[name="source_url"]').setValue('https://primary.example/api/v1/model-identification/groups')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(modelIdentificationAPI.saveSource).toHaveBeenCalledWith('https://primary.example/api/v1/model-identification/groups')
    expect(wrapper.emitted('saved')).toHaveLength(1)
  })
  it('复用站点分享主站地址，清空输入恢复本站', async () => {
    vi.mocked(modelIdentificationAPI.source).mockResolvedValue({ source_url: 'https://primary.example/api/v1/model-identification', read_only: true })
    const wrapper = mount(IdentificationSourceForm)
    await flushPromises()
    expect((wrapper.get('input[readonly]').element as HTMLInputElement).value).toBe('https://primary.example/api/v1/model-identification/groups')
    await wrapper.get('input[name="source_url"]').setValue('')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(modelIdentificationAPI.saveSource).toHaveBeenCalledWith('')
  })
  it('加载失败不能用空值覆盖既有来源', async () => {
    vi.mocked(modelIdentificationAPI.source).mockRejectedValue(new Error('offline'))
    const wrapper = mount(IdentificationSourceForm)
    await flushPromises()
    await wrapper.get('form').trigger('submit')
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeDefined()
    expect(modelIdentificationAPI.saveSource).not.toHaveBeenCalled()
  })
  it('保存失败保留输入并显示错误', async () => {
    vi.mocked(modelIdentificationAPI.saveSource).mockRejectedValue(new Error('来源无法访问'))
    const wrapper = mount(IdentificationSourceForm)
    await flushPromises()
    await wrapper.get('input[name="source_url"]').setValue('https://primary.example')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toContain('来源无法访问')
    expect(wrapper.emitted('saved')).toBeUndefined()
  })
})

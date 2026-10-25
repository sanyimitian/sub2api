import { afterEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, ref } from 'vue'
import { defaultConversationQualityConfig } from '@/utils/openaiSessionPool'
import { flushPromises, mount } from '@vue/test-utils'
import OpenAISessionPoolFields from '../OpenAISessionPoolFields.vue'
import { openAISessionPoolModelsForSave, parseOpenAISessionPoolModels, readOpenAISessionPoolModels } from '@/utils/openaiSessionPool'

const { getAvailableModels, syncUpstreamModelsPreview } = vi.hoisted(() => ({
  getAvailableModels: vi.fn(),
  syncUpstreamModelsPreview: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: { accounts: { getAvailableModels, syncUpstreamModelsPreview } }
}))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

afterEach(() => vi.clearAllMocks())

describe('备用会话配置', () => {
  it('从现有账号读取模型并在下拉中多选', async () => {
    getAvailableModels.mockResolvedValue([{ id: 'gpt-6-sol' }, { id: 'gpt-5.6-sol' }])
    const wrapper = mount(OpenAISessionPoolFields, {
      props: { enabled: true, models: ['gpt-6-sol'], accountId: 7, fallbackModels: ['fallback-model'] }
    })
    await flushPromises()
    expect(getAvailableModels).toHaveBeenCalledWith(7)
    await wrapper.get('[data-testid="openai-session-pool-models"]').trigger('click')
    const checkboxes = wrapper.findAll('input[type="checkbox"]')
    expect(checkboxes).toHaveLength(2)
    await checkboxes[1].setValue(true)
    expect(wrapper.emitted('update:models')?.at(-1)).toEqual([['gpt-6-sol', 'gpt-5.6-sol']])
    expect(wrapper.text()).toContain('gpt-5.6-sol')
  })

  it('新建账号通过当前凭据预览上游模型', async () => {
    syncUpstreamModelsPreview.mockResolvedValue({ models: ['gpt-6-sol', 'custom-model'] })
    mount(OpenAISessionPoolFields, {
      props: {
        enabled: true,
        models: ['gpt-6-sol'],
        previewCredentials: { platform: 'openai', type: 'apikey', api_key: 'test-key' },
        fallbackModels: ['catalog-model']
      }
    })
    await flushPromises()
    expect(syncUpstreamModelsPreview).toHaveBeenCalledWith({ platform: 'openai', type: 'apikey', api_key: 'test-key' })
  })

  it.each([
    [['gpt-6-sol', 'gpt-6.1-sol'], 'gpt-6-sol'],
    [['custom-model', 'gpt-6.1-sol'], 'gpt-6.1-sol'],
  ])('编辑账号按可用模型选择默认值：%j', async (available, expected) => {
    getAvailableModels.mockResolvedValue(available.map(id => ({ id })))
    const wrapper = mount(OpenAISessionPoolFields, { props: { enabled: true, models: ['gpt-6-sol'], accountId: 7 } })
    await flushPromises()
    expect(wrapper.emitted('update:models')?.at(-1)).toEqual([[expected]])
    wrapper.unmount()
  })

  it('保留显式选择的自定义模型，读取失败时也保留原值', async () => {
    getAvailableModels.mockResolvedValue([{ id: 'gpt-6.1-sol' }])
    const wrapper = mount(OpenAISessionPoolFields, { props: { enabled: true, models: ['custom-model'], accountId: 7 } })
    await flushPromises()
    expect(wrapper.emitted('update:models')).toBeUndefined()
    wrapper.unmount()
    getAvailableModels.mockRejectedValue(new Error('network'))
    const failed = mount(OpenAISessionPoolFields, { props: { enabled: true, models: ['gpt-6-sol'], accountId: 7 } })
    await flushPromises()
    expect(failed.emitted('update:models')).toBeUndefined()
    failed.unmount()
  })

  it('模型读取兼容旧账号，保存时去重和去空格', () => {
    expect(readOpenAISessionPoolModels()).toEqual(['gpt-6-sol'])
    expect(readOpenAISessionPoolModels({ openai_session_pool_models: ['model-a', 'model-b'] })).toEqual(['model-a', 'model-b'])
    expect(parseOpenAISessionPoolModels(' model-a \nmodel-b\nmodel-a\n')).toEqual(['model-a', 'model-b'])
    expect(parseOpenAISessionPoolModels('  ')).toEqual([])
    expect(openAISessionPoolModelsForSave(true, [' model-a ', 'model-a', 'model-b'])).toEqual(['model-a', 'model-b'])
    expect(openAISessionPoolModelsForSave(false, ['hidden'], { openai_session_pool_models: ['saved'] })).toEqual(['saved'])
  })
})

describe('对话探测开关依赖', () => {
  it('开启降智自动开启低延迟，关闭低延迟同时关闭降智', async () => {
    const Harness = defineComponent({
      components: { OpenAISessionPoolFields },
      setup() { return { enabled: ref(false), quality: ref(defaultConversationQualityConfig()) } },
      template: '<OpenAISessionPoolFields v-model:enabled="enabled" v-model:quality="quality" />'
    })
    const wrapper = mount(Harness, { global: { stubs: { ConversationQualityFields: true } } })
    await wrapper.get('[data-testid="openai-session-pool-quality-enabled"]').trigger('click')
    expect(wrapper.get('[data-testid="openai-session-pool-enabled"]').attributes('aria-checked')).toBe('true')
    expect(wrapper.get('[data-testid="openai-session-pool-quality-enabled"]').attributes('aria-checked')).toBe('true')
    await wrapper.get('[data-testid="openai-session-pool-enabled"]').trigger('click')
    expect(wrapper.get('[data-testid="openai-session-pool-quality-enabled"]').attributes('aria-checked')).toBe('false')
    await wrapper.get('[data-testid="openai-session-pool-enabled"]').trigger('click')
    expect(wrapper.get('[data-testid="openai-session-pool-quality-enabled"]').attributes('aria-checked')).toBe('false')
    wrapper.unmount()
  })
})

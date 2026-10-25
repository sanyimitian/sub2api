import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent, ref } from 'vue'
import ConversationQualityFields from '../ConversationQualityFields.vue'
import { CONVERSATION_CANDY_PROMPT, defaultConversationQualityConfig, readConversationQualityConfig, conversationQualityConfigForSave } from '@/utils/openaiSessionPool'

const { getAll, getModelAllowlistCandidates } = vi.hoisted(() => ({ getAll: vi.fn(), getModelAllowlistCandidates: vi.fn() }))
vi.mock('@/api/admin', () => ({ adminAPI: { groups: { getAll, getModelAllowlistCandidates } } }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
beforeEach(() => {
  vi.resetAllMocks()
  getAll.mockResolvedValue([{ id: 9, name: '判题组', status: 'active' }, { id: 10, name: '停用组', status: 'inactive' }])
  getModelAllowlistCandidates.mockResolvedValue(['judge-a', 'judge-b'])
})

function mountFields(groupID = 0) {
  const Harness = defineComponent({
    components: { ConversationQualityFields },
    setup() {
      const config = ref(defaultConversationQualityConfig())
      config.value.enabled = true
      config.value.judge.group_id = groupID
      config.value.judge.model_id = groupID ? 'saved-judge' : ''
      return { config }
    },
    template: '<form><ConversationQualityFields v-model="config" /></form>'
  })
  return mount(Harness)
}

describe('降智探测用户配置', () => {
  it('糖果题按可辨形状验证最少取数，恢复默认题时使用正确答案', async () => {
    expect(CONVERSATION_CANDY_PROMPT).toContain('不同的形状靠手感可以分辨')
    expect(CONVERSATION_CANDY_PROMPT).toContain('圆形 7 9 8')
    expect(CONVERSATION_CANDY_PROMPT).toContain('五角星形 7 6 4')
    // 枚举未配对的所有取法，记录其圆形、五角星形数量。
    const limits = [7, 9, 8, 7, 6, 4]
    const invalidShapeCounts = new Set<string>()
    function enumerate(counts: number[]) {
      if (counts.length < limits.length) {
        for (let n = 0; n <= limits[counts.length]; n++) enumerate([...counts, n])
        return
      }
      const [appleRound, peachRound, watermelonRound, appleStar, peachStar, watermelonStar] = counts
      if ((appleRound > 0 && peachStar > 0) || (peachRound > 0 && appleStar > 0)) return
      invalidShapeCounts.add(`${appleRound + peachRound + watermelonRound},${appleStar + peachStar + watermelonStar}`)
    }
    enumerate([])
    let minimumGuaranteedDraw = Infinity
    for (let round = 0; round <= 24; round++) {
      for (let star = 0; star <= 17; star++) {
        if (!invalidShapeCounts.has(`${round},${star}`)) {
          minimumGuaranteedDraw = Math.min(minimumGuaranteedDraw, round + star)
        }
      }
    }
    expect(invalidShapeCounts.has('9,12')).toBe(false)
    expect(minimumGuaranteedDraw).toBe(21)
    expect(defaultConversationQualityConfig().expected_answer).toBe(String(minimumGuaranteedDraw))
    const wrapper = mountFields()
    wrapper.vm.config.prompt = '自定义题'
    wrapper.vm.config.expected_answer = '自定义答案'
    await flushPromises()
    const reset = wrapper.findAll('button').find(button => button.text() === 'admin.accounts.sessionPool.useCandy')!
    await reset.trigger('click')
    expect(wrapper.vm.config.prompt).toBe(CONVERSATION_CANDY_PROMPT)
    expect(wrapper.vm.config.expected_answer).toBe('21')
    wrapper.unmount()
  })
  it('可编辑固定题并切换随机题，随机题不要求填写固定题目和答案', async () => {
    const wrapper = mountFields()
    await flushPromises()
    await wrapper.get('[data-testid="conversation-quality-question"]').setValue('自定义题')
    expect(wrapper.vm.config.prompt).toBe('自定义题')
    await wrapper.get('[data-testid="conversation-quality-kind"]').setValue('random_reasoning')
    expect(wrapper.find('[data-testid="conversation-quality-question"]').exists()).toBe(false)
    expect(wrapper.vm.config.question_kind).toBe('random_reasoning')
    await wrapper.get('[data-testid="conversation-quality-kind"]').setValue('candy')
    expect(wrapper.get('[data-testid="conversation-quality-question"]').element.value).toBe('自定义题')
    wrapper.unmount()
  })
  it('要求选择判题分组，并可加载候选模型或输入自定义模型', async () => {
    const wrapper = mountFields()
    await flushPromises()
    const group = wrapper.get<HTMLSelectElement>('[data-testid="conversation-quality-judge-group"]')
    expect(group.element.validity.valueMissing).toBe(true)
    expect(group.text()).not.toContain('停用组')
    await group.setValue('9')
    await flushPromises()
    expect(getModelAllowlistCandidates).toHaveBeenCalledWith(9)
    expect(wrapper.findAll('datalist option').map(x => x.attributes('value'))).toEqual(['judge-a', 'judge-b'])
    await wrapper.get('[data-testid="conversation-quality-judge-model"]').setValue('custom-judge')
    expect(wrapper.vm.config.judge).toMatchObject({ group_id: 9, model_id: 'custom-judge' })
    wrapper.unmount()
  })
  it('加载失败时保留已存配置并提供重试', async () => {
    getAll.mockRejectedValue(new Error('network'))
    getModelAllowlistCandidates.mockRejectedValue(new Error('network'))
    const wrapper = mountFields(9)
    await flushPromises()
    expect(wrapper.findAll('[role="alert"]')).toHaveLength(2)
    expect(wrapper.vm.config.judge).toMatchObject({ group_id: 9, model_id: 'saved-judge' })
    getAll.mockResolvedValue([{ id: 9, name: '判题组', status: 'active' }])
    getModelAllowlistCandidates.mockResolvedValue(['judge-a'])
    for (const button of wrapper.findAll('[role="alert"] button')) await button.trigger('click')
    await flushPromises()
    expect(wrapper.findAll('datalist option')).toHaveLength(1)
    wrapper.unmount()
  })
  it('兼容旧账号且保存时保证降智不能脱离低延迟开启', () => {
    expect(readConversationQualityConfig().enabled).toBe(false)
    const config = defaultConversationQualityConfig()
    config.enabled = true
    config.question_kind = 'random_reasoning'
    config.judge = { group_id: 9, model_id: 'custom-judge', prompt: '判题要求' }
    expect(readConversationQualityConfig({ openai_session_pool_quality: config })).toEqual(config)
    expect(conversationQualityConfigForSave(false, config).enabled).toBe(false)
    expect(conversationQualityConfigForSave(true, config)).toEqual(config)
    expect(config.enabled).toBe(true)
  })
})

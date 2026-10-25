export const DEFAULT_OPENAI_SESSION_POOL_MODEL = 'gpt-6-sol'

export function defaultOpenAISessionPoolModel(availableModels: string[]): string {
  return availableModels.includes(DEFAULT_OPENAI_SESSION_POOL_MODEL) ? DEFAULT_OPENAI_SESSION_POOL_MODEL : 'gpt-6.1-sol'
}

export function parseOpenAISessionPoolModels(value: string): string[] {
  return [...new Set(value.split(/\r?\n/).map(model => model.trim()).filter(Boolean))]
}

// 关闭时保留已保存的模型，不提交已经隐藏的编辑值。
export function openAISessionPoolModelsForSave(enabled: boolean, value: string[], extra?: Record<string, unknown>): string[] {
  return normalizeOpenAISessionPoolModels(enabled ? value : readOpenAISessionPoolModels(extra))
}

export function normalizeOpenAISessionPoolModels(value: unknown): string[] {
  if (!Array.isArray(value)) return [DEFAULT_OPENAI_SESSION_POOL_MODEL]
  return [...new Set(value.filter((model): model is string => typeof model === 'string').map(model => model.trim()).filter(Boolean))]
}

export function readOpenAISessionPoolModels(extra?: Record<string, unknown>): string[] {
  const values = extra?.openai_session_pool_models
  return normalizeOpenAISessionPoolModels(values)
}

export interface ConversationQualityConfig {
  enabled: boolean
  question_kind: 'candy' | 'random_reasoning'
  prompt: string
  expected_answer: string
  reasoning_effort: string
  judge: { group_id: number; model_id: string; prompt: string }
}

export const CONVERSATION_CANDY_PROMPT = `在一个黑色的袋子里放有三种口味的糖果，每种糖果有两种不同的形状（圆形和五角星形，不同的形状靠手感可以分辨）。现已知不同口味的糖和不同形状的数量统计如下表。参赛者需要在活动前决定摸出的糖果数目，那么，最少取出多少个糖果才能保证手中同时拥有不同形状的苹果味和桃子味的糖？（同时手中有圆形苹果味匹配五角星桃子味糖果，或者有圆形桃子味匹配五角星苹果味糖果都满足要求）
苹果味 桃子味 西瓜味
圆形 7 9 8
五角星形 7 6 4`

export const CONVERSATION_CANDY_EXPECTED_ANSWER = '29'

export function defaultConversationQualityConfig(): ConversationQualityConfig {
  return {
    enabled: false,
    question_kind: 'candy',
    prompt: CONVERSATION_CANDY_PROMPT,
    expected_answer: CONVERSATION_CANDY_EXPECTED_ANSWER,
    reasoning_effort: 'medium',
    judge: {
      group_id: 0,
      model_id: '',
      prompt: '只比较两个值：reference_answer（参考值）与 candidate_answer（候选值）。数值相同或语义等价返回 correct，明确不同返回 incorrect，无法确定返回 unknown。忽略单位、标点和措辞差异，不要引用或推测题目内容。'
    }
  }
}

export function readConversationQualityConfig(extra?: Record<string, unknown>): ConversationQualityConfig {
  const raw = extra?.openai_session_pool_quality
  const defaults = defaultConversationQualityConfig()
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) return defaults
  const value = raw as Partial<ConversationQualityConfig>
  return { ...defaults, ...value, enabled: value.enabled === true, judge: { ...defaults.judge, ...value.judge } }
}

export function conversationQualityConfigForSave(enabled: boolean, config: ConversationQualityConfig): ConversationQualityConfig {
  return { ...config, enabled: enabled && config.enabled, judge: { ...config.judge } }
}

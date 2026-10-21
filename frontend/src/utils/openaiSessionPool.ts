export const DEFAULT_OPENAI_SESSION_POOL_MODEL = 'gpt-6-sol'

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

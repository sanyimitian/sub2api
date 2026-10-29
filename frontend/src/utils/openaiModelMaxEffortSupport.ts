export type ModelMaxEffortSupport = Record<string, boolean>

export function normalizeModelMaxEffortSupport(value: unknown): ModelMaxEffortSupport {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return {}

  const support: ModelMaxEffortSupport = {}
  for (const [model, supported] of Object.entries(value)) {
    if (model && supported === false) support[model] = false
  }
  return support
}

export function buildModelMaxEffortSupport(
  value: unknown,
  configuredModels: string[]
): ModelMaxEffortSupport {
  const configured = new Set(configuredModels.map(model => model.trim()).filter(Boolean))
  return Object.fromEntries(
    Object.entries(normalizeModelMaxEffortSupport(value)).filter(([model]) => configured.has(model))
  )
}

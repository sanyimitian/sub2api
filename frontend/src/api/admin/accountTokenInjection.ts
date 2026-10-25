import { buildApiUrl } from '@/api/client'
import { ADMIN_UI_REQUEST_HEADER } from '@/api/adminUIRequest'

export interface TokenInjectionSample {
  index: number
  repeat: boolean
  local_tokens: number
  input_tokens: number
  cache_read_tokens: number
  cache_write_tokens: number
  output_tokens: number
  extra_tokens: number
  first_token_ms: number | null
  duration_ms: number
  response_model?: string
  raw_usage: Record<string, unknown>
}

export interface TokenInjectionResult {
  account_id: number
  requested_model: string
  upstream_model: string
  protocol: string
  tokenizer: string
  status: 'stable' | 'inconclusive'
  estimate_tokens: number | null
  slope: number
  max_residual: number
  repeat_delta: number
  warnings: string[]
  samples: TokenInjectionSample[]
}

export type TokenInjectionProtocol = 'auto' | 'responses' | 'chat'
export type TokenInjectionEvent =
  | { type: 'injection_start'; data: TokenInjectionResult }
  | { type: 'injection_progress'; data: { index: number; total: number } }
  | { type: 'injection_sample'; data: TokenInjectionSample }
  | { type: 'injection_complete'; data: TokenInjectionResult }
  | { type: 'error'; error?: string; code?: string }

// Never retry paid probes. Closing the panel aborts the upstream requests.
export async function streamTokenInjectionTest(
  accountID: number,
  modelID: string,
  protocol: TokenInjectionProtocol,
  onEvent: (event: TokenInjectionEvent) => void,
  signal: AbortSignal
): Promise<void> {
  const response = await fetch(buildApiUrl('/admin/accounts/' + accountID + '/token-injection-test'), {
    method: 'POST',
    headers: {
      Authorization: 'Bearer ' + (localStorage.getItem('auth_token') || ''),
      [ADMIN_UI_REQUEST_HEADER]: '1',
      'Content-Type': 'application/json',
      Accept: 'text/event-stream'
    },
    body: JSON.stringify({ model_id: modelID.trim(), protocol }),
    signal
  })
  if (!response.ok) throw new Error('HTTP ' + response.status)
  const reader = response.body?.getReader()
  if (!reader) throw new Error('No response stream')
  const decoder = new TextDecoder()
  const newline = '\n'
  let buffer = ''
  let dataLines: string[] = []
  let eventLength = 0
  let completed = false
  const flush = () => {
    if (!dataLines.length) return
    const event = JSON.parse(dataLines.join(newline)) as TokenInjectionEvent
    dataLines = []
    eventLength = 0
    if (event.type === 'error') throw new Error(event.error || 'Diagnostic failed')
    if (event.type === 'injection_complete') completed = true
    onEvent(event)
  }
  const line = (value: string) => {
    if (value.endsWith('\r')) value = value.slice(0, -1)
    if (value === '') flush()
    else if (value.startsWith('data:')) {
      eventLength += value.length
      if (eventLength > 1024 * 1024) throw new Error('Diagnostic event is too large')
      dataLines.push(value.slice(5).trimStart())
    }
  }
  try {
    while (true) {
      const { done, value } = await reader.read()
      if (done) break
      buffer += decoder.decode(value, { stream: true })
      if (buffer.length > 1024 * 1024) throw new Error('Diagnostic event is too large')
      const lines = buffer.split(newline)
      buffer = lines.pop() || ''
      for (const value of lines) line(value)
      if (completed) break
    }
    buffer += decoder.decode()
    if (buffer) line(buffer)
    flush()
    if (!completed) throw new Error('Diagnostic stream ended before completion')
  } finally {
    await reader.cancel().catch(() => undefined)
    reader.releaseLock()
  }
}

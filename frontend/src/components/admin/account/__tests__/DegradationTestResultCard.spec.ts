import { afterEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, mount } from '@vue/test-utils'
import DegradationTestResultCard from '../DegradationTestResultCard.vue'
import { extractDegradationTestHtml, buildDegradationTestPreview } from '@/utils/degradationTestPreview'
import type { DegradationTestResult } from '@/types'

vi.mock('vue-i18n', async (importOriginal) => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({ t: (key: string) => key })
}))
enableAutoUnmount(afterEach)

describe('generated HTML results', () => {
  const html = '<!DOCTYPE html><html><head><base href="https://example.org"><meta http-equiv="refresh" content="0;url=https://example.org"></head><body><svg><circle r="5" /></svg><script>animate()</script></body></html>'

  it.each([undefined, '', 'high', 'xhigh'] as const)('shows the saved reasoning effort snapshot %s', effort => {
    const result = { id: 7, model_id: 'model', reasoning_effort: effort, status: 'success', latency_ms: 123, started_at: '2026-10-24T00:00:00Z' } as DegradationTestResult
    const wrapper = mount(DegradationTestResultCard, { props: { result } })
    expect(wrapper.get('[data-testid="result-reasoning-effort"]').text()).toBe(`admin.degradationTests.reasoningEffortSnapshot: admin.degradationTests.effort.${effort || 'default'}`)
  })

  it('extracts complete HTML from fenced prose and preserves raw text separately', () => {
    expect(extractDegradationTestHtml(`生成结果：\n\`\`\`html\n${html}\n\`\`\`\n以上是代码。`)).toBe(html)
    expect(extractDegradationTestHtml('ordinary text response')).toBeNull()
    expect(extractDegradationTestHtml('<html><body>unfinished')).toBeNull()
    expect(extractDegradationTestHtml('<svg><circle r="5" /></svg>')).toContain('<svg>')
  })

  it('puts a restrictive policy first and removes redirects from the preview document', () => {
    const doc = new DOMParser().parseFromString(buildDegradationTestPreview(html), 'text/html')
    expect(doc.head.firstElementChild?.getAttribute('http-equiv')).toBe('Content-Security-Policy')
    expect(doc.head.firstElementChild?.getAttribute('content')).toContain("connect-src 'none'")
    expect(doc.head.firstElementChild?.getAttribute('content')).toContain("form-action 'none'")
    expect(doc.querySelector('base, meta[http-equiv="refresh"]')).toBeNull()
    expect(doc.querySelector('svg')).not.toBeNull()
  })

  it('keeps the source inert during preparation while retaining styles, scripts, and SVG', () => {
    const parse = vi.spyOn(DOMParser.prototype, 'parseFromString')
    let preview: string
    try {
      preview = buildDegradationTestPreview('<!doctype html><!-- generated --><html lang="zh-CN" class="ocean" data-caption="ride > rest"><head><title>Ride</title><style>svg { width: 100% }</style></head><body class="seaside" style="background: skyblue"><svg><circle r="5" /></svg><img src="https://example.org/image.png"><script>animate()</script></body></html>')
      expect(parse).not.toHaveBeenCalled()
    } finally {
      parse.mockRestore()
    }
    const doc = new DOMParser().parseFromString(preview!, 'text/html')
    expect(doc.querySelector('title')?.textContent).toBe('Ride')
    expect(doc.querySelector('style')?.textContent).toContain('width: 100%')
    expect(doc.querySelector('script')?.textContent).toBe('animate()')
    expect(doc.querySelector('svg circle')).not.toBeNull()
    expect(doc.documentElement.lang).toBe('zh-CN')
    expect(doc.documentElement.className).toBe('ocean')
    expect(doc.documentElement.getAttribute('data-caption')).toBe('ride > rest')
    expect(doc.body.className).toBe('seaside')
    expect(doc.body.style.background).toBe('skyblue')
    expect(doc.head.firstElementChild?.getAttribute('http-equiv')).toBe('Content-Security-Policy')
  })

  it('copies the parent CSP nonce only to inline scripts without changing the response', () => {
    const parentScript = document.createElement('script')
    parentScript.nonce = 'preview-test-nonce'
    document.head.appendChild(parentScript)
    const source = '<html><body><script>animate()</script><script src="https://example.org/app.js"></script><svg><script href="https://example.org/svg.js"></script><script xmlns:xlink="http://www.w3.org/1999/xlink" xlink:href="https://example.org/legacy.js"></script></svg></body></html>'
    try {
      const doc = new DOMParser().parseFromString(buildDegradationTestPreview(source), 'text/html')
      expect(doc.querySelector('script:not([src])')?.getAttribute('nonce')).toBe('preview-test-nonce')
      expect(doc.querySelector('script[src]')?.hasAttribute('nonce')).toBe(false)
      expect(doc.querySelector('script[href]')?.hasAttribute('nonce')).toBe(false)
      expect(doc.querySelector('svg script:last-child')?.hasAttribute('nonce')).toBe(false)
      expect(source).not.toContain('nonce=')
      expect(doc.head.firstElementChild?.getAttribute('content')).toContain("script-src 'unsafe-inline'")
    } finally {
      parentScript.remove()
    }
  })

  it('only opens preview on demand, isolates its origin, and shows errors with partial output', async () => {
    const result = { id: 7, model_id: 'text-model', prompt: 'animate', response_text: html, error_message: 'upstream timed out', status: 'failed', latency_ms: 123, started_at: '2026-10-24T00:00:00Z' } as DegradationTestResult
    const wrapper = mount(DegradationTestResultCard, { props: { result } })
    expect(wrapper.get('[role="alert"]').text()).toBe('upstream timed out')
    expect(wrapper.text()).toContain(html)
    expect(wrapper.find('iframe').exists()).toBe(false)
    await wrapper.get('[data-testid="preview"]').trigger('click')
    expect(wrapper.get('iframe').attributes('sandbox')).toBe('allow-scripts')
    expect(wrapper.get('iframe').attributes('srcdoc')).toContain('Content-Security-Policy')
    expect(wrapper.find('script').exists()).toBe(false)
    await wrapper.get('[data-testid="preview"]').trigger('click')
    expect(wrapper.find('iframe').exists()).toBe(false)
  })
})

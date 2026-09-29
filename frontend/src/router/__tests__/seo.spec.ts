import { afterEach, describe, expect, it } from 'vitest'
import { applyRouteSEO } from '../seo'

describe('route SEO metadata', () => {
  afterEach(() => {
    document.head.innerHTML = ''
  })

  it('sets the approved Chinese home title, description, social metadata, and canonical URL', () => {
    document.head.innerHTML = '<meta name="robots" content="noindex,follow">'

    applyRouteSEO('Home')

    expect(document.title).toBe('codebot.one（codebot API）- 稳定高速的 AI API 中转站')
    expect(document.querySelector('meta[name="description"]')?.getAttribute('content')).toContain('为开发者及企业提供稳定、高速的 AI API 中转站')
    expect(document.querySelector('meta[property="og:url"]')?.getAttribute('content')).toBe('https://codebot.one/home')
    expect(document.querySelector('link[rel="canonical"]')?.getAttribute('href')).toBe('https://codebot.one/home')
    expect(document.querySelector('meta[name="robots"]')?.getAttribute('content')).toBe('index,follow')
  })

  it('marks routes outside the public SEO set as non-indexable', () => {
    document.head.innerHTML = '<link rel="canonical" href="https://codebot.one/home"><meta name="robots" content="index,follow">'

    applyRouteSEO('Login')

    expect(document.querySelector('meta[name="robots"]')?.getAttribute('content')).toBe('noindex,follow')
    expect(document.querySelector('link[rel="canonical"]')).toBeNull()
  })
})

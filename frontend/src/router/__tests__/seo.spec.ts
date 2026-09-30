import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { afterEach, describe, expect, it } from 'vitest'
import { applyRouteSEO, getRouteSEO } from '../seo'

describe('route SEO metadata', () => {
  afterEach(() => {
    document.head.innerHTML = ''
  })

  it('sets the approved Chinese home title, description, social metadata, and canonical URL', () => {
    document.head.innerHTML = '<meta name="robots" content="noindex,follow">'

    applyRouteSEO('Home')

    expect(document.title).toBe('codebot.one（codebot API）- 稳定高速的 AI API 中转站')
    expect(document.querySelector('meta[name="description"]')?.getAttribute('content')).toBe(
      'codebot.one(codebot API) 为开发者及企业提供稳定、高速的 AI API 中转站，支持通过统一接口接入多种主流大模型。',
    )
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

  it('不再为旧首页入口声明独立的搜索信息', () => {
    expect(getRouteSEO('HubHome')).toBeNull()
  })

  it('站点地图仅收录现有正式首页，并保留教程地址', () => {
    const sitemap = readFileSync(resolve(process.cwd(), 'public/sitemap.xml'), 'utf8')
    const xml = new DOMParser().parseFromString(sitemap, 'application/xml')
    const urls = [...xml.querySelectorAll('loc')].map((element) => element.textContent)

    expect(xml.querySelector('parsererror')).toBeNull()
    expect(urls).toEqual([
      getRouteSEO('Home')?.canonical,
      'https://codebot.one/guide.html',
      'https://codebot.one/guide/api-key.html',
      'https://codebot.one/guide/cc-switch.html',
      'https://codebot.one/guide/claude-code.html',
      'https://codebot.one/guide/codex.html',
    ])
    expect(new Set(urls).size).toBe(urls.length)
  })
})

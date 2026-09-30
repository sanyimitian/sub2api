import { readFileSync, existsSync } from 'node:fs'
import { resolve } from 'node:path'
import { JSDOM } from 'jsdom'
import { describe, expect, it, vi } from 'vitest'

const publicDirectory = resolve(process.cwd(), 'public')
const guideScript = readFileSync(resolve(publicDirectory, 'guide-assets/guide.js'), 'utf8')
const topics = ['api-key', 'cc-switch', 'claude-code', 'codex']

function openGuide(slug: string, mobile = false) {
  const html = readFileSync(resolve(publicDirectory, `guide/${slug}.html`), 'utf8')
  const browser = new JSDOM(html, {
    url: `https://codebot.one/guide/${slug}.html`,
    runScripts: 'outside-only',
    pretendToBeVisual: true,
  })
  browser.window.matchMedia = vi.fn((query: string) => ({
    matches: mobile && query.includes('max-width'),
    media: query,
    onchange: null,
    addListener: vi.fn(),
    removeListener: vi.fn(),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    dispatchEvent: vi.fn(),
  }))
  return browser
}

describe('独立接入教程', () => {
  it.each(topics)('%s 保留静态正文、原版资源和完整指南入口', (slug) => {
    const browser = openGuide(slug)
    try {
      const page = browser.window.document
      expect(page.querySelectorAll('h1')).toHaveLength(1)
      expect(page.querySelector('link[rel="canonical"]')?.getAttribute('href'))
        .toBe(`https://codebot.one/guide/${slug}.html`)
      expect(page.querySelector('meta[property="og:url"]')?.getAttribute('content'))
        .toBe(`https://codebot.one/guide/${slug}.html`)
      expect(page.querySelector('meta[name="robots"]')?.getAttribute('content')).toBe('index,follow')
      expect(page.querySelector('link[rel="stylesheet"]')?.getAttribute('href')).toBe('/guide-assets/guide.css')
      expect(page.querySelector('script[src]')?.getAttribute('src')).toBe('/guide-assets/guide.js')
      expect(page.querySelector('.topic-nav a[aria-current="page"]')?.getAttribute('href'))
        .toBe(`/guide/${slug}.html`)
      expect(page.querySelector('.guide-index-link')?.getAttribute('href')).toBe('/guide.html')
      expect(page.querySelector('[data-theme-toggle]')).not.toBeNull()
      const sectionLinks = [...page.querySelectorAll<HTMLAnchorElement>('.toc a[data-section]')]
      expect(sectionLinks).toHaveLength(4)
      for (const link of sectionLinks) {
        expect(page.getElementById(link.dataset.section!)).not.toBeNull()
        expect(link.hash).toBe(`#${link.dataset.section}`)
      }
      const ids = [...page.querySelectorAll('[id]')].map((element) => element.id)
      expect(new Set(ids).size).toBe(ids.length)
      for (const asset of page.querySelectorAll('img[src], link[href], script[src]')) {
        const path = asset.getAttribute('src') || asset.getAttribute('href') || ''
        if (path.startsWith('/')) expect(existsSync(resolve(publicDirectory, path.slice(1)))).toBe(true)
      }
    } finally {
      browser.window.close()
    }
  })

  it.each(topics)('%s 可以切换主题并操作移动端目录', (slug) => {
    const browser = openGuide(slug, true)
    try {
      browser.window.eval(guideScript)
      const page = browser.window.document
      expect(page.documentElement.dataset.theme).toBe('light')
      page.querySelector<HTMLButtonElement>('[data-theme-toggle]')!.click()
      expect(page.documentElement.dataset.theme).toBe('dark')
      expect(browser.window.localStorage.getItem('llmbridge-guide-theme')).toBe('dark')
      expect(page.querySelector('[data-theme-label]')?.textContent).toBe('浅色模式')
      const menu = page.querySelector<HTMLButtonElement>('.menu-toggle')!
      const sidebar = page.querySelector('.sidebar')!
      expect(sidebar.getAttribute('aria-hidden')).toBe('true')
      menu.click()
      expect(menu.getAttribute('aria-expanded')).toBe('true')
      expect(sidebar.getAttribute('aria-hidden')).toBe('false')
      page.querySelector<HTMLAnchorElement>('.toc a[data-section]')!.click()
      expect(menu.getAttribute('aria-expanded')).toBe('false')
      expect(sidebar.getAttribute('aria-hidden')).toBe('true')
    } finally {
      browser.window.close()
    }
  })

  it('Claude Code 系统标签支持点击、键盘切换和命令复制', () => {
    const browser = openGuide('claude-code')
    try {
      const copy = vi.fn().mockResolvedValue(undefined)
      Object.defineProperty(browser.window.navigator, 'clipboard', { value: { writeText: copy } })
      browser.window.eval(guideScript)
      const page = browser.window.document
      const windows = page.querySelector<HTMLButtonElement>('[data-tab="windows"]')!
      const mac = page.querySelector<HTMLButtonElement>('[data-tab="mac"]')!
      const macPanel = page.querySelector<HTMLElement>('[data-panel="mac"]')!
      mac.click()
      expect(mac.getAttribute('aria-selected')).toBe('true')
      expect(macPanel.hidden).toBe(false)
      macPanel.querySelector<HTMLButtonElement>('.copy-button')!.click()
      expect(copy).toHaveBeenCalledWith(expect.stringContaining('export ANTHROPIC_BASE_URL="https://codebot.one"'))
      mac.dispatchEvent(new browser.window.KeyboardEvent('keydown', { key: 'ArrowLeft', bubbles: true }))
      expect(windows.getAttribute('aria-selected')).toBe('true')
      expect(macPanel.hidden).toBe(true)
    } finally {
      browser.window.close()
    }
  })

  it('图解教程使用原指南中的图片资源', () => {
    for (const slug of ['api-key', 'cc-switch', 'codex']) {
      const browser = openGuide(slug)
      try {
        const screenshots = browser.window.document.querySelectorAll('.screenshot img')
        expect(screenshots.length).toBeGreaterThan(0)
        for (const image of screenshots) {
          expect(image.getAttribute('alt')).toBeTruthy()
          expect(image.getAttribute('loading')).toBe('lazy')
        }
      } finally {
        browser.window.close()
      }
    }
  })
})

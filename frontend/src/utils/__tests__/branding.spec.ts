import { beforeEach, describe, expect, it } from 'vitest'
import { updateFavicon } from '@/utils/branding'

describe('updateFavicon', () => {
  beforeEach(() => {
    document.head.innerHTML = '<link rel="icon" href="/logo.svg">'
  })

  it('replaces the default favicon with the configured logo', () => {
    updateFavicon('https://example.com/custom-logo.png')

    const link = document.querySelector<HTMLLinkElement>('link[rel="icon"]')
    expect(link?.href).toBe('https://example.com/custom-logo.png')
  })

  it('ignores unsafe logo URLs', () => {
    updateFavicon('javascript:alert(1)')

    const link = document.querySelector<HTMLLinkElement>('link[rel="icon"]')
    expect(link?.getAttribute('href')).toBe('/logo.svg')
  })

  it('keeps the crawlable SEO favicon when a managed favicon is configured', () => {
    document.head.innerHTML = '<link rel="icon" type="image/png" href="/codebot-favicon.png?v=2"><meta name="codebot-seo-favicon" content="managed">'

    updateFavicon('data:image/png;base64,abc')

    const link = document.querySelector<HTMLLinkElement>('link[rel="icon"]')
    expect(link?.getAttribute('href')).toBe('/codebot-favicon.png?v=2')
  })
})

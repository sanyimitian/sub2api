import { afterEach, describe, expect, it, vi } from 'vitest'

describe('默认语言', () => {
  afterEach(() => {
    localStorage.clear()
    vi.resetModules()
  })

  it('首次访问默认显示中文，不随英文浏览器语言改成英文', async () => {
    Object.defineProperty(navigator, 'language', { configurable: true, value: 'en-US' })
    localStorage.clear()

    const { i18n } = await import('../index')

    expect(i18n.global.locale.value).toBe('zh')
  })

  it('保留用户已保存的英文偏好', async () => {
    localStorage.setItem('sub2api_locale', 'en')

    const { i18n } = await import('../index')

    expect(i18n.global.locale.value).toBe('en')
  })
})

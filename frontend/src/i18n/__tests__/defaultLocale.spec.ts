import { afterEach, describe, expect, it, vi } from 'vitest'

describe('默认语言', () => {
  afterEach(() => {
    localStorage.clear()
    vi.resetModules()
  })

  it('首次访问默认显示英文，不随中文浏览器语言改成中文', async () => {
    Object.defineProperty(navigator, 'language', { configurable: true, value: 'zh-CN' })
    localStorage.clear()

    const { i18n } = await import('../index')

    expect(i18n.global.locale.value).toBe('en')
  })

  it('保留用户已保存的中文偏好', async () => {
    localStorage.setItem('sub2api_locale', 'zh')

    const { i18n } = await import('../index')

    expect(i18n.global.locale.value).toBe('zh')
  })
})

import { afterEach, describe, expect, it, vi } from 'vitest'

describe('默认语言', () => {
  afterEach(() => {
    localStorage.clear()
    vi.restoreAllMocks()
    vi.resetModules()
  })

  it('首次访问默认显示中文，不随英文浏览器语言改成英文', async () => {
    vi.spyOn(navigator, 'language', 'get').mockReturnValue('en-US')
    localStorage.clear()

    const { i18n } = await import('../index')

    expect(i18n.global.locale.value).toBe('zh')
    expect(i18n.global.fallbackLocale.value).toBe('zh')
  })

  it.each(['zh', 'en'])('保留用户已保存的语言偏好：%s', async (locale) => {
    localStorage.setItem('sub2api_locale', locale)

    const { i18n } = await import('../index')

    expect(i18n.global.locale.value).toBe(locale)
  })

  it('无效的已保存语言回退到中文', async () => {
    localStorage.setItem('sub2api_locale', 'invalid')

    const { i18n } = await import('../index')

    expect(i18n.global.locale.value).toBe('zh')
  })
})

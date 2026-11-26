import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { getLocale, i18n, initI18n } from '@/i18n'

beforeEach(() => {
  localStorage.clear()
  i18n.global.locale.value = 'zh'
  document.documentElement.removeAttribute('lang')
})
afterEach(() => localStorage.clear())

describe('新用户默认语言', () => {
  it.each(['en', 'zh'])('首次访问使用站点设置 %s，并记住语言', async (language) => {
    await initI18n(language)
    expect(getLocale()).toBe(language)
    expect(document.documentElement.lang).toBe(language)
    expect(localStorage.getItem('sub2api_locale')).toBe(language)
    expect(Object.keys(i18n.global.getLocaleMessage(language)).length).toBeGreaterThan(0)
  })

  it.each([['zh', 'en'], ['en', 'zh']])('保留已有语言 %s，不被默认值 %s 覆盖', async (saved, configured) => {
    localStorage.setItem('sub2api_locale', saved)
    await initI18n(configured)
    expect(getLocale()).toBe(saved)
    expect(document.documentElement.lang).toBe(saved)
  })

  it('默认值变更只影响新访客', async () => {
    await initI18n('en')
    await initI18n('zh')
    expect(getLocale()).toBe('en')
    localStorage.clear()
    await initI18n('zh')
    expect(getLocale()).toBe('zh')
  })

  it('无效的本地偏好使用后台默认语言', async () => {
    localStorage.setItem('sub2api_locale', 'fr')
    await initI18n('en')
    expect(getLocale()).toBe('en')
  })

  it.each([undefined, 'fr'])('旧配置或未知默认语言保持原有中文默认值', async (language) => {
    await initI18n(language)
    expect(getLocale()).toBe('zh')
    expect(localStorage.getItem('sub2api_locale')).toBeNull()
    await initI18n('en')
    expect(getLocale()).toBe('en')
  })
})

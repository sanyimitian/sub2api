import { describe, expect, it } from 'vitest'
import { createI18n } from 'vue-i18n'
import zh from '../locales/zh/modelBenchmark'
import en from '../locales/en/modelBenchmark'

describe('评测文案的真实运行时翻译', () => {
  it('正确显示中文标签、变量和英文切换', () => {
    const i18n = createI18n({ legacy: false, locale: 'zh', messages: { zh: { modelBenchmark: zh }, en: { modelBenchmark: en } } })
    expect(i18n.global.t('modelBenchmark.adminTest')).toBe('管理员测试')
    expect(i18n.global.t('modelBenchmark.userTest')).toBe('用户自测')
    expect(i18n.global.t('modelBenchmark.loadedResults', { count: 24 })).toBe('已加载 24 条结果')
    i18n.global.locale.value = 'en'
    expect(i18n.global.t('modelBenchmark.loadedResults', { count: 48 })).toBe('48 results loaded')
    i18n.dispose()
  })
})

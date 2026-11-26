import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import { createI18n } from 'vue-i18n'
import RegionAccessBlocked from '../RegionAccessBlocked.vue'
import regionAccess from '@/i18n/locales/zh/regionAccess'

function mountBlocked(props = {}) {
  return mount(RegionAccessBlocked, {
    props,
    global: { plugins: [createI18n({ legacy: false, locale: 'zh', messages: { zh: { regionAccess } } })] },
  })
}

describe('地区登录受限提示', () => {
  it('使用已确认文案并移除法规原文链接，且没有可关闭后继续登录的操作', async () => {
    const wrapper = mountBlocked()
    expect(wrapper.text()).toContain('根据《生成式人工智能服务管理暂行办法》监管要求以及相关法律法规要求，本站暂不向中国大陆地区提供服务。')
    expect(wrapper.find('a[href*="cac.gov.cn"]').exists()).toBe(false)
    expect(wrapper.get('a[href="https://db-ip.com"]').exists()).toBe(true)
    expect(wrapper.findAll('button')).toHaveLength(1)
    await wrapper.get('button').trigger('click')
    expect(wrapper.emitted('retry')).toHaveLength(1)
    await wrapper.trigger('keydown', { key: 'Escape' })
    expect(wrapper.emitted('close')).toBeUndefined()
  })

  it('地区未知不误报大陆，检测过程中禁止重复提交', async () => {
    const wrapper = mountBlocked({ reason: 'REGION_LOOKUP_UNAVAILABLE', retrying: true })
    expect(wrapper.text()).toContain(regionAccess.unavailableTitle)
    expect(wrapper.text()).not.toContain(regionAccess.blockedIntro)
    expect(wrapper.find('details').exists()).toBe(false)
    expect(wrapper.get('button').attributes('disabled')).toBeDefined()
    await wrapper.get('button').trigger('click')
    expect(wrapper.emitted('retry')).toBeUndefined()
  })

  it.each(['MAINLAND_ACCESS_DENIED', 'REGION_LOOKUP_UNAVAILABLE'])('%s 时仅提供重新检测操作', async (reason) => {
    const wrapper = mountBlocked({ reason })
    expect(wrapper.findAll('button')).toHaveLength(1)
    expect(wrapper.get('button').text()).toBe(regionAccess.retry)
    await wrapper.get('button').trigger('click')
    expect(wrapper.emitted('retry')).toHaveLength(1)
    expect(wrapper.emitted('admin-login')).toBeUndefined()
  })
})

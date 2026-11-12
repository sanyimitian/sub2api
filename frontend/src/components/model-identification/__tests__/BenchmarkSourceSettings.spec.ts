import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import BenchmarkSourceSettings from '../BenchmarkSourceSettings.vue'
import { benchmarks } from '@/api/admin/model-benchmarks'
vi.mock('vue-i18n', async (original) => ({ ...await original<typeof import('vue-i18n')>(), useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess: vi.fn() }) }))
vi.mock('@/api/admin/model-benchmarks', () => ({ benchmarks: { source: vi.fn(), saveSource: vi.fn(), sites: vi.fn(), createSite: vi.fn(), revokeSite: vi.fn() } }))
enableAutoUnmount(afterEach)
beforeEach(() => {
  vi.resetAllMocks()
  vi.mocked(benchmarks.source).mockResolvedValue({ source_url: '', site_id: '', has_token: false })
  vi.mocked(benchmarks.sites).mockResolvedValue({ items: [] })
})
const render = () => mount(BenchmarkSourceSettings, { global: { stubs: { ConfirmDialog: true } } })
describe('掺水测试站点接入', () => {
  it('主站生成独立密钥并只在领取时显示', async () => {
    const site = { id: 'a'.repeat(32), name: '分站甲', created_at: '2026-11-12T00:00:00Z' }
    vi.mocked(benchmarks.createSite).mockResolvedValue({ site, token: 'once-only-secret' })
    const wrapper = render(); await flushPromises()
    expect((wrapper.get('input[readonly]').element as HTMLInputElement).value).toContain('/model-benchmarks/federation')
    await wrapper.get('[name="site_name"]').setValue('分站甲')
    await wrapper.findAll('form')[1].trigger('submit'); await flushPromises()
    expect(benchmarks.createSite).toHaveBeenCalledWith('分站甲', expect.any(AbortSignal))
    expect((wrapper.get('textarea[readonly]').element as HTMLTextAreaElement).value).toBe('once-only-secret')
    const saved = wrapper.findAll('button').find(b => b.text() === 'modelBenchmark.tokenSaved')!
    await saved.trigger('click')
    expect(wrapper.find('textarea[readonly]').exists()).toBe(false)
    expect(wrapper.text()).toContain('分站甲')
  })
  it('接入后清空输入的密钥并通知页面切换主站', async () => {
    const source = { source_url: 'https://main.example/api/v1/model-benchmarks/federation', site_id: 'a'.repeat(32), has_token: true }
    vi.mocked(benchmarks.saveSource).mockResolvedValue(source)
    const wrapper = render(); await flushPromises()
    await wrapper.get('[name="source_url"]').setValue('https://main.example')
    await wrapper.get('[name="site_token"]').setValue('private-token')
    await wrapper.findAll('form')[0].trigger('submit'); await flushPromises()
    expect(benchmarks.saveSource).toHaveBeenCalledWith('https://main.example', 'private-token', expect.any(AbortSignal))
    expect(wrapper.emitted('saved')?.[0]).toEqual([source])
    expect((wrapper.get('[name="site_token"]').element as HTMLInputElement).value).toBe('')
    expect(wrapper.find('[name="site_name"]').exists()).toBe(false)
  })
  it('接入站保留已有密钥，无需读取主站授权列表', async () => {
    const source = { source_url: 'https://main.example/api/v1/model-benchmarks/federation', site_id: 'a'.repeat(32), has_token: true }
    vi.mocked(benchmarks.source).mockResolvedValue(source)
    vi.mocked(benchmarks.saveSource).mockResolvedValue(source)
    const wrapper = render(); await flushPromises()
    expect(benchmarks.sites).not.toHaveBeenCalled()
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(benchmarks.saveSource).toHaveBeenCalledWith(source.source_url, '', expect.any(AbortSignal))
  })
  it('读取配置失败时禁止空配置覆盖并支持重试', async () => {
    vi.mocked(benchmarks.source).mockRejectedValueOnce(new Error('offline'))
    const wrapper = render(); await flushPromises()
    await wrapper.get('form').trigger('submit')
    expect(benchmarks.saveSource).not.toHaveBeenCalled()
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[role="alert"] button').trigger('click'); await flushPromises()
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeUndefined()
  })
  it('保存失败保留当前配置和输入', async () => {
    vi.mocked(benchmarks.saveSource).mockRejectedValue(new Error('主站验证失败'))
    const wrapper = render(); await flushPromises()
    await wrapper.get('[name="source_url"]').setValue('https://main.example')
    await wrapper.get('[name="site_token"]').setValue('private-token')
    await wrapper.findAll('form')[0].trigger('submit'); await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toContain('主站验证失败')
    expect(wrapper.emitted('saved')).toBeUndefined()
    expect((wrapper.get('[name="site_token"]').element as HTMLInputElement).value).toBe('private-token')
  })
})

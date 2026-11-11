import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import BenchmarkKeyPicker from '../BenchmarkKeyPicker.vue'
import { keysAPI } from '@/api/keys'
import type { ApiKey } from '@/types'

vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ user: { id: 7 } }) }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/api/keys', () => ({ keysAPI: { list: vi.fn(), getById: vi.fn() } }))
enableAutoUnmount(afterEach)
function key(id: number, overrides: Partial<ApiKey> = {}) {
  return { id, user_id: 7, name: `Key ${id}`, key: `secret-${id}`, status: 'active', quota: 0, quota_used: 0, expires_at: null, ...overrides } as ApiKey
}
const render = () => mount(BenchmarkKeyPicker, { props: { show: true }, global: { stubs: { BaseDialog: { template: '<div><slot /></div>' } } } })
beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(keysAPI.list).mockResolvedValue({ items: [key(1), key(2, { user_id: 8 }), key(3, { expires_at: '2020-01-01T00:00:00Z' })], total: 3, page: 1, page_size: 20, pages: 1 })
})
describe('benchmark own-key picker', () => {
  it('lists own metadata only and rechecks the chosen key', async () => {
    vi.mocked(keysAPI.getById).mockResolvedValue(key(1))
    const wrapper = render()
    await flushPromises()
    expect(keysAPI.list).toHaveBeenCalledWith(1, 20, { search: '', status: 'active' }, { signal: expect.any(AbortSignal) })
    expect(wrapper.find('[data-key-id="2"]').exists()).toBe(false)
    expect(wrapper.get('[data-key-id="3"]').attributes('disabled')).toBeDefined()
    expect(wrapper.html()).not.toContain('secret-1')
    await wrapper.get('[data-key-id="1"]').trigger('click')
    await flushPromises()
    expect(keysAPI.getById).toHaveBeenCalledWith(1)
    expect(wrapper.emitted('select')).toEqual([[{ id: 1, name: 'Key 1', key: 'secret-1' }]])
  })
  it('rejects a key revoked or transferred since the list was loaded', async () => {
    vi.mocked(keysAPI.getById).mockResolvedValue(key(1, { user_id: 8 }))
    const wrapper = render()
    await flushPromises()
    await wrapper.get('[data-key-id="1"]').trigger('click')
    await flushPromises()
    expect(wrapper.emitted('select')).toBeUndefined()
    expect(wrapper.get('[role="alert"]').text()).toBe('modelBenchmark.loadKeysError')
  })
  it('ignores late responses after closing the picker', async () => {
    let resolve!: (value: ApiKey) => void
    vi.mocked(keysAPI.getById).mockReturnValue(new Promise<ApiKey>(r => { resolve = r }))
    const wrapper = render()
    await flushPromises()
    await wrapper.get('[data-key-id="1"]').trigger('click')
    await wrapper.setProps({ show: false })
    resolve(key(1))
    await flushPromises()
    expect(wrapper.emitted('select')).toBeUndefined()
  })
})

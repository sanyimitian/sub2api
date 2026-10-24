import { afterEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, mount } from '@vue/test-utils'
import AccountActionMenu from '../AccountActionMenu.vue'
import type { Account } from '@/types'

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
enableAutoUnmount(afterEach)

describe('independent account test menu entries', () => {
  it.each([
    ['admin.scheduledTests.schedule', 'schedule', 'degradation'],
    ['admin.degradationTests.title', 'degradation', 'schedule']
  ])('dispatches %s to its own feature', async (label, event, otherEvent) => {
    const account = { id: 7, name: 'Test account', platform: 'openai', type: 'apikey' } as Account
    const wrapper = mount(AccountActionMenu, {
      props: { show: true, account, anchorRect: new DOMRect(100, 100, 24, 24) },
      global: { stubs: { teleport: true } }
    })
    const buttons = wrapper.findAll('button')
    expect(buttons.some(button => button.text() === 'admin.scheduledTests.schedule')).toBe(true)
    expect(buttons.some(button => button.text() === 'admin.degradationTests.title')).toBe(true)
    await buttons.find(button => button.text() === label)!.trigger('click')
    expect(wrapper.emitted(event)?.[0]).toEqual([account])
    expect(wrapper.emitted(otherEvent)).toBeUndefined()
    expect(wrapper.emitted('close')).toHaveLength(1)
  })
})

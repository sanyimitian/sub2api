import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { defineComponent } from 'vue'
import DownstreamAssessmentView from '../DownstreamAssessmentView.vue'
import UserAllowedGroupsModal from '@/components/admin/user/UserAllowedGroupsModal.vue'
import type { DownstreamAssessmentResult } from '@/api/admin/downstreamAssessment'
import type { AdminUser } from '@/types'
import { formatDateTime } from '@/utils/format'

const { getGroups, getAssessment, getUser, listGroups, updateUser, showError, showSuccess } = vi.hoisted(() => ({
  getGroups: vi.fn(), getAssessment: vi.fn(), getUser: vi.fn(), listGroups: vi.fn(),
  updateUser: vi.fn(), showError: vi.fn(), showSuccess: vi.fn()
}))
vi.mock('@/api/admin/groups', () => ({ getAllIncludingInactive: getGroups }))
vi.mock('@/api/admin/downstreamAssessment', () => ({ getDownstreamAssessment: getAssessment }))
vi.mock('@/api/admin/users', () => ({ getById: getUser }))
vi.mock('@/api/admin', () => ({ adminAPI: { groups: { list: listGroups }, users: { update: updateUser } } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError, showSuccess }) }))
vi.mock('vue-i18n', async () => {
  const { ref } = await import('vue')
  return {
    ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
    useI18n: () => ({ t: (key: string) => key, locale: ref('en') })
  }
})
vi.mock('@/components/layout/AppLayout.vue', () => ({ default: { template: '<main><slot /></main>' } }))

const GroupSelect = defineComponent({
  props: ['modelValue', 'options'],
  emits: ['update:modelValue'],
  template: `<select :value="modelValue" @change="$emit('update:modelValue', Number($event.target.value))"><option value="">Select</option><option v-for="g in options" :key="g.value" :value="g.value">{{ g.label }}</option></select>`
})
const DatePicker = defineComponent({
  props: ['startDate', 'endDate'],
  emits: ['update:startDate', 'update:endDate'],
  template: '<div />'
})
const Pager = defineComponent({
  props: ['total', 'page', 'pageSize'],
  emits: ['update:page', 'update:pageSize'],
  template: '<div />'
})

const response = (overrides: Partial<DownstreamAssessmentResult> = {}): DownstreamAssessmentResult => ({
  items: [
    { user_id: 1, email: 'idle@test.com', username: 'Idle', total_requests: 0, total_tokens: 0, daily_average: 0, qualified: false, last_used_at: null },
    { user_id: 2, email: 'met@test.com', username: 'Met', total_requests: 10, total_tokens: 7000000000, daily_average: 1000000000, qualified: true, last_used_at: '2026-10-15T04:34:56Z' }
  ],
  total: 31, qualified_count: 20, unqualified_count: 11, total_tokens: 7000000000,
  days: 7, daily_threshold: 1000000000, page: 1, page_size: 20, pages: 2, ...overrides
})
const groupUser = { id: 2, email: 'met@test.com', allowed_groups: [7], group_rates: { 7: 0.5, 8: 0.75 }, restrict_public_groups: false } as AdminUser

let wrapper: VueWrapper
beforeEach(() => {
  vi.resetAllMocks()
  localStorage.clear()
  getGroups.mockResolvedValue([{ id: 7, name: 'Daily 1B' }, { id: 8, name: 'Inactive group', status: 'inactive' }])
  getAssessment.mockResolvedValue(response())
  getUser.mockResolvedValue(groupUser)
  listGroups.mockResolvedValue({ items: [
    { id: 7, name: 'Daily 1B', platform: 'openai', is_exclusive: true, subscription_type: 'standard', status: 'active', rate_multiplier: 1 },
    { id: 8, name: 'Daily 0.5B', platform: 'openai', is_exclusive: true, subscription_type: 'standard', status: 'active', rate_multiplier: 1 }
  ] })
  updateUser.mockResolvedValue(groupUser)
  wrapper = mount(DownstreamAssessmentView, { global: { stubs: {
    Select: GroupSelect, DateRangePicker: DatePicker, Pagination: Pager, Icon: true, PlatformIcon: true,
    BaseDialog: { props: ['show'], template: '<div v-if="show" role="dialog"><slot /><slot name="footer" /></div>' }
  } } })
})
afterEach(() => wrapper.unmount())

async function fillFilters() {
  await flushPromises()
  await wrapper.find('select').setValue('7')
  const picker = wrapper.findComponent(DatePicker)
  picker.vm.$emit('update:startDate', '2026-10-01')
  picker.vm.$emit('update:endDate', '2026-10-07')
  await wrapper.find('#assessment-threshold').setValue('1')
}

describe('DownstreamAssessmentView', () => {
  it('requires a group and a valid threshold and never automatically runs on mount', async () => {
    await flushPromises()
    expect(getAssessment).not.toHaveBeenCalled()
    expect(wrapper.find('[data-testid=assess]').attributes('disabled')).toBeDefined()
    await wrapper.find('select').setValue('7')
    expect(wrapper.find('[data-testid=assess]').attributes('disabled')).toBeDefined()
    await wrapper.find('#assessment-threshold').setValue('-1B')
    expect(wrapper.find('[data-testid=assess]').attributes('disabled')).toBeDefined()
    await wrapper.find('#assessment-threshold').setValue('1')
    expect(wrapper.find('[data-testid=assess]').attributes('disabled')).toBeUndefined()
    expect(wrapper.find('select').text()).toContain('Inactive group')
  })

  it('sends normalized tokens and dates, colors only status badges, and offers group actions', async () => {
    await fillFilters()
    await wrapper.find('[data-testid=assess]').trigger('click')
    await flushPromises()
    expect(getAssessment).toHaveBeenCalledWith(expect.objectContaining({ group_id: 7, start_date: '2026-10-01', end_date: '2026-10-07', daily_threshold: 1000000000, page: 1 }), { signal: expect.any(AbortSignal) })
    expect(wrapper.find('[data-user-id="1"]').classes()).toContain('text-gray-700')
    expect(wrapper.find('[data-user-id="1"]').classes()).not.toContain('text-red-700')
    expect(wrapper.find('[data-user-id="1"]').classes()).not.toContain('bg-red-50')
    const unqualifiedStatus = wrapper.find('[data-user-id="1"] [data-testid=assessment-status]')
    expect(unqualifiedStatus.text()).toBe('admin.downstreamAssessment.unqualified')
    expect(unqualifiedStatus.classes()).toContain('badge-danger')
    expect(wrapper.find('[data-user-id="2"]').classes()).not.toContain('text-red-700')
    const qualifiedStatus = wrapper.find('[data-user-id="2"] [data-testid=assessment-status]')
    expect(qualifiedStatus.text()).toBe('admin.downstreamAssessment.qualified')
    expect(qualifiedStatus.classes()).toContain('badge-success')
    expect(wrapper.find('[data-user-id="2"] [data-testid=total-tokens-b]').text()).toBe('7.00')
    expect(wrapper.find('[data-user-id="2"] [data-testid=daily-average-b]').text()).toBe('1.00')
    expect(wrapper.find('[data-user-id="1"] [data-testid=total-tokens-b]').text()).toBe('0.00')
    expect(wrapper.find('[data-user-id="1"] [data-testid=last-used-at]').text()).toBe('-')
    expect(wrapper.find('[data-user-id="2"] [data-testid=last-used-at]').text()).toBe(formatDateTime('2026-10-15T04:34:56Z', undefined, 'en'))
    expect(wrapper.findAll('thead th')).toHaveLength(7)
    expect(wrapper.findComponent(Pager).props('total')).toBe(31)
    expect(wrapper.findAll('thead th').at(-1)?.text()).toBe('common.actions')
    expect(wrapper.findAll('[data-testid=configure-groups]')).toHaveLength(2)
  })

  it('loads current user settings into the shared dialog, saves groups, and refreshes the current page', async () => {
    await fillFilters()
    await wrapper.find('[data-testid=assess]').trigger('click')
    await flushPromises()
    getAssessment.mockResolvedValueOnce(response({ page: 2 }))
    wrapper.findComponent(Pager).vm.$emit('update:page', 2)
    await flushPromises()
    await wrapper.find('[data-user-id="2"] [data-testid=configure-groups]').trigger('click')
    await flushPromises()
    expect(getUser).toHaveBeenCalledWith(2)
    const modal = wrapper.findComponent(UserAllowedGroupsModal)
    expect(modal.props('show')).toBe(true)
    expect(modal.props('user')).toEqual(groupUser)
    const checkboxes = modal.findAll<HTMLInputElement>('input[type=checkbox]')
    expect(checkboxes.map(input => input.element.checked)).toEqual([true, false])
    expect(modal.findAll<HTMLInputElement>('input[type=number]').map(input => input.element.value)).toEqual(['0.5', '0.75'])
    await checkboxes[0].setValue(false)
    await checkboxes[1].setValue(true)
    getAssessment.mockResolvedValueOnce(response({ items: [], total: 0, pages: 0, page: 2 }))
    await modal.get('button.btn-primary').trigger('click')
    await flushPromises()
    expect(updateUser).toHaveBeenCalledWith(2, { allowed_groups: [8], restrict_public_groups: false, group_rates: { 7: 0.5, 8: 0.75 } })
    expect(showSuccess).toHaveBeenCalledWith('admin.users.groupConfigUpdated')
    expect(getAssessment).toHaveBeenCalledTimes(3)
    expect(getAssessment).toHaveBeenLastCalledWith(expect.objectContaining({ group_id: 7, start_date: '2026-10-01', end_date: '2026-10-07', daily_threshold: 1000000000, page: 2 }), expect.anything())
    expect(modal.props('show')).toBe(false)
    expect(wrapper.find('[data-user-id]').exists()).toBe(false)
    expect(wrapper.get('tbody td').attributes('colspan')).toBe('7')
  })

  it('closes group settings without saving or rerunning the assessment on cancel', async () => {
    await fillFilters()
    await wrapper.find('[data-testid=assess]').trigger('click')
    await flushPromises()
    await wrapper.find('[data-user-id="2"] [data-testid=configure-groups]').trigger('click')
    await flushPromises()
    await wrapper.findComponent(UserAllowedGroupsModal).get('button.btn-secondary').trigger('click')
    expect(wrapper.findComponent(UserAllowedGroupsModal).props('show')).toBe(false)
    expect(wrapper.findComponent(UserAllowedGroupsModal).props('user')).toBeNull()
    expect(updateUser).not.toHaveBeenCalled()
    expect(getAssessment).toHaveBeenCalledTimes(1)
  })

  it('shows a retryable user loading error without opening an empty group editor', async () => {
    getUser.mockRejectedValueOnce(new Error('Offline'))
    await fillFilters()
    await wrapper.find('[data-testid=assess]').trigger('click')
    await flushPromises()
    const button = wrapper.find('[data-user-id="2"] [data-testid=configure-groups]')
    await button.trigger('click')
    await flushPromises()
    expect(showError).toHaveBeenCalledWith('admin.downstreamAssessment.loadUserError')
    expect(wrapper.findComponent(UserAllowedGroupsModal).props('show')).toBe(false)
    expect(button.attributes('disabled')).toBeUndefined()
    expect(updateUser).not.toHaveBeenCalled()
    await button.trigger('click')
    await flushPromises()
    expect(wrapper.findComponent(UserAllowedGroupsModal).props('show')).toBe(true)
  })

  it('prevents duplicate group loads and ignores pending details when assessment filters change', async () => {
    let resolve!: (value: AdminUser) => void
    getUser.mockReturnValueOnce(new Promise<AdminUser>(done => { resolve = done }))
    await fillFilters()
    await wrapper.find('[data-testid=assess]').trigger('click')
    await flushPromises()
    await wrapper.find('[data-user-id="2"] [data-testid=configure-groups]').trigger('click')
    for (const button of wrapper.findAll('[data-testid=configure-groups]')) expect(button.attributes('disabled')).toBeDefined()
    await wrapper.find('[data-user-id="1"] [data-testid=configure-groups]').trigger('click')
    expect(getUser).toHaveBeenCalledTimes(1)
    await wrapper.find('select').setValue('8')
    resolve(groupUser)
    await flushPromises()
    expect(wrapper.findComponent(UserAllowedGroupsModal).props('show')).toBe(false)
    expect(wrapper.findComponent(UserAllowedGroupsModal).props('user')).toBeNull()
    expect(updateUser).not.toHaveBeenCalled()
  })

  it('uses B for fractional thresholds and usage below one billion', async () => {
    getAssessment.mockResolvedValueOnce(response({
      daily_threshold: 500000000,
      total_tokens: 3500000000,
      items: [{ user_id: 3, email: 'half@test.com', username: 'Half', total_requests: 7, total_tokens: 3500000000, daily_average: 500000000, qualified: true, last_used_at: null }]
    }))
    await fillFilters()
    await wrapper.find('#assessment-threshold').setValue('0.5')
    await wrapper.find('[data-testid=assess]').trigger('click')
    await flushPromises()
    expect(getAssessment).toHaveBeenLastCalledWith(expect.objectContaining({ daily_threshold: 500000000 }), expect.anything())
    expect(wrapper.find('[data-testid=total-tokens-b]').text()).toBe('3.50')
    expect(wrapper.find('[data-testid=daily-average-b]').text()).toBe('0.50')
    expect(wrapper.find('p[title="3.50 B"]').text()).toBe('3.50')
  })

  it('retains the API qualification when below-threshold usage rounds up to the threshold', async () => {
    getAssessment.mockResolvedValueOnce(response({ items: [
      { user_id: 3, email: 'below@test.com', username: 'Below', total_requests: 7, total_tokens: 6999999999, daily_average: 6999999999 / 7, qualified: false, last_used_at: '2026-10-07T12:00:00Z' },
      response().items[1]
    ] }))
    await fillFilters()
    await wrapper.find('[data-testid=assess]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-user-id="3"] [data-testid=total-tokens-b]').text()).toBe('7.00')
    expect(wrapper.find('[data-user-id="3"] [data-testid=daily-average-b]').text()).toBe('1.00')
    expect(wrapper.find('[data-user-id="3"] [data-testid=assessment-status]').classes()).toContain('badge-danger')
    expect(wrapper.find('[data-user-id="2"] [data-testid=assessment-status]').classes()).toContain('badge-success')
  })

  it('paginates on the server with the applied filters and resets page when size changes', async () => {
    await fillFilters()
    await wrapper.find('[data-testid=assess]').trigger('click')
    await flushPromises()
    wrapper.findComponent(Pager).vm.$emit('update:page', 2)
    await flushPromises()
    expect(getAssessment).toHaveBeenLastCalledWith(expect.objectContaining({ group_id: 7, page: 2, daily_threshold: 1000000000, start_date: '2026-10-01' }), expect.anything())
    wrapper.findComponent(Pager).vm.$emit('update:pageSize', 50)
    await flushPromises()
    expect(getAssessment).toHaveBeenLastCalledWith(expect.objectContaining({ page: 1, page_size: 50 }), expect.anything())
  })

  it('clears stale results on edits and requires another explicit assessment', async () => {
    await fillFilters()
    await wrapper.find('[data-testid=assess]').trigger('click')
    await flushPromises()
    await wrapper.find('#assessment-threshold').setValue('')
    expect(wrapper.find('[data-user-id]').exists()).toBe(false)
    expect(wrapper.find('[data-testid=assess]').attributes('disabled')).toBeDefined()
    expect(getAssessment).toHaveBeenCalledTimes(1)
  })

  it('ignores an old response when filters change during a request', async () => {
    let resolve!: (value: DownstreamAssessmentResult) => void
    getAssessment.mockImplementationOnce(() => new Promise<DownstreamAssessmentResult>(done => { resolve = done }))
    await fillFilters()
    await wrapper.find('[data-testid=assess]').trigger('click')
    await wrapper.find('select').setValue('8')
    resolve(response())
    await flushPromises()
    expect(wrapper.find('[data-user-id]').exists()).toBe(false)
    expect(wrapper.text()).toContain('admin.downstreamAssessment.initialHint')
  })

  it('handles empty membership and failed requests without stale results', async () => {
    getAssessment.mockResolvedValueOnce(response({ items: [], total: 0, pages: 0 }))
    await fillFilters()
    await wrapper.find('[data-testid=assess]').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('admin.downstreamAssessment.noMembers')
    expect(wrapper.findComponent(Pager).exists()).toBe(false)
    getAssessment.mockRejectedValueOnce(new Error('unavailable'))
    await wrapper.find('[data-testid=assess]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[role=alert]').text()).toContain('admin.downstreamAssessment.loadError')
    expect(wrapper.find('[data-user-id]').exists()).toBe(false)
  })

  it('rejects a reversed range', async () => {
    await fillFilters()
    wrapper.findComponent(DatePicker).vm.$emit('update:endDate', '2026-09-30')
    await flushPromises()
    expect(wrapper.find('[data-testid=assess]').attributes('disabled')).toBeDefined()
    expect(getAssessment).not.toHaveBeenCalled()
  })

  it('recovers when membership shrinks beyond the requested page', async () => {
    await fillFilters()
    await wrapper.find('[data-testid=assess]').trigger('click')
    await flushPromises()
    getAssessment.mockResolvedValueOnce(response({ items: [], total: 1, pages: 1, page: 2 })).mockResolvedValueOnce(response({ total: 1, pages: 1 }))
    wrapper.findComponent(Pager).vm.$emit('update:page', 2)
    await flushPromises()
    expect(getAssessment).toHaveBeenLastCalledWith(expect.objectContaining({ page: 1 }), expect.anything())
    expect(wrapper.find('[data-user-id="1"]').exists()).toBe(true)
  })
})

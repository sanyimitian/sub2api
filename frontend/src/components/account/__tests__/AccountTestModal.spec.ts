import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent } from 'vue'
import AccountTestModal from '../AccountTestModal.vue'
import AccountTokenInjectionPanel from '../AccountTokenInjectionPanel.vue'
import Select from '@/components/common/Select.vue'

const { getAvailableModelsMock } = vi.hoisted(() => ({
  getAvailableModelsMock: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      getAvailableModels: getAvailableModelsMock
    }
  }
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({
    copyToClipboard: vi.fn()
  })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key
    })
  }
})

const BaseDialogStub = defineComponent({
  name: 'BaseDialog',
  props: { show: { type: Boolean, default: false } },
  template: '<div v-if="show"><slot /><slot name="footer" /></div>'
})

const SelectStub = defineComponent({
  name: 'SelectStub',
  props: {
    modelValue: { type: [String, Number, Boolean, null], default: '' },
    options: { type: Array, default: () => [] },
    valueKey: { type: String, default: 'value' },
    labelKey: { type: String, default: 'label' }
  },
  emits: ['update:modelValue'],
  template: `
    <select
      v-bind="$attrs"
      :value="modelValue"
      @change="$emit('update:modelValue', $event.target.value)"
    >
      <option
        v-for="option in options"
        :key="option[valueKey]"
        :value="option[valueKey]"
      >
        {{ option[labelKey] }}
      </option>
    </select>
  `
})

const TextAreaStub = defineComponent({
  name: 'TextArea',
  props: {
    modelValue: { type: String, default: '' }
  },
  emits: ['update:modelValue'],
  template: `
    <textarea
      v-bind="$attrs"
      :value="modelValue"
      @input="$emit('update:modelValue', $event.target.value)"
    />
  `
})

function buildAccount() {
  return {
    id: 1,
    name: 'OpenAI OAuth',
    platform: 'openai',
    type: 'oauth',
    status: 'active',
    credentials: {},
    extra: {},
    concurrency: 1,
    priority: 1,
    proxy_id: null,
    auto_pause_on_expired: false
  } as any
}

describe('AccountTestModal', () => {
  const originalFetch = global.fetch

  beforeEach(() => {
    getAvailableModelsMock.mockReset()
    getAvailableModelsMock.mockResolvedValue([
      { id: 'gpt-5.4', display_name: 'GPT-5.4' }
    ])
    global.fetch = vi.fn().mockResolvedValue({
      ok: true,
      body: {
        getReader: () => ({
          read: vi.fn().mockResolvedValue({ done: true, value: undefined })
        })
      }
    } as any)
    localStorage.setItem('auth_token', 'test-token')
  })

  afterEach(() => {
    global.fetch = originalFetch
    localStorage.clear()
  })

  it('opens injection diagnostics for the selected account and manual model', async () => {
    const wrapper = mount(AccountTestModal, {
      props: { show: true, account: buildAccount() },
      global: { stubs: {
        BaseDialog: BaseDialogStub, Select: SelectStub, TextArea: TextAreaStub,
        Icon: true, AccountTokenInjectionPanel: true
      } }
    })
    const modeSelect = wrapper.findAll('select')[1]
    expect(modeSelect.text()).toContain('admin.accounts.tokenInjection.title')
    await modeSelect.setValue('injection')
    ;(wrapper.vm as any).selectedModelId = 'custom-model'
    await flushPromises()
    const panel = wrapper.getComponent(AccountTokenInjectionPanel)
    expect(panel.props('account').id).toBe(1)
    expect(panel.props('model')).toBe('custom-model')
    expect(wrapper.text()).not.toContain('admin.accounts.startTest')
    panel.vm.$emit('running', true)
    await flushPromises()
    expect(wrapper.findAll('select').every(select => select.attributes('disabled') !== undefined)).toBe(true)
    await wrapper.setProps({ account: { ...buildAccount(), id: 2, platform: 'anthropic' } })
    await flushPromises()
    expect(wrapper.findComponent(AccountTokenInjectionPanel).exists()).toBe(false)
    expect(getAvailableModelsMock).toHaveBeenCalledWith(2)
    await wrapper.setProps({ show: false })
    expect(wrapper.findComponent(AccountTokenInjectionPanel).exists()).toBe(false)
    expect(global.fetch).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('allows entering a model when the available list is empty', async () => {
    const wrapper = mount(AccountTestModal, {
      props: { show: true, account: buildAccount() },
      global: { stubs: {
        BaseDialog: BaseDialogStub, TextArea: TextAreaStub,
        Icon: true, AccountTokenInjectionPanel: true, Teleport: true
      } }
    })
    const selectors = wrapper.findAllComponents(Select)
    selectors[1].vm.$emit('update:modelValue', 'injection')
    await flushPromises()
    await selectors[0].get('button').trigger('click')
    await selectors[0].get('input').setValue('gpt-custom-model')
    await selectors[0].get('.select-option').trigger('click')
    await flushPromises()
    expect(wrapper.getComponent(AccountTokenInjectionPanel).props('model')).toBe('gpt-custom-model')
    wrapper.unmount()
  })

  it('posts compact mode for OpenAI compact probe', async () => {
    const wrapper = mount(AccountTestModal, {
      props: {
        show: true,
        account: buildAccount()
      },
      global: {
        stubs: {
          BaseDialog: BaseDialogStub,
          Select: SelectStub,
          TextArea: TextAreaStub,
          Icon: true
        }
      }
    })

    await flushPromises()
    ;(wrapper.vm as any).selectedModelId = 'gpt-5.4'
    ;(wrapper.vm as any).testMode = 'compact'
    await (wrapper.vm as any).startTest()
    await flushPromises()

    expect(global.fetch).toHaveBeenCalledTimes(1)
    const [, options] = (global.fetch as any).mock.calls[0]
    expect(JSON.parse(options.body)).toMatchObject({
      model_id: 'gpt-5.4',
      mode: 'compact'
    })
  })

  it('renders Chat Completions path status from test SSE', async () => {
    const encoder = new TextEncoder()
    const chunks = [
      encoder.encode('data: {"type":"status","text":"已通过 /v1/chat/completions 验证"}\n\n'),
      encoder.encode('data: {"type":"test_complete","success":true}\n\n')
    ]
    global.fetch = vi.fn().mockResolvedValue({
      ok: true,
      body: {
        getReader: () => ({
          read: vi.fn().mockImplementation(() => Promise.resolve(
            chunks.length > 0
              ? { done: false, value: chunks.shift() }
              : { done: true, value: undefined }
          ))
        })
      }
    } as any)

    const wrapper = mount(AccountTestModal, {
      props: {
        show: true,
        account: buildAccount()
      },
      global: {
        stubs: {
          BaseDialog: BaseDialogStub,
          Select: SelectStub,
          TextArea: TextAreaStub,
          Icon: true
        }
      }
    })

    await flushPromises()
    ;(wrapper.vm as any).selectedModelId = 'gpt-5.4'
    await (wrapper.vm as any).startTest()
    await flushPromises()

    expect(wrapper.text()).toContain('已通过 /v1/chat/completions 验证')
  })
})

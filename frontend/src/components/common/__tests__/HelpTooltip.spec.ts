import { afterEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import HelpTooltip from '@/components/common/HelpTooltip.vue'

function getTooltipElement(): HTMLDivElement {
  const tooltip = document.body.querySelector('[role="tooltip"]')
  if (!(tooltip instanceof HTMLDivElement)) {
    throw new Error('tooltip element not found')
  }
  return tooltip
}

describe('HelpTooltip', () => {
  afterEach(() => {
    document.body.innerHTML = ''
    vi.restoreAllMocks()
  })

  it('keeps the existing hover interaction by default', async () => {
    const wrapper = mount(HelpTooltip, {
      attachTo: document.body,
      props: {
        content: 'hover details',
      },
    })

    const trigger = wrapper.get('.group')
    const tooltip = getTooltipElement()

    expect(tooltip.style.display).toBe('none')

    await trigger.trigger('mouseenter')
    await nextTick()
    expect(tooltip.style.display).not.toBe('none')

    await trigger.trigger('mouseleave')
    await nextTick()
    expect(tooltip.style.display).toBe('none')

    wrapper.unmount()
  })

  it('positions fixed tooltips in viewport coordinates after page scrolling', async () => {
    vi.spyOn(window, 'scrollY', 'get').mockReturnValue(800)
    vi.spyOn(window, 'scrollX', 'get').mockReturnValue(400)
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({
      x: 20,
      y: 30,
      top: 30,
      left: 20,
      right: 120,
      bottom: 46,
      width: 100,
      height: 16,
      toJSON: () => ({}),
    } as DOMRect)

    const wrapper = mount(HelpTooltip, {
      attachTo: document.body,
      props: { content: 'scroll details' },
    })

    await wrapper.get('.group').trigger('mouseenter')
    await nextTick()

    const tooltip = getTooltipElement()
    expect(tooltip.style.top).toBe('calc(22px)')
    expect(tooltip.style.left).toBe('70px')

    wrapper.unmount()
  })

  it('places compact side tooltips beside the trigger and flips them inside the viewport', async () => {
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
      const isTooltip = this.getAttribute('role') === 'tooltip'
      return {
        x: isTooltip ? 0 : 900,
        y: isTooltip ? 0 : 30,
        top: isTooltip ? 0 : 30,
        left: isTooltip ? 0 : 900,
        right: isTooltip ? 180 : 940,
        bottom: isTooltip ? 80 : 46,
        width: isTooltip ? 180 : 40,
        height: isTooltip ? 80 : 16,
        toJSON: () => ({}),
      } as DOMRect
    })

    const wrapper = mount(HelpTooltip, {
      attachTo: document.body,
      props: {
        content: 'compact details',
        placement: 'right',
        compact: true,
        widthClass: 'w-max',
      },
    })

    await wrapper.get('.group').trigger('mouseenter')
    await nextTick()

    const tooltip = getTooltipElement()
    expect(tooltip.style.top).toBe('38px')
    expect(tooltip.style.left).toBe('892px')
    expect(tooltip.classList.contains('-translate-x-full')).toBe(true)
    expect(tooltip.classList.contains('before:left-full')).toBe(true)

    wrapper.unmount()
  })

  it('keeps a hover tooltip open while the pointer moves between the trigger and the tooltip', async () => {
    const wrapper = mount(HelpTooltip, {
      attachTo: document.body,
      props: {
        content: 'copyable details',
      },
    })

    const trigger = wrapper.get('.group')
    const tooltip = getTooltipElement()

    await trigger.trigger('mouseenter')
    await nextTick()
    expect(tooltip.style.display).not.toBe('none')

    await trigger.trigger('mouseleave', { relatedTarget: tooltip })
    await nextTick()
    expect(tooltip.style.display).not.toBe('none')

    tooltip.dispatchEvent(new MouseEvent('mouseleave', { relatedTarget: trigger.element }))
    await nextTick()
    expect(tooltip.style.display).not.toBe('none')

    tooltip.dispatchEvent(new MouseEvent('mouseleave', { relatedTarget: null }))
    await nextTick()
    expect(tooltip.style.display).toBe('none')

    wrapper.unmount()
  })

  it('supports click-to-toggle details and closes on outside click', async () => {
    const wrapper = mount(HelpTooltip, {
      attachTo: document.body,
      props: {
        content: 'click details',
        trigger: 'click',
      },
    })

    const trigger = wrapper.get('.group')
    const tooltip = getTooltipElement()

    expect(tooltip.style.display).toBe('none')

    await trigger.trigger('click')
    await nextTick()
    expect(tooltip.style.display).not.toBe('none')
    expect(tooltip.textContent).toContain('click details')

    const closeButton = tooltip.querySelector('button[aria-label="Close"]')
    if (!(closeButton instanceof HTMLButtonElement)) {
      throw new Error('close button not found')
    }
    closeButton.click()
    await nextTick()
    expect(tooltip.style.display).toBe('none')

    await trigger.trigger('click')
    await nextTick()
    expect(tooltip.style.display).not.toBe('none')

    document.body.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await nextTick()
    expect(tooltip.style.display).toBe('none')

    wrapper.unmount()
  })
})

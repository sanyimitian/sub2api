(() => {
  const root = document.documentElement
  const sidebar = document.querySelector('.sidebar')
  const overlay = document.querySelector('.nav-overlay')
  const menuToggle = document.querySelector('.menu-toggle')
  const mobileNavQuery = window.matchMedia('(max-width: 900px)')
  const tocLinks = [...document.querySelectorAll('.toc a[data-section]')]
  const sections = tocLinks
    .map((link) => document.getElementById(link.dataset.section))
    .filter(Boolean)

  const syncMenuState = (isOpen = sidebar?.classList.contains('is-open')) => {
    const isMobile = mobileNavQuery.matches
    const visible = isMobile ? Boolean(isOpen) : true
    if (sidebar) {
      sidebar.setAttribute('aria-hidden', String(!visible))
      if ('inert' in sidebar) sidebar.inert = !visible
      else sidebar.toggleAttribute('inert', !visible)
    }
    overlay?.setAttribute('aria-hidden', String(!(isMobile && visible)))
    menuToggle?.setAttribute('aria-label', isMobile && visible ? '关闭文档目录' : '打开文档目录')
  }

  const closeMenu = () => {
    const focusInsideSidebar = sidebar?.contains(document.activeElement)
    sidebar?.classList.remove('is-open')
    overlay?.classList.remove('is-visible')
    menuToggle?.setAttribute('aria-expanded', 'false')
    syncMenuState(false)
    if (focusInsideSidebar) menuToggle?.focus({ preventScroll: true })
  }

  menuToggle?.addEventListener('click', () => {
    const isOpen = sidebar?.classList.toggle('is-open') || false
    overlay?.classList.toggle('is-visible', Boolean(isOpen))
    menuToggle.setAttribute('aria-expanded', String(Boolean(isOpen)))
    syncMenuState(Boolean(isOpen))
    if (isOpen) sidebar?.querySelector('a')?.focus({ preventScroll: true })
  })
  overlay?.addEventListener('click', closeMenu)
  tocLinks.forEach((link) => link.addEventListener('click', closeMenu))
  document.addEventListener('keydown', (event) => {
    if (event.key === 'Escape' && sidebar?.classList.contains('is-open')) closeMenu()
  })
  mobileNavQuery.addEventListener?.('change', () => {
    if (!mobileNavQuery.matches) {
      sidebar?.classList.remove('is-open')
      overlay?.classList.remove('is-visible')
      menuToggle?.setAttribute('aria-expanded', 'false')
    }
    syncMenuState(sidebar?.classList.contains('is-open'))
  })
  syncMenuState(false)

  const markActive = () => {
    const cursor = window.scrollY + 130
    let current = sections[0]
    sections.forEach((section) => {
      if (section.offsetTop <= cursor) current = section
    })
    tocLinks.forEach((link) => link.classList.toggle('is-active', link.dataset.section === current?.id))
  }
  window.addEventListener('scroll', markActive, { passive: true })
  markActive()

  const savedTheme = localStorage.getItem('llmbridge-guide-theme')
  const preferredDark = window.matchMedia('(prefers-color-scheme: dark)').matches
  const setTheme = (dark) => {
    root.dataset.theme = dark ? 'dark' : 'light'
    const icon = document.querySelector('.theme-icon')
    const label = document.querySelector('[data-theme-label]')
    if (icon) icon.textContent = dark ? '☀' : '☾'
    if (label) label.textContent = dark ? '浅色模式' : '深色模式'
  }
  setTheme(savedTheme ? savedTheme === 'dark' : preferredDark)
  document.querySelector('[data-theme-toggle]')?.addEventListener('click', () => {
    const nextDark = root.dataset.theme !== 'dark'
    setTheme(nextDark)
    localStorage.setItem('llmbridge-guide-theme', nextDark ? 'dark' : 'light')
  })

  document.querySelectorAll('[data-tabs]').forEach((tabs, groupIndex) => {
    const buttons = [...tabs.querySelectorAll(':scope > .tab-bar [data-tab]')]
    const panels = [...tabs.querySelectorAll(':scope > .tab-panel[data-panel]')]
    const groupId = tabs.id || 'guide-tabs-' + (groupIndex + 1)
    tabs.id = groupId
    buttons.forEach((button, buttonIndex) => {
      const tabId = button.id || groupId + '-tab-' + (buttonIndex + 1)
      const panel = panels.find((item) => item.dataset.panel === button.dataset.tab)
      const panelId = panel?.id || groupId + '-panel-' + button.dataset.tab
      button.id = tabId
      button.setAttribute('aria-controls', panelId)
      button.setAttribute('tabindex', button.classList.contains('is-active') ? '0' : '-1')
      if (panel) {
        panel.id = panelId
        panel.setAttribute('aria-labelledby', tabId)
      }
    })
    buttons.forEach((button) => {
      button.addEventListener('click', () => {
        const tab = button.dataset.tab
        buttons.forEach((item) => {
          const active = item === button
          item.classList.toggle('is-active', active)
          item.setAttribute('aria-selected', String(active))
          item.setAttribute('tabindex', active ? '0' : '-1')
        })
        panels.forEach((panel) => {
          const active = panel.dataset.panel === tab
          panel.classList.toggle('is-active', active)
          panel.hidden = !active
        })
      })
      button.addEventListener('keydown', (event) => {
        if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return
        const currentIndex = buttons.indexOf(button)
        const nextIndex = event.key === 'Home'
          ? 0
          : event.key === 'End'
            ? buttons.length - 1
            : (currentIndex + (event.key === 'ArrowRight' ? 1 : -1) + buttons.length) % buttons.length
        event.preventDefault()
        buttons[nextIndex].focus()
        buttons[nextIndex].click()
      })
    })
  })

  const fallbackCopy = (value) => {
    const textarea = document.createElement('textarea')
    textarea.value = value
    textarea.setAttribute('readonly', '')
    textarea.style.position = 'fixed'
    textarea.style.opacity = '0'
    document.body.appendChild(textarea)
    textarea.select()
    try { return document.execCommand('copy') } finally { textarea.remove() }
  }

  document.querySelectorAll('.copy-button').forEach((button) => {
    button.addEventListener('click', async () => {
      const value = (button.closest('.code-block')?.querySelector('code')?.textContent || button.dataset.copy || '')
        .replace(/\\n/g, '\n')
      const markCopied = () => {
        button.classList.remove('copy-failed')
        button.classList.add('is-copied')
        button.textContent = '已复制'
        window.setTimeout(() => {
          button.classList.remove('is-copied')
          button.textContent = '复制'
        }, 1400)
      }
      const markFailed = () => {
        button.classList.add('copy-failed')
        button.textContent = '复制失败'
        window.setTimeout(() => {
          button.classList.remove('copy-failed')
          button.textContent = '复制'
        }, 1800)
      }
      try {
        if (navigator.clipboard?.writeText) await navigator.clipboard.writeText(value)
        else if (!fallbackCopy(value)) throw new Error('copy command rejected')
        markCopied()
      } catch {
        if (fallbackCopy(value)) markCopied()
        else markFailed()
      }
    })
  })
})()

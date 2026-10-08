/**
 * Shared URL builder for iframe-embedded pages.
 * Embedded destinations receive display context only. Authentication credentials
 * must never be forwarded in URLs.
 */

const EMBEDDED_USER_ID_QUERY_KEY = 'user_id'
const EMBEDDED_THEME_QUERY_KEY = 'theme'
const EMBEDDED_LANG_QUERY_KEY = 'lang'
const EMBEDDED_UI_MODE_QUERY_KEY = 'ui_mode'
const EMBEDDED_UI_MODE_VALUE = 'embedded'
const EMBEDDED_SRC_HOST_QUERY_KEY = 'src_host'
const EMBEDDED_SRC_QUERY_KEY = 'src_url'
const CREDENTIAL_QUERY_KEYS = new Set([
  'token',
  'accesstoken',
  'refreshtoken',
  'idtoken',
  'authtoken',
  'authorization',
  'apikey',
  'xapikey',
  'jwt',
  'secret',
  'clientsecret',
  'password',
  'sessiontoken',
  'bearer',
])

function isCredentialParam(key: string): boolean {
  return CREDENTIAL_QUERY_KEYS.has(key.toLowerCase().replace(/[-_]/g, ''))
}

function removeCredentialParams(params: URLSearchParams): void {
  for (const key of [...params.keys()]) {
    if (isCredentialParam(key)) {
      params.delete(key)
    }
  }
}

function removeCredentialFragmentParams(url: URL): void {
  if (!url.hash) return

  const fragment = url.hash.slice(1)
  const queryIndex = fragment.indexOf('?')
  const prefix = queryIndex >= 0 ? fragment.slice(0, queryIndex) : ''
  const params = new URLSearchParams(queryIndex >= 0 ? fragment.slice(queryIndex + 1) : fragment)
  const hasCredentials = [...params.keys()].some(isCredentialParam)
  if (!hasCredentials) return

  removeCredentialParams(params)
  const remaining = params.toString()
  url.hash = remaining ? `${prefix}${queryIndex >= 0 ? '?' : ''}${remaining}` : prefix
}

export function buildEmbeddedUrl(
  baseUrl: string,
  userId?: number,
  theme: 'light' | 'dark' = 'light',
  lang?: string,
): string {
  if (!baseUrl) return baseUrl
  try {
    const url = new URL(baseUrl)
    url.username = ''
    url.password = ''
    removeCredentialParams(url.searchParams)
    removeCredentialFragmentParams(url)
    if (userId) {
      url.searchParams.set(EMBEDDED_USER_ID_QUERY_KEY, String(userId))
    }
    url.searchParams.set(EMBEDDED_THEME_QUERY_KEY, theme)
    if (lang) {
      url.searchParams.set(EMBEDDED_LANG_QUERY_KEY, lang)
    }
    url.searchParams.set(EMBEDDED_UI_MODE_QUERY_KEY, EMBEDDED_UI_MODE_VALUE)
    // Source tracking: let the embedded page know where it's being loaded from
    if (typeof window !== 'undefined') {
      url.searchParams.set(EMBEDDED_SRC_HOST_QUERY_KEY, window.location.origin)
      url.searchParams.set(EMBEDDED_SRC_QUERY_KEY, `${window.location.origin}${window.location.pathname}`)
    }
    return url.toString()
  } catch {
    return baseUrl
  }
}

export function detectTheme(): 'light' | 'dark' {
  if (typeof document === 'undefined') return 'light'
  return document.documentElement.classList.contains('dark') ? 'dark' : 'light'
}

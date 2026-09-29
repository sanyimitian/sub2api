export interface RouteSEO {
  title: string
  description: string
  canonical: string
}

const ROUTE_SEO: Record<string, RouteSEO> = {
  Home: {
    title: 'codebot.one（codebot API）- 稳定高速的 AI API 中转站',
    description:
      'codebot.one(codebot API) 为开发者及企业提供稳定、高速的 AI API 中转站，支持通过统一接口接入多种主流大模型。',
    canonical: 'https://codebot.one/home',
  },
  HubHome: {
    title: 'AI API 中转站：统一接入多种主流大模型 | codebot API',
    description:
      'codebot API 面向开发者及企业提供 AI API 中转服务，通过统一接口连接多种主流大模型，并提供 API 接入文档与开发教程。',
    canonical: 'https://codebot.one/hub',
  },
}

export function getRouteSEO(routeName: unknown): RouteSEO | null {
  return typeof routeName === 'string' ? ROUTE_SEO[routeName] ?? null : null
}

function setMeta(attribute: 'name' | 'property', key: string, content: string) {
  let element = document.head.querySelector<HTMLMetaElement>(`meta[${attribute}="${key}"]`)
  if (!element) {
    element = document.createElement('meta')
    element.setAttribute(attribute, key)
    document.head.append(element)
  }
  element.content = content
}

export function applyRouteSEO(routeName: unknown) {
  const seo = getRouteSEO(routeName)
  const robots = document.head.querySelector<HTMLMetaElement>('meta[name="robots"]')

  if (!seo) {
    if (robots) robots.content = 'noindex,follow'
    document.head.querySelector('link[rel="canonical"]')?.remove()
    return
  }

  document.title = seo.title
  setMeta('name', 'description', seo.description)
  setMeta('name', 'robots', 'index,follow')
  setMeta('property', 'og:type', 'website')
  setMeta('property', 'og:site_name', 'codebot.one（codebot API）')
  setMeta('property', 'og:title', seo.title)
  setMeta('property', 'og:description', seo.description)
  setMeta('property', 'og:url', seo.canonical)
  setMeta('name', 'twitter:card', 'summary')
  setMeta('name', 'twitter:title', seo.title)
  setMeta('name', 'twitter:description', seo.description)

  let canonical = document.head.querySelector<HTMLLinkElement>('link[rel="canonical"]')
  if (!canonical) {
    canonical = document.createElement('link')
    canonical.rel = 'canonical'
    document.head.append(canonical)
  }
  canonical.href = seo.canonical
}

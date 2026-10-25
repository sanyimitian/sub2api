export const DEFAULT_DEGRADATION_TEST_PROMPT = '创建一个html,内容是svg绘制一个鹈鹕骑自行车的2D动画'

/** Keep the model's original response; extract an HTML/SVG artifact only for preview. */
export function extractDegradationTestHtml(response: string): string | null {
  const source = response.trim()
  const blocks = [...source.matchAll(/```(?:html|svg|xml)?\s*\n([\s\S]*?)```/gi)]
  for (const block of blocks) {
    const html = extractDocument(block[1])
    if (html) return html
  }
  return extractDocument(source)
}

function extractDocument(source: string): string | null {
  const documentStart = source.search(/<!doctype\s+html\b|<html(?:\s|>)/i)
  const documentEnd = source.toLowerCase().lastIndexOf('</html>')
  if (documentStart >= 0 && documentEnd > documentStart) {
    return source.slice(documentStart, documentEnd + 7)
  }
  // Standalone SVG and HTML fragments containing SVG are useful model outputs too.
  if (/<svg(?:\s|>)/i.test(source) && /<\/svg\s*>/i.test(source)) {
    return source
  }
  return null
}

/** This is used only in an iframe with sandbox="allow-scripts", never via v-html. */
export function buildDegradationTestPreview(html: string): string {
  // A template keeps resource elements inert while we prepare the document.
  // Parsing the untrusted source as a document can start image/frame requests
  // before the preview's policy is installed.
  const template = document.createElement('template')
  const root = template.content.ownerDocument.createElement('html')
  root.innerHTML = html
  // Fragment parsing in an html context retains head/body attributes, but not
  // the root's own attributes. Parse just that opening tag in another inert
  // template so language, theme classes, and inline layout styles survive too.
  const openingHtml = html.match(/^\s*(?:(?:<!doctype[^>]*>|<!--[\s\S]*?-->)\s*)*<html\b((?:"[^"]*"|'[^']*'|[^'">])*)>/i)
  if (openingHtml) {
    const attributes = document.createElement('template')
    attributes.innerHTML = `<div${openingHtml[1]}></div>`
    for (const attribute of Array.from(attributes.content.firstElementChild?.attributes ?? [])) {
      root.setAttribute(attribute.name, attribute.value)
    }
  }
  root.querySelectorAll('base, meta[http-equiv="refresh" i]').forEach(node => node.remove())

  // srcdoc inherits the application's CSP. Authorize only inline scripts with
  // its current nonce; the iframe still has an opaque origin and its own CSP.
  // Read the property: browsers hide a live nonce from getAttribute().
  const nonce = Array.from(document.querySelectorAll<HTMLScriptElement>('script[nonce]')).find(script => script.nonce)?.nonce
  if (nonce) {
    root.querySelectorAll('script').forEach(script => {
      if (!script.hasAttribute('src') && !script.hasAttribute('href') && !script.hasAttributeNS('http://www.w3.org/1999/xlink', 'href')) {
        script.setAttribute('nonce', nonce)
      }
    })
  }

  const policy = root.ownerDocument.createElement('meta')
  policy.httpEquiv = 'Content-Security-Policy'
  policy.content = [
    "default-src 'none'",
    "script-src 'unsafe-inline'",
    "style-src 'unsafe-inline'",
    'img-src data: blob:',
    'font-src data:',
    'media-src data: blob:',
    "connect-src 'none'",
    "frame-src 'none'",
    "object-src 'none'",
    "form-action 'none'",
    "base-uri 'none'"
  ].join('; ')
  root.querySelector('head')!.prepend(policy)
  return '<!doctype html>\n' + root.outerHTML
}

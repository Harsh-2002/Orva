// copyText writes a string to the clipboard. Modern browsers expose
// navigator.clipboard only in secure contexts (HTTPS or http://localhost),
// so we fall back to a hidden-textarea + document.execCommand('copy') when
// the modern API isn't available — this works on plain HTTP from a LAN IP
// or behind a non-TLS reverse proxy. Returns a Promise<boolean>.
export async function copyText(text) {
  if (!text) return false

  // Preferred path: secure context (HTTPS or localhost).
  if (navigator.clipboard && window.isSecureContext) {
    try {
      await navigator.clipboard.writeText(text)
      return true
    } catch (e) {
      console.warn('navigator.clipboard.writeText failed, falling back', e)
    }
  }

  // Fallback for HTTP / sandboxed iframes — wrap in try/finally so the
  // textarea is always removed even if execCommand throws.
  const ta = document.createElement('textarea')
  ta.value = text
  ta.setAttribute('readonly', '')
  // Position offscreen but inside the viewport so iOS / mobile actually
  // selects the text. `position: fixed` + `top: 0` keeps the page from
  // scrolling on focus.
  ta.style.position = 'fixed'
  ta.style.top = '0'
  ta.style.left = '0'
  ta.style.opacity = '0'
  ta.style.pointerEvents = 'none'
  document.body.appendChild(ta)
  try {
    ta.focus()
    ta.select()
    ta.setSelectionRange(0, text.length)
    return document.execCommand('copy')
  } catch (e) {
    console.error('clipboard fallback failed', e)
    return false
  } finally {
    document.body.removeChild(ta)
  }
}

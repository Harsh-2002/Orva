// copyText writes a string to the clipboard using the modern Clipboard API.
// Requires a secure context — HTTPS or http://localhost. On plain HTTP from
// a LAN IP or custom hostname this will fail; deploy with HTTPS in front.
// Returns a Promise<boolean>.
export async function copyText(text) {
  if (!text) return false
  try {
    await navigator.clipboard.writeText(text)
    return true
  } catch (e) {
    console.error('clipboard write failed', e)
    return false
  }
}

// Copying text, including where the browser withholds the clipboard API.
//
// navigator.clipboard is gated on a secure context, and it is not merely
// unreliable off one -- it is UNDEFINED. So on a LAN address (http://192.168.x.y)
// every `await navigator.clipboard.writeText(...)` threw a TypeError, and the
// Copy buttons on a reply, a code block, a diff and the session folder all did
// nothing. Silently, because the throw happened inside a click handler nobody was
// watching.
//
// The fallback is the pre-clipboard-API trick: a throwaway textarea, select,
// document.execCommand('copy'). It is deprecated and it still works everywhere,
// which is exactly the trade to make for a path that only runs where the modern
// API has been taken away.
export async function copyText(text: string): Promise<boolean> {
  if (!text) return false
  // The real API when it exists. It can still reject (permissions, a page that
  // is not focused), so a failure falls through to the same fallback rather than
  // being reported as success.
  if (navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(text)
      return true
    } catch {
      /* fall through */
    }
  }
  return legacyCopy(text)
}

function legacyCopy(text: string): boolean {
  try {
    const ta = document.createElement('textarea')
    ta.value = text
    // Off-screen rather than hidden: execCommand needs a selectable element that
    // is actually rendered, so display:none or visibility:hidden would fail.
    ta.setAttribute('readonly', '')
    ta.style.position = 'fixed'
    ta.style.top = '-1000px'
    ta.style.opacity = '0'
    document.body.appendChild(ta)
    ta.select()
    ta.setSelectionRange(0, ta.value.length) // iOS ignores select() alone
    const ok = document.execCommand('copy')
    document.body.removeChild(ta)
    return ok
  } catch {
    return false
  }
}

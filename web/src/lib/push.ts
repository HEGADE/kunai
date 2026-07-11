// Web Push enablement. On iOS this only works from a PWA installed to the home
// screen, and permission must be requested from a user gesture — hence the
// explicit enable button rather than an on-load prompt.

function urlBase64ToUint8Array(base64: string): Uint8Array {
  const padding = '='.repeat((4 - (base64.length % 4)) % 4)
  const b64 = (base64 + padding).replace(/-/g, '+').replace(/_/g, '/')
  const raw = atob(b64)
  const out = new Uint8Array(raw.length)
  for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i)
  return out
}

export function pushSupported(): boolean {
  return 'serviceWorker' in navigator && 'PushManager' in window && 'Notification' in window
}

export function isStandalone(): boolean {
  return (
    window.matchMedia('(display-mode: standalone)').matches ||
    // iOS Safari
    (navigator as unknown as { standalone?: boolean }).standalone === true
  )
}

export function pushState(): 'unsupported' | 'granted' | 'denied' | 'default' {
  if (!pushSupported()) return 'unsupported'
  return Notification.permission as 'granted' | 'denied' | 'default'
}

// isSubscribed reports whether this device currently has a live push
// subscription. Permission being "granted" is not enough — the user may have
// turned notifications off, which unsubscribes without revoking permission.
export async function isSubscribed(): Promise<boolean> {
  if (!pushSupported()) return false
  try {
    const reg = await navigator.serviceWorker.ready
    return !!(await reg.pushManager.getSubscription())
  } catch {
    return false
  }
}

// enablePush requests permission, subscribes, and registers the subscription
// with the server. Returns a human-readable error string on failure, or ''.
export async function enablePush(): Promise<string> {
  if (!pushSupported()) return 'This browser does not support notifications.'
  if (!isStandalone() && /iphone|ipad|ipod/i.test(navigator.userAgent)) {
    return 'On iOS, add Kunai to your home screen first, then enable notifications from there.'
  }
  const perm = await Notification.requestPermission()
  if (perm !== 'granted') return 'Notifications were not allowed.'

  const reg = await navigator.serviceWorker.ready
  const res = await fetch('/api/push/pubkey')
  if (!res.ok) return 'Push is not configured on the server.'
  const { key } = (await res.json()) as { key: string }

  const sub = await reg.pushManager.subscribe({
    userVisibleOnly: true,
    applicationServerKey: urlBase64ToUint8Array(key),
  })
  const post = await fetch('/api/push/subscribe', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(sub),
  })
  if (!post.ok) return 'Could not register for notifications.'
  return ''
}

// disablePush unsubscribes this device and drops it from the server. The
// browser permission itself can't be revoked programmatically, so a later
// re-enable won't re-prompt — it just re-subscribes.
export async function disablePush(): Promise<string> {
  if (!pushSupported()) return ''
  try {
    const reg = await navigator.serviceWorker.ready
    const sub = await reg.pushManager.getSubscription()
    if (sub) {
      await fetch('/api/push/unsubscribe', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ endpoint: sub.endpoint }),
      }).catch(() => {})
      await sub.unsubscribe().catch(() => {})
    }
    return ''
  } catch {
    return 'Could not turn notifications off.'
  }
}

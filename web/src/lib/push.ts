// Web Push enablement. On iOS this only works from a PWA installed to the home
// screen, and permission must be requested from a user gesture — hence the
// explicit enable button rather than an on-load prompt.

// Returns an ArrayBuffer-backed view so it satisfies BufferSource for
// applicationServerKey (lib.dom's Uint8Array is generic and defaults to the
// wider ArrayBufferLike, which no longer assigns to BufferSource).
function urlBase64ToUint8Array(base64: string): Uint8Array<ArrayBuffer> {
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

function withTimeout<T>(p: Promise<T>, ms: number): Promise<T | null> {
  return Promise.race([p, new Promise<null>((r) => setTimeout(() => r(null), ms))])
}

// activeRegistration returns a ready service-worker registration, ensuring it is
// registered first. It never hangs: if the worker doesn't become active within
// the timeout it resolves null so the caller can show a message instead of the
// toggle spinning forever (navigator.serviceWorker.ready otherwise waits
// indefinitely when the worker never activates).
async function activeRegistration(timeoutMs = 8000): Promise<ServiceWorkerRegistration | null> {
  if (!('serviceWorker' in navigator)) return null
  try {
    if (!(await navigator.serviceWorker.getRegistration())) {
      await navigator.serviceWorker.register('/sw.js')
    }
  } catch {
    /* the ready race below still gives it a chance */
  }
  return withTimeout(navigator.serviceWorker.ready, timeoutMs)
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
    const reg = await withTimeout(navigator.serviceWorker.ready, 5000)
    return !!reg && !!(await reg.pushManager.getSubscription())
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
  if (perm === 'denied') {
    return 'Notifications are blocked for this site. Allow them in the browser site settings (the padlock in the address bar), then try again.'
  }
  if (perm !== 'granted') return 'Notifications were not allowed. Click the toggle again and choose Allow.'

  // Each step is bounded so the toggle can never spin silently: a stalled
  // service worker or push service becomes a message, not a hang.
  try {
    const reg = await activeRegistration()
    if (!reg) {
      return 'The notification service worker did not start. Hard-reload (Cmd+Shift+R), then try again; if it persists, close and reopen the tab.'
    }
    const res = await fetch('/api/push/pubkey')
    if (!res.ok) return 'Push is not configured on the server.'
    const { key } = (await res.json()) as { key: string }

    const sub = await withTimeout(subscribeFresh(reg, key), 15000)
    if (!sub) {
      return 'The browser push service did not respond (often a network/VPN/firewall issue reaching Google/Mozilla push). Check the connection and try again.'
    }
    const post = await fetch('/api/push/subscribe', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(sub),
    })
    if (!post.ok) return 'Could not register the subscription with the server.'
    return ''
  } catch (e) {
    return `Could not subscribe: ${(e as Error).message}`
  }
}

// subscribeFresh subscribes with the given VAPID key, recovering from a stale
// subscription left by a previous/different key (which otherwise rejects with
// "a subscription with a different applicationServerKey already exists").
async function subscribeFresh(
  reg: ServiceWorkerRegistration,
  key: string,
): Promise<PushSubscription> {
  const opts = { userVisibleOnly: true, applicationServerKey: urlBase64ToUint8Array(key) }
  try {
    return await reg.pushManager.subscribe(opts)
  } catch (e) {
    const existing = await reg.pushManager.getSubscription()
    if (existing) {
      await existing.unsubscribe().catch(() => {})
      return reg.pushManager.subscribe(opts)
    }
    throw e
  }
}

// disablePush unsubscribes this device and drops it from the server. The
// browser permission itself can't be revoked programmatically, so a later
// re-enable won't re-prompt — it just re-subscribes.
export async function disablePush(): Promise<string> {
  if (!pushSupported()) return ''
  try {
    const reg = await withTimeout(navigator.serviceWorker.ready, 5000)
    const sub = reg ? await reg.pushManager.getSubscription() : null
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

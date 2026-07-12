/// <reference lib="webworker" />
/* Custom service worker (vite-plugin-pwa injectManifest). Precaches the app
   shell for instant, offline-capable launch; API and WebSocket traffic always
   hit the network. Push handlers are intentionally generic: a wake-up shows a
   neutral notification, and the real content is pulled fresh over Tailscale when
   the app reconnects — no session content ever rides the push channel. */

declare const self: ServiceWorkerGlobalScope & {
  __WB_MANIFEST: { url: string; revision: string | null }[]
}

const CACHE = 'kunai-shell-v2'
const ASSETS = self.__WB_MANIFEST.map((e) => e.url)

self.addEventListener('install', (event) => {
  event.waitUntil(
    (async () => {
      const cache = await caches.open(CACHE)
      await cache.addAll([...new Set([...ASSETS, '/', '/index.html'])])
      await self.skipWaiting()
    })(),
  )
})

self.addEventListener('activate', (event) => {
  event.waitUntil(
    (async () => {
      const keys = await caches.keys()
      await Promise.all(keys.filter((k) => k !== CACHE).map((k) => caches.delete(k)))
      await self.clients.claim()
    })(),
  )
})

self.addEventListener('fetch', (event) => {
  const req = event.request
  const url = new URL(req.url)
  if (req.method !== 'GET' || url.origin !== self.location.origin) return
  // Never cache live data channels.
  if (url.pathname.startsWith('/api') || url.pathname.startsWith('/ws')) return

  // App shell: navigations are network-FIRST so a normal refresh always picks up
  // the latest deploy (cache-first here served a stale app until a hard refresh).
  // Falls back to the cached shell when offline. Fingerprinted assets below stay
  // cache-first since they're immutable.
  if (req.mode === 'navigate') {
    event.respondWith(
      fetch(req)
        .then((res) => {
          const copy = res.clone()
          caches.open(CACHE).then((c) => c.put('/index.html', copy))
          return res
        })
        .catch(() => caches.match('/index.html').then((r) => r ?? fetch(req))),
    )
    return
  }
  event.respondWith(
    caches.match(req).then(
      (hit) =>
        hit ??
        fetch(req).then((res) => {
          const copy = res.clone()
          caches.open(CACHE).then((c) => c.put(req, copy))
          return res
        }),
    ),
  )
})

// A push is only ever a generic wake-up. Show a neutral notification; the app
// fetches the real state on reconnect.
self.addEventListener('push', (event) => {
  let title = 'Kunai'
  let body = 'A session needs your attention'
  try {
    const data = event.data?.json() as { title?: string; body?: string } | undefined
    if (data?.title) title = data.title
    if (data?.body) body = data.body
  } catch {
    if (event.data?.text()) body = event.data.text()
  }
  event.waitUntil(
    self.registration.showNotification(title, {
      body,
      icon: '/icon-192.png',
      badge: '/icon-192.png',
      tag: 'kunai',
    }),
  )
})

self.addEventListener('notificationclick', (event) => {
  event.notification.close()
  event.waitUntil(
    (async () => {
      const clients = await self.clients.matchAll({ type: 'window', includeUncontrolled: true })
      for (const c of clients) {
        if ('focus' in c) return c.focus()
      }
      return self.clients.openWindow('/')
    })(),
  )
})

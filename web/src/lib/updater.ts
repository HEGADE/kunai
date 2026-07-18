// Keep a long-lived PWA current without a manual refresh.
//
// The browser only re-checks the service worker on a navigation, so an app left
// open for hours never notices a deploy — which is why a new build used to sit
// there until you reloaded by hand. Here we poll registration.update() on an
// interval and whenever the tab comes back to the foreground; when the new
// worker takes control (the controllerchange handler in main.ts) the page
// reloads to swap in the fresh assets.
//
// The reload is held while there's an unsent prompt or a staged attachment,
// since that's the only thing a reload would actually lose; the moment the
// composer is clear (you sent it, or emptied it) the held reload applies. With
// nothing in the composer a reload is harmless — the app re-seeds its state from
// the server and pins back to the bottom — so it just happens.

const CHECK_INTERVAL_MS = 30_000
const RETRY_MS = 1500

// The app registers a predicate that is true while a reload would lose work
// (an unsent prompt or a staged attachment). Defaults to "always safe" so the
// updater works even before the composer has mounted.
let hasUnsavedWork: () => boolean = () => false
export function setReloadGuard(fn: () => boolean): void {
  hasUnsavedWork = fn
}

let pendingReload = false

// Called by main.ts on controllerchange: a new worker now controls the page, so
// the running code is stale. Reload as soon as it's safe.
export function reloadWhenSafe(): void {
  pendingReload = true
  drain()
}

function drain(): void {
  if (!pendingReload) return
  if (!hasUnsavedWork()) {
    location.reload()
    return
  }
  // A draft is in flight: check back shortly. This runs only while a reload is
  // actually pending, so it isn't a standing timer.
  setTimeout(drain, RETRY_MS)
}

// Start the background poll for new builds. Safe to call once at startup.
export function startUpdatePolling(): void {
  if (!('serviceWorker' in navigator)) return
  navigator.serviceWorker.ready
    .then((reg) => {
      const check = () => {
        reg.update().catch(() => {})
      }
      check()
      setInterval(check, CHECK_INTERVAL_MS)
      window.addEventListener('focus', check)
      document.addEventListener('visibilitychange', () => {
        if (document.visibilityState === 'visible') check()
      })
    })
    .catch(() => {})
}

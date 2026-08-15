import { mount } from 'svelte'
import '@fontsource-variable/geist'
import '@fontsource-variable/geist-mono'
import '@fontsource-variable/source-serif-4'
// The review workspace's own two faces. Loaded here rather than in the view so
// they are in the bundle before the screen opens: a review is read, and type
// swapping under a paragraph mid-read is worse than a moment of Geist.
import '@fontsource-variable/inter/index.css'
import '@fontsource-variable/jetbrains-mono/index.css'
import './review.css'
import './app.css'
import './hljs-theme.css'
import './image-frame.css'
// One card, one row, one button, one field, shared by every settings section.
// Global for the reason the file says: scoped styles are what let six sections
// each describe a different row.
import './settings.css'
import App from './App.svelte'
import { startUpdatePolling, reloadWhenSafe } from './lib/updater'

// When a new service worker takes control after a deploy, reload once so the page
// swaps to the fresh assets instead of stranding the user on the old build. Guard
// against the first-ever install (no prior controller) and against reload loops.
// The reload is deferred to a safe moment (updater.ts) so it never eats a
// half-typed prompt; startUpdatePolling is what makes a long-open PWA discover
// the deploy at all, instead of waiting for a manual refresh.
if ('serviceWorker' in navigator) {
  const hadController = !!navigator.serviceWorker.controller
  let reloaded = false
  navigator.serviceWorker.addEventListener('controllerchange', () => {
    if (reloaded || !hadController) return
    reloaded = true
    reloadWhenSafe()
  })
  startUpdatePolling()
}

const app = mount(App, {
  target: document.getElementById('app')!,
})

export default app

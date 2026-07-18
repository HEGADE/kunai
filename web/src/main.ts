import { mount } from 'svelte'
import '@fontsource-variable/geist'
import '@fontsource-variable/geist-mono'
import '@fontsource-variable/source-serif-4'
import './app.css'
import './hljs-theme.css'
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

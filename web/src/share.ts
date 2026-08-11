// Entry for the guest page: the app somebody sees when they open a share link.
//
// Separate from main.ts on purpose, and the difference is not cosmetic. This one
// registers no service worker and starts no update polling. The PWA belongs to
// the machine's owner, is scoped to /, and installing it or caching its assets is
// not something a visitor holding a temporary link should be doing. It also has
// no app store, no fleet, and no notion of other machines: there is exactly one
// conversation reachable from here.
import { mount } from 'svelte'
import '@fontsource-variable/geist'
import '@fontsource-variable/geist-mono'
import '@fontsource-variable/source-serif-4'
import './app.css'
import './hljs-theme.css'
import './image-frame.css'
import Share from './Share.svelte'

export default mount(Share, {
  target: document.getElementById('share')!,
})

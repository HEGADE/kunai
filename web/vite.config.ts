import { fileURLToPath } from 'node:url'
import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'
import { VitePWA } from 'vite-plugin-pwa'

// The build lands in the Go server's embed directory so the whole PWA ships
// inside the single kunai binary.
export default defineConfig({
  plugins: [
    svelte(),
    VitePWA({
      strategies: 'injectManifest',
      srcDir: 'src',
      filename: 'sw.ts',
      injectRegister: 'auto',
      injectManifest: {
        globPatterns: ['**/*.{js,css,html,png,svg,webmanifest}'],
        // The guest page is not part of the owner's app and must not be
        // precached into it: the PWA is scoped to /, share.html is served only
        // from the public listener, and caching it here would ship a stranger's
        // entry point to every installed client.
        globIgnores: ['share.html'],
      },
      manifest: {
        id: '/',
        name: 'Kunai',
        short_name: 'Kunai',
        description: 'Self-hosted, relay-free mobile client for Claude Code',
        theme_color: '#0b0b0c',
        background_color: '#0b0b0c',
        display: 'standalone',
        display_override: ['standalone'],
        start_url: '/',
        scope: '/',
        icons: [
          { src: 'icon-192.png', sizes: '192x192', type: 'image/png' },
          { src: 'icon-512.png', sizes: '512x512', type: 'image/png' },
          { src: 'icon-512.png', sizes: '512x512', type: 'image/png', purpose: 'maskable' },
        ],
      },
      devOptions: { enabled: false, type: 'module' },
    }),
  ],
  build: {
    outDir: '../internal/webui/dist',
    emptyOutDir: true,
    rollupOptions: {
      // Two entries: the owner's app, and the page a share link opens. They share
      // their component chunks, so rendering a shared conversation costs one HTML
      // file rather than a second copy of the renderer.
      input: {
        index: fileURLToPath(new URL('./index.html', import.meta.url)),
        share: fileURLToPath(new URL('./share.html', import.meta.url)),
      },
    },
  },
})

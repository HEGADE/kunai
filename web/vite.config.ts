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
      },
      manifest: {
        name: 'Kunai',
        short_name: 'Kunai',
        description: 'Self-hosted, relay-free mobile client for Claude Code',
        theme_color: '#0e0f12',
        background_color: '#0e0f12',
        display: 'standalone',
        orientation: 'portrait',
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
  },
})

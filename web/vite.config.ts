import { fileURLToPath, URL } from 'node:url'
import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import tailwindcss from '@tailwindcss/vite'
import Components from 'unplugin-vue-components/vite'
import { PrimeVueResolver } from '@primevue/auto-import-resolver'

// Proxy /api -> backend. Inside the docker-compose `web` service the backend
// is reachable as `https://prexel:3000` over prexel-net. Override with
// PREXEL_PROXY_TARGET when running `npm run dev` directly on the host.
const proxyTarget = process.env.PREXEL_PROXY_TARGET || 'https://prexel:3000'

export default defineConfig({
  plugins: [
    vue(),
    tailwindcss(),
    Components({
      resolvers: [PrimeVueResolver()],
      dts: 'components.d.ts',
    }),
  ],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  define: {
    __PREXEL_WEB_VERSION__: JSON.stringify(
      process.env.npm_package_version ?? '0.0.0',
    ),
  },
  server: {
    host: '0.0.0.0',
    port: 5173,
    proxy: {
      '/api': {
        target: proxyTarget,
        changeOrigin: true,
        secure: false,
        cookieDomainRewrite: '',
        // ws: true upgrades WebSocket connections through the proxy.
        // The ContainerTerminal opens `ws(s)://<host>/api/v1/.../exec`
        // and without this Vite swallows the upgrade request — the
        // terminal hangs forever on "Connecting to container...".
        ws: true,
      },
    },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    sourcemap: false,
    chunkSizeWarningLimit: 1500,
  },
})

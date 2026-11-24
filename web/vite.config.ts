import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// The dev server proxies the Go API so `npm run dev` works against
// `meridian-server serve` on :8080. Point it at another backend when needed,
// for example a leader that has the admin console enabled:
//
//   MERIDIAN_API_TARGET=http://127.0.0.1:8095 npm run dev
//
// A production build is written straight into the Go embed directory and
// baked into the binary.
const apiTarget = process.env.MERIDIAN_API_TARGET ?? 'http://127.0.0.1:8080'

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      '/v1': { target: apiTarget, changeOrigin: true },
      '/ml': { target: apiTarget, changeOrigin: true },
      '/status': { target: apiTarget, changeOrigin: true },
      '/policies': { target: apiTarget, changeOrigin: true },
    },
  },
  build: {
    outDir: '../internal/webui/dist',
    emptyOutDir: true,
  },
})

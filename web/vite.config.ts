import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// The dev server proxies the Go API so `npm run dev` works against
// `meridian-server serve` on :8080. A production build is written straight
// into the Go embed directory and baked into the binary.
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      '/v1': { target: 'http://127.0.0.1:8080', changeOrigin: true },
      '/ml': { target: 'http://127.0.0.1:8080', changeOrigin: true },
      '/status': { target: 'http://127.0.0.1:8080', changeOrigin: true },
      '/policies': { target: 'http://127.0.0.1:8080', changeOrigin: true },
    },
  },
  build: {
    outDir: '../internal/webui/dist',
    emptyOutDir: true,
  },
})

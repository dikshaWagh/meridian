import { defineConfig, loadEnv } from 'vite'
import react from '@vitejs/plugin-react'

// The Go server sends no CORS headers, so development traffic goes through the
// Vite proxy. VITE_MERIDIAN_URL is the upstream Meridian server.
export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), '')
  const target = env.VITE_MERIDIAN_URL || 'http://localhost:8080'

  return {
    plugins: [react()],
    server: {
      port: 5173,
      proxy: {
        '/status': { target, changeOrigin: true },
        '/v1': { target, changeOrigin: true },
        '/healthz': { target, changeOrigin: true },
      },
    },
  }
})

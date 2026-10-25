import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

// The backend (Gin, :8080) issues an HttpOnly `ssid` cookie but its CORS setup
// does not allow credentials, so the browser talks to it through this proxy and
// every request stays same-origin.
const backend = process.env.VITE_BACKEND_URL || 'http://localhost:8080'

export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      '/session-manager': { target: backend, changeOrigin: false },
      '/admin-control-panel': { target: backend, changeOrigin: false },
    },
  },
})

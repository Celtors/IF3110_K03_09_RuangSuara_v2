import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

const page = (file) => new URL(file, import.meta.url).pathname

// Multi-page app: every HTML file below is its own page with its own entry script.
// Navigation between pages is a normal link / full page load.
//
// The backend (Gin, :8080) issues an HttpOnly `ssid` cookie but its CORS setup does
// not allow credentials, so the browser talks to it through this proxy and every
// request stays same-origin.
const backend = process.env.VITE_BACKEND_URL || 'http://localhost:8080'

export default defineConfig({
  plugins: [react()],
  appType: 'mpa',
  build: {
    rollupOptions: {
      input: {
        index: page('./index.html'),
        login: page('./login.html'),
        users: page('./admin/users.html'),
        groups: page('./admin/groups.html'),
        policies: page('./admin/policies.html'),
      },
    },
  },
  server: {
    proxy: {
      '/session-manager': { target: backend },
      '/admin-control-panel': { target: backend },
    },
  },
})

import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'
import { fileURLToPath } from 'node:url'

const page = (file) => fileURLToPath(new URL(file, import.meta.url))

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
        home: page('./home.html'),
        users: page('./admin/users.html'),
        groups: page('./admin/groups.html'),
        policies: page('./admin/policies.html'),
        migrationHome: page('./migrations/index.html'),
        migrationHistory: page('./migrations/history.html'),
        migrationEditor: page('./migrations/editor.html'),
        migrationDetails: page('./migrations/details.html'),
        migrationSchemaDiff: page('./migrations/schema-diff.html'),
        migrationQueryPlan: page('./migrations/query-plan.html'),
      },
    },
  },
  server: {
    proxy: {
      '/api': { target: backend },
      '/session-manager': { target: backend },
      '/admin-control-panel': { target: backend },
    },
  },
})

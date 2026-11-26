import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'
import { fileURLToPath } from 'node:url'

const page = (file) => fileURLToPath(new URL(file, import.meta.url))

// Multi-page app: every HTML file below is its own page with its own entry script, and moving
// between them is a normal link / full page load. The one exception is the migration app,
// which is a single-page app: migrations/index.html is its only entry and a client-side
// router (src/migration/router.js) shows the page named in the URL.
//
// The backend (Gin, :8080) issues an HttpOnly `ssid` cookie but its CORS setup does
// not allow credentials, so the browser talks to it through this proxy and every
// request stays same-origin.
const backend = process.env.VITE_BACKEND_URL || 'http://localhost:8080'

// The migration app keeps its /migrations/<page>.html URLs, so every one of them (opened
// directly or refreshed) must get the same shell. Production does this in the Go router.
const MIGRATION_PAGE = /^\/migrations\/(?:(?:history|editor|details|schema-diff|query-plan|change-request)\.html)?(\?.*)?$/
const migrationShell = {
  name: 'migration-spa-fallback',
  configureServer: (server) => server.middlewares.use(rewriteMigrationPage),
  configurePreviewServer: (server) => server.middlewares.use(rewriteMigrationPage),
}
function rewriteMigrationPage(req, _res, next) {
  const m = MIGRATION_PAGE.exec(req.url ?? '')
  if (m) req.url = '/migrations/index.html' + (m[1] ?? '')
  next()
}

export default defineConfig({
  plugins: [react(), migrationShell],
  appType: 'mpa',
  build: {
    rollupOptions: {
      input: {
        index: page('./index.html'),
        login: page('./login.html'),
        home: page('./home.html'),
        users: page('./admin/users.html'),
        groups: page('./admin/groups.html'),
        databases: page('./admin/databases.html'),
        policies: page('./admin/policies.html'),
        migrationHome: page('./migrations/index.html'),
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
  test: {
    environment: 'jsdom',
    setupFiles: './src/test/setup.js',
    include: ['src/**/*.test.{js,jsx}'],
  },
})

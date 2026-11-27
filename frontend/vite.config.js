import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'
import { matchPath } from './src/paths.js'

// Single-page app: index.html is the only HTML entry and src/router.js shows the screen named
// in the URL (/login, /admin/users, /migrations/details?...; the old .html names still work). The
// server has to answer every one of them with the same shell. Production does that in the Go
// router (backend/src/internal/app/router.go); this plugin does it for `vite` and `vite preview`.
//
// The backend (Gin, :8080) issues an HttpOnly `ssid` cookie but its CORS setup does
// not allow credentials, so the browser talks to it through this proxy and every
// request stays same-origin.
const backend = process.env.VITE_BACKEND_URL || 'http://localhost:8080'

function serveShell(req, _res, next) {
  const [pathname, query = ''] = (req.url ?? '').split('?')
  if (matchPath(pathname) && pathname !== '/') req.url = '/index.html' + (query ? `?${query}` : '')
  next()
}
const spaShell = {
  name: 'spa-shell',
  // Braces, not an expression body: a hook that returns a function is run by Vite as a "post"
  // hook, and `use()` returns the connect app itself.
  configureServer(server) {
    server.middlewares.use(serveShell)
  },
  configurePreviewServer(server) {
    server.middlewares.use(serveShell)
  },
}

export default defineConfig({
  plugins: [react(), spaShell],
  appType: 'spa',
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

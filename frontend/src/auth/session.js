import { api } from '../api/endpoints'

export const LOGIN_PAGE = '/login.html'
export const ADMIN_PAGE = '/admin/users.html'
export const HOME_PAGE = '/home.html'

// The session lives in an HttpOnly cookie that JavaScript cannot read, so the server is the
// only source of truth: every page asks /session-manager/me who is signed in and whether
// they may use the admin console. Nothing about the user is cached in the browser.

// Resolves to { username, admin, admin_access } or null when nobody is signed in.
// Other failures (for example the backend being down) are thrown to the caller.
export async function fetchMe() {
  try {
    return await api.me()
  } catch (e) {
    if (e.status === 401) return null
    throw e
  }
}

export const login = (username, password) => api.login(username, password)

export async function logout() {
  try {
    await api.logout()
  } catch {
    /* the session may already be gone server-side; carry on to the login page regardless */
  }
}

// Only same-site absolute paths are accepted as a post-login destination.
export function safeNext(raw) {
  return raw && raw.startsWith('/') && !raw.startsWith('//') && !raw.startsWith('/\\') ? raw : ADMIN_PAGE
}

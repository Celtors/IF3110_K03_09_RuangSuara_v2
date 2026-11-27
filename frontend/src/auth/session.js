import { api } from '../api/endpoints'
import { matchPath } from '../paths'

export const LOGIN_PAGE = '/login'
export const ADMIN_PAGE = '/admin/users'
export const HOME_PAGE = '/home'

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

// Where to go after signing in. A ?next= destination is honoured only when it is a page of this
// app (same-site path, nothing the server answers itself) and the user may open it; otherwise
// admins land on the admin console and everyone else on the home page.
export function landingFor(me, rawNext) {
  const ok = rawNext && rawNext.startsWith('/') && !rawNext.startsWith('//') && !rawNext.startsWith('/\\')
  if (ok) {
    const route = matchPath(rawNext.split(/[?#]/)[0])
    if (route && route.name !== 'login' && route.name !== 'root' && (route.name !== 'admin' || me.admin)) return rawNext
  }
  return me.admin ? ADMIN_PAGE : HOME_PAGE
}

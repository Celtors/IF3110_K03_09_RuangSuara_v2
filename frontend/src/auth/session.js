import { api } from '../api/endpoints'

export const LOGIN_PAGE = '/login.html'
export const HOME_PAGE = '/admin/users.html'

const KEY = 'ruangsuara.user'

// The session itself lives in an HttpOnly cookie that JavaScript cannot read. Only the
// username is remembered here (never the password or session id) so each page knows
// whether to show the panel or send the visitor to the login page.
export function getUsername() {
  try {
    return localStorage.getItem(KEY)
  } catch {
    return null
  }
}

function remember(value) {
  try {
    if (value) localStorage.setItem(KEY, value)
    else localStorage.removeItem(KEY)
  } catch {
    /* storage unavailable: the visitor will be asked to sign in again on the next page */
  }
}

export async function login(username, password) {
  await api.login(username, password)
  remember(username)
}

export async function logout() {
  try {
    await api.logout()
  } catch {
    /* the session may already be gone server-side; sign out locally regardless */
  }
  remember(null)
}

// Only same-site absolute paths are accepted as a post-login destination.
export function safeNext(raw) {
  return raw && raw.startsWith('/') && !raw.startsWith('//') && !raw.startsWith('/\\') ? raw : HOME_PAGE
}

export class ApiError extends Error {
  constructor(message, status = 0, code = '') {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
  }
}

const sessionPath = '/session-manager/me'
const loginPath = '/session-manager/login'
const csrfHeader = 'X-CSRF-Token'

let csrf = ''
let refreshToken

async function sessionResponse() {
  const response = await fetch(sessionPath, { credentials: 'same-origin' })
  csrf = response.ok ? response.headers.get(csrfHeader) ?? '' : ''
  return response
}

// Only retry a mutation when the server explicitly rejected it before dispatch.
export async function sessionFetch(path, method = 'GET', body) {
  if (path === sessionPath) return sessionResponse()
  const headers = new Headers()
  if (body !== undefined) headers.set('Content-Type', 'application/json')
  const send = () => fetch(path, {
    method,
    credentials: 'same-origin',
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
  })

  if (method === 'GET' || method === 'HEAD' || method === 'OPTIONS' || path === loginPath) {
    const response = await send()
    if (path === loginPath && response.ok) csrf = ''
    return response
  }

  for (let attempt = 0; attempt < 2; attempt++) {
    if (!csrf) {
      refreshToken ??= sessionResponse().finally(() => { refreshToken = undefined })
      const response = await refreshToken
      if (!response.ok) return response.clone()
      if (!csrf) throw new ApiError('The session did not return a CSRF token. Refresh and try again.')
    }
    headers.set(csrfHeader, csrf)
    const response = await send()
    if (response.status !== 403 || attempt === 1) {
      if (path === '/session-manager/logout' && response.ok) csrf = ''
      return response
    }
    const error = await response.clone().json().catch(() => null)
    if (error?.error?.code !== 'csrf_failed') return response
    csrf = ''
  }
}

// Backend error shapes:
//   login/logout:  { error: { code, message, requestId } }
//   control panel: { status: "error", message }
async function request(method, path, body) {
  let res
  try {
    res = await sessionFetch(path, method, body)
  } catch (error) {
    if (error instanceof ApiError) throw error
    throw new ApiError('Cannot reach the server. Check that the backend is running on port 8080.')
  }

  const text = await res.text()
  let data = null
  if (text) {
    try {
      data = JSON.parse(text)
    } catch {
      data = null
    }
  }

  if (!res.ok) {
    const message =
      data?.error?.message || data?.message || `Request failed with status ${res.status}.`
    throw new ApiError(message, res.status, data?.error?.code)
  }
  return data
}

export const http = {
  get: (path) => request('GET', path),
  post: (path, body) => request('POST', path, body),
  patch: (path, body) => request('PATCH', path, body),
  delete: (path, body) => request('DELETE', path, body),
}

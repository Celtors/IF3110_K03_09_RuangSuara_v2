import { afterEach, expect, it, vi } from 'vitest'

afterEach(() => vi.unstubAllGlobals())

it('renews the CSRF proof and retries a write rejected before dispatch', async () => {
  vi.resetModules()
  const fetch = vi.fn()
    .mockResolvedValueOnce(new Response('{}', { headers: { 'X-CSRF-Token': 'previous-proof' } }))
    .mockResolvedValueOnce(new Response('{"error":{"code":"csrf_failed"}}', { status: 403 }))
    .mockResolvedValueOnce(new Response('{}', { headers: { 'X-CSRF-Token': 'current-proof' } }))
    .mockResolvedValueOnce(new Response('{}', { status: 200 }))
  vi.stubGlobal('fetch', fetch)
  const { http } = await import('./client')
  await http.patch('/admin-control-panel/users', { status: 'Inactive' })
  expect(fetch.mock.calls.map(([path]) => path)).toEqual([
    '/session-manager/me', '/admin-control-panel/users',
    '/session-manager/me', '/admin-control-panel/users',
  ])
  expect(fetch.mock.calls[3][1].headers.get('X-CSRF-Token')).toBe('current-proof')
})

it('preserves a logout storage error without retrying the write', async () => {
  vi.resetModules()
  const fetch = vi.fn()
    .mockResolvedValueOnce(new Response('{}', { headers: { 'X-CSRF-Token': 'proof' } }))
    .mockResolvedValueOnce(new Response('{"error":{"message":"Could not revoke the session"}}', { status: 503 }))
  vi.stubGlobal('fetch', fetch)
  const { logout } = await import('../auth/session')
  await expect(logout()).rejects.toMatchObject({ status: 503, message: 'Could not revoke the session' })
  expect(fetch).toHaveBeenCalledTimes(2)
})

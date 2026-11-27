import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { flushSync } from 'react-dom'
import { LOGIN_PAGE, fetchMe, login, logout } from './session'
import { navigate } from '../router'
import { SessionContext } from './session-context'

// Who is signed in, asked of the server once when the app starts and again when the tab becomes
// visible (a session that expired in the background sends the user to the sign-in page instead
// of leaving them on a screen whose requests all fail). The session itself is an HttpOnly cookie,
// so nothing about the user is cached in the browser.
//
//   status: 'loading' until the first answer, then 'ready'
//   me:     { id, username, admin, admin_access, ... } or null when nobody is signed in
//   error:  message of the last failed check (backend down), '' otherwise
export default function SessionProvider({ children }) {
  const [state, setState] = useState({ status: 'loading', me: null, error: '' })

  const generation = useRef(0)

  const refresh = useCallback(async () => {
    const current = generation.current
    try {
      const me = await fetchMe()
      if (current !== generation.current) return null
      setState((s) => (s.status === 'ready' && s.error === '' && JSON.stringify(s.me) === JSON.stringify(me) ? s : { status: 'ready', me, error: '' }))
      return me
    } catch (e) {
      if (current !== generation.current) return null
      // keep whoever was signed in: a backend that is briefly unreachable is not a sign-out
      setState((s) => ({ status: 'ready', me: s.me, error: e.message }))
      return null
    }
  }, [])

  useEffect(() => {
    refresh()
    const onVisible = () => document.visibilityState === 'visible' && refresh()
    document.addEventListener('visibilitychange', onVisible)
    return () => document.removeEventListener('visibilitychange', onVisible)
  }, [refresh])

  const signIn = useCallback(async (username, password) => {
    generation.current++
    await login(username, password)
    const me = await fetchMe()
    setState({ status: 'ready', me, error: '' })
    return me
  }, [])

  // Ends the session, then shows the sign-in page without a ?next= (leaving on purpose is not
  // the same as being thrown out).
  const signOut = useCallback(async () => {
    generation.current++
    try {
      await logout()
    } catch (error) {
      setState((s) => ({ ...s, error: error.message }))
      return
    }
    generation.current++
    // One synchronous update for both: if the URL changed first, the sign-in screen would still
    // see a signed-in user and send them straight back; if the session changed first, the old
    // screen would redirect to sign-in with a ?next= pointing at itself.
    flushSync(() => {
      navigate(LOGIN_PAGE, { replace: true })
      setState({ status: 'ready', me: null, error: '' })
    })
  }, [])

  const value = useMemo(() => ({ ...state, signIn, signOut, refresh }), [state, signIn, signOut, refresh])
  return <SessionContext.Provider value={value}>{children}</SessionContext.Provider>
}

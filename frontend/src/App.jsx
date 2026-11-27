import { Suspense, lazy, useEffect } from 'react'
import { LOGIN_PAGE, ADMIN_PAGE, HOME_PAGE, landingFor } from './auth/session'
import SessionProvider from './auth/SessionProvider'
import { useSession } from './auth/session-context'
import Redirect from './components/Redirect'
import { cleanPath, matchPath } from './paths'
import Login from './pages/Login'
import NotFound from './pages/NotFound'
import UserHome from './pages/UserHome'
import { interceptLinks, useLocation } from './router'

// The big areas load on demand: the login page does not ship the migration workspace.
const AdminScreen = lazy(() => import('./admin/AdminScreen'))
const MigrationApp = lazy(() => import('./migration/MigrationApp').then((m) => ({ default: m.MigrationApp })))

const TITLES = {
  login: 'Sign in',
  home: 'Home',
  users: 'Users',
  groups: 'Groups',
  databases: 'Databases',
  policies: 'Database access',
}

function Loading() {
  return (
    <main className="content">
      <p role="status" className="muted">Loading…</p>
    </main>
  )
}

function StartupError({ message, onRetry }) {
  return (
    <main className="content">
      <p className="alert alert-error" role="alert">{message}</p>
      <button type="button" className="btn btn-primary" onClick={onRetry}>Try again</button>
    </main>
  )
}

// The route table: which screen a URL shows and who may see it. The server still decides what
// each API call may do; this only keeps people out of screens that would fail for them.
//   /login                 signed-out visitors; the signed-in are sent on to where they were going
//   /home                  any signed-in user
//   /admin/<section>       admins (others go home, visitors go to sign in and come back)
//   /migrations[/<page>]   any signed-in user (the migration APIs check database access)
// Old .html URLs are replaced by the clean form before anything else happens.
function Routes() {
  const { pathname, search, hash } = useLocation()
  const { status, me, error, refresh } = useSession()
  const route = matchPath(pathname)
  const name = route?.name

  useEffect(() => {
    // the migration workspace titles its own pages
    if (name === 'migrations') return
    const title = TITLES[route?.page ?? name]
    document.title = title ? `${title} · RuangSuara` : 'RuangSuara'
  }, [name, route?.page])
  useEffect(() => {
    if (name !== 'migrations') window.scrollTo(0, 0)
  }, [pathname, name])

  if (route && cleanPath(pathname) !== pathname) return <Redirect to={cleanPath(pathname) + search + hash} />
  if (status === 'loading') return <Loading />
  if (!route) return <NotFound />
  if (name === 'root') return <Redirect to={ADMIN_PAGE} />

  if (name === 'login') {
    if (me) return <Redirect to={landingFor(me, new URLSearchParams(search).get('next'))} />
    // backend unreachable: still show the form, the sign-in attempt will report the problem
    return <Login />
  }

  // everything below needs a signed-in user
  if (!me) {
    if (error) return <StartupError message={error} onRetry={refresh} />
    if (name === 'home') return <Redirect to={LOGIN_PAGE} />
    const back = name === 'migrations' ? pathname + search : pathname
    return <Redirect to={`${LOGIN_PAGE}?next=${encodeURIComponent(back)}`} />
  }
  if (name === 'home') return <UserHome username={me.username} admin={me.admin} />
  if (name === 'admin') {
    if (!me.admin) return <Redirect to={HOME_PAGE} />
    return (
      <Suspense fallback={<Loading />}>
        <AdminScreen active={route.page} me={me} />
      </Suspense>
    )
  }
  return (
    <Suspense fallback={<Loading />}>
      <MigrationApp />
    </Suspense>
  )
}

export default function App() {
  // Plain clicks on links between screens navigate in place instead of loading a new document.
  useEffect(() => {
    document.addEventListener('click', interceptLinks)
    return () => document.removeEventListener('click', interceptLinks)
  }, [])
  return (
    <SessionProvider>
      <Routes />
    </SessionProvider>
  )
}

import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import '../index.css'
import { HOME_PAGE, LOGIN_PAGE, fetchMe } from '../auth/session'
import AdminShell from './AdminShell'

function StartupError({ message }) {
  return (
    <main className="content">
      <p className="alert alert-error" role="alert">{message}</p>
    </main>
  )
}

// Shared start-up for every admin page. The server decides who gets in: visitors who are
// not signed in go to the login page (remembering where they were going), signed-in users
// without admin access go to the home page, and only admins see the console.
export async function mountAdmin(Page, active) {
  const root = createRoot(document.getElementById('root'))
  let me
  try {
    me = await fetchMe()
  } catch (e) {
    root.render(<StartupError message={e.message} />)
    return
  }
  if (!me) {
    window.location.replace(`${LOGIN_PAGE}?next=${encodeURIComponent(window.location.pathname)}`)
    return
  }
  if (!me.admin) {
    window.location.replace(HOME_PAGE)
    return
  }
  root.render(
    <StrictMode>
      <AdminShell active={active} username={me.username} access={me.admin_access}>
        <Page />
      </AdminShell>
    </StrictMode>,
  )
}

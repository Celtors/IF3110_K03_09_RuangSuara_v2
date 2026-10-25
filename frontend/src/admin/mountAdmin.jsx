import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import '../index.css'
import { LOGIN_PAGE, getUsername } from '../auth/session'
import AdminShell from './AdminShell'

export function mountAdmin(Page, active) {
  if (!getUsername()) {
    window.location.replace(`${LOGIN_PAGE}?next=${encodeURIComponent(window.location.pathname)}`)
    return
  }
  createRoot(document.getElementById('root')).render(
    <StrictMode>
      <AdminShell active={active}>
        <Page />
      </AdminShell>
    </StrictMode>,
  )
}

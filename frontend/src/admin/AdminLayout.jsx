import { NavLink, Navigate, Outlet, useLocation, useNavigate } from 'react-router-dom'
import { useAuth } from '../auth/auth-context'
import ToastProvider from '../components/ToastProvider'

const NAV = [
  { to: '/admin/users', label: 'Users' },
  { to: '/admin/groups', label: 'Groups' },
  { to: '/admin/policies', label: 'Database access' },
]

export default function AdminLayout() {
  const { username, logout } = useAuth()
  const navigate = useNavigate()
  const location = useLocation()

  if (!username) return <Navigate to="/login" replace state={{ from: location.pathname }} />

  async function signOut() {
    await logout()
    navigate('/login', { replace: true })
  }

  return (
    <ToastProvider>
      <div className="shell">
        <aside className="sidebar">
          <span className="brand-mark">RuangSuara</span>
          <nav aria-label="Admin sections">
            {NAV.map((n) => (
              <NavLink key={n.to} to={n.to} className="nav-link">
                {n.label}
              </NavLink>
            ))}
          </nav>
          <div className="sidebar-foot">
            <span className="who" title={username}>{username}</span>
            <button type="button" className="btn btn-small btn-onDark" onClick={signOut}>
              Sign out
            </button>
          </div>
        </aside>
        <main className="content">
          <Outlet />
        </main>
      </div>
    </ToastProvider>
  )
}

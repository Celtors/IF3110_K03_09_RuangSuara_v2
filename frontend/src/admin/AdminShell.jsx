import { logout, getUsername, LOGIN_PAGE } from '../auth/session'
import ToastProvider from '../components/ToastProvider'

const NAV = [
  { key: 'users', href: '/admin/users.html', label: 'Users' },
  { key: 'groups', href: '/admin/groups.html', label: 'Groups' },
  { key: 'policies', href: '/admin/policies.html', label: 'Database access' },
]

export default function AdminShell({ active, children }) {
  const username = getUsername()

  async function signOut() {
    await logout()
    window.location.assign(LOGIN_PAGE)
  }

  return (
    <ToastProvider>
      <div className="shell">
        <aside className="sidebar">
          <span className="brand-mark">RuangSuara</span>
          <nav aria-label="Admin sections">
            {NAV.map((n) => (
              <a
                key={n.key}
                href={n.href}
                className={n.key === active ? 'nav-link active' : 'nav-link'}
                aria-current={n.key === active ? 'page' : undefined}
              >
                {n.label}
              </a>
            ))}
          </nav>
          <div className="sidebar-foot">
            <span className="who" title={username}>{username}</span>
            <button type="button" className="btn btn-small btn-onDark" onClick={signOut}>
              Sign out
            </button>
          </div>
        </aside>
        <main className="content">{children}</main>
      </div>
    </ToastProvider>
  )
}

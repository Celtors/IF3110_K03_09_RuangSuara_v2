import { ADMIN_PAGE, LOGIN_PAGE, logout } from '../auth/session'

// Placeholder landing page for signed-in users who cannot use the admin console.
export default function UserHome({ username, admin }) {
  async function signOut() {
    await logout()
    window.location.assign(LOGIN_PAGE)
  }

  return (
    <main className="home">
      <div className="home-card stack">
        <span className="brand-mark home-brand">RuangSuara</span>
        <h1>Welcome, {username}</h1>
        <p className="muted">
          {admin
            ? 'You can manage users, groups and database access.'
            : 'You’re signed in, but your account can’t open the admin console. If you need access, ask a platform admin to add you to a group that has it.'}
        </p>
        <div className="actions home-actions">
          {admin && <a className="btn btn-primary" href={ADMIN_PAGE}>Open admin console</a>}
          <button type="button" className="btn" onClick={signOut}>Sign out</button>
        </div>
      </div>
    </main>
  )
}

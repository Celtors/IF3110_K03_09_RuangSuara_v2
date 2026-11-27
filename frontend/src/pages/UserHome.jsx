import { ADMIN_PAGE } from '../auth/session'
import { useSession } from '../auth/session-context'

// Placeholder landing page for signed-in users who cannot use the admin console.
export default function UserHome({ username, admin }) {
  const { signOut, error } = useSession()

  return (
    <main className="home">
      <div className="home-card stack">
        <span className="brand-mark home-brand">RuangSuara</span>
        <h1>Welcome, {username}</h1>
        <p className="muted">
          {admin
            ? 'You can manage users, groups and database access.'
            : 'You’re signed in'}
        </p>
        {error && <p className="alert alert-error" role="alert">{error}</p>}
        <div className="actions home-actions">
          <a className="btn" href="/migrations">Open migrations</a>
          {admin && <a className="btn btn-primary" href={ADMIN_PAGE}>Open admin console</a>}
          <button type="button" className="btn" onClick={signOut}>Sign out</button>
        </div>
      </div>
    </main>
  )
}

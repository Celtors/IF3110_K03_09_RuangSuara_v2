import { useState } from 'react'
import { login } from '../auth/session'

export default function Login({ next }) {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [showPassword, setShowPassword] = useState(false)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  async function onSubmit(e) {
    e.preventDefault()
    if (busy) return
    setError('')
    if (!username.trim()) return setError('Enter your username.')
    if (password.length < 8) return setError('Passwords are at least 8 characters.')

    setBusy(true)
    try {
      await login(username.trim(), password)
      window.location.assign(next)
    } catch (err) {
      setError(err.message)
      setPassword('')
      setBusy(false)
    }
  }

  return (
    <main className="login">
      <section className="login-art" aria-hidden="true">
        <div className="foam" />
        <div className="login-art-copy">
          <span className="brand-mark">RuangSuara</span>
          <p>One place to decide who can open, read and change every shared database.</p>
        </div>
      </section>

      <section className="login-pane">
        <form className="login-form stack" onSubmit={onSubmit} noValidate>
          <div>
            <h1>Sign in</h1>
            <p className="muted">Use your platform account.</p>
          </div>

          {error && <p className="alert alert-error" role="alert">{error}</p>}

          <label className="field">
            <span>Username</span>
            <input
              className="input"
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              autoComplete="username"
              maxLength={20}
              autoFocus
              required
            />
          </label>

          <label className="field">
            <span>Password</span>
            <span className="input-row">
              <input
                className="input"
                type={showPassword ? 'text' : 'password'}
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                autoComplete="current-password"
                required
              />
              <button
                type="button"
                className="btn btn-small"
                onClick={() => setShowPassword((s) => !s)}
                aria-pressed={showPassword}
              >
                {showPassword ? 'Hide' : 'Show'}
              </button>
            </span>
          </label>

          <button className="btn btn-primary btn-block" disabled={busy}>
            {busy ? 'Signing in…' : 'Sign in'}
          </button>
        </form>
      </section>
    </main>
  )
}

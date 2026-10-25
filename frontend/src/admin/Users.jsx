import { useState } from 'react'
import { api } from '../api/endpoints'
import { useResource } from '../api/useResource'
import ConfirmDialog from '../components/ConfirmDialog'
import DataState from '../components/DataState'
import Modal from '../components/Modal'
import { useToast } from '../components/toast-context'
import { formatDate } from '../components/format'
import MembershipDialog from './MembershipDialog'

const fetchUsers = () => api.listUsers()
const fetchGroups = () => api.listGroups()

function UserForm({ user, onClose, onSaved }) {
  const editing = Boolean(user)
  const [username, setUsername] = useState(user?.username ?? '')
  const [password, setPassword] = useState('')
  const [status, setStatus] = useState(user?.status ?? 'Active')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  async function submit(e) {
    e.preventDefault()
    setError('')
    if (!username.trim()) return setError('Enter a username.')
    if ((!editing || password) && password.length < 8) {
      return setError('Passwords are at least 8 characters.')
    }
    setBusy(true)
    try {
      if (editing) {
        const payload = { id: user.id, status }
        if (username.trim() !== user.username) payload.username = username.trim()
        if (password) payload.password = password
        await api.updateUser(payload)
      } else {
        await api.createUser(username.trim(), password)
      }
      onSaved(editing ? 'User updated.' : 'User created.')
      onClose()
    } catch (e2) {
      setError(e2.message)
      setBusy(false)
    }
  }

  return (
    <Modal title={editing ? `Edit ${user.username}` : 'Create user'} onClose={onClose}>
      <form className="stack" onSubmit={submit} noValidate>
        {error && <p className="alert alert-error" role="alert">{error}</p>}
        <label className="field">
          <span>Username</span>
          <input className="input" value={username} maxLength={20} onChange={(e) => setUsername(e.target.value)} autoComplete="off" autoFocus />
        </label>
        <label className="field">
          <span>{editing ? 'New password (leave empty to keep the current one)' : 'Password'}</span>
          <input className="input" type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="new-password" />
        </label>
        {editing && (
          <label className="field">
            <span>Status</span>
            <select className="select" value={status} onChange={(e) => setStatus(e.target.value)}>
              <option value="Active">Active</option>
              <option value="Inactive">Inactive</option>
            </select>
          </label>
        )}
        <div className="actions">
          <button type="button" className="btn" onClick={onClose} disabled={busy}>Cancel</button>
          <button className="btn btn-primary" disabled={busy}>{busy ? 'Saving…' : editing ? 'Save changes' : 'Create user'}</button>
        </div>
      </form>
    </Modal>
  )
}

export default function Users() {
  const toast = useToast()
  const users = useResource(fetchUsers)
  const groups = useResource(fetchGroups)
  const [query, setQuery] = useState('')
  const [modal, setModal] = useState(null)

  const list = (users.data ?? []).filter((u) => u.username.toLowerCase().includes(query.toLowerCase()))
  const close = () => setModal(null)

  return (
    <>
      <header className="page-head">
        <div>
          <h1>Users</h1>
          <p className="muted">People who can sign in. Database access comes from their groups.</p>
        </div>
        <button className="btn btn-primary" onClick={() => setModal({ type: 'form' })}>Create user</button>
      </header>

      <div className="toolbar">
        <input className="input" type="search" placeholder="Search by username" aria-label="Search users" value={query} onChange={(e) => setQuery(e.target.value)} />
      </div>

      <DataState
        loading={users.loading}
        error={users.error}
        empty={list.length === 0}
        emptyText={query ? 'No users match your search.' : 'No users yet. Create the first one.'}
        onRetry={users.reload}
      >
        <div className="table-wrap">
          <table className="table">
            <thead>
              <tr><th>Username</th><th>Status</th><th>Created</th><th><span className="sr-only">Actions</span></th></tr>
            </thead>
            <tbody>
              {list.map((u) => (
                <tr key={u.id}>
                  <td><strong>{u.username}</strong></td>
                  <td><span className={`badge ${u.status === 'Active' ? 'badge-ok' : 'badge-off'}`}>{u.status}</span></td>
                  <td className="muted">{formatDate(u.created_at)}</td>
                  <td className="row-actions">
                    <button className="btn btn-small" onClick={() => setModal({ type: 'groups', user: u })}>Groups</button>
                    <button className="btn btn-small" onClick={() => setModal({ type: 'form', user: u })}>Edit</button>
                    <button className="btn btn-small btn-danger-quiet" onClick={() => setModal({ type: 'delete', user: u })}>Delete</button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </DataState>

      {modal?.type === 'form' && (
        <UserForm user={modal.user} onClose={close} onSaved={(m) => { toast(m); users.reload() }} />
      )}
      {modal?.type === 'groups' && (
        <MembershipDialog
          mode="user"
          entity={modal.user}
          options={(groups.data ?? []).map((g) => ({ id: g.id, label: g.name }))}
          onClose={close}
        />
      )}
      {modal?.type === 'delete' && (
        <ConfirmDialog
          title="Delete user"
          confirmLabel="Delete user"
          onClose={close}
          onConfirm={async () => { await api.deleteUser(modal.user.id); toast('User deleted.'); users.reload() }}
        >
          Delete <strong>{modal.user.username}</strong>? They lose access to every database and are removed from all groups.
        </ConfirmDialog>
      )}
    </>
  )
}

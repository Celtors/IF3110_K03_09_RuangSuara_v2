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

function GroupForm({ group, onClose, onSaved }) {
  const editing = Boolean(group)
  const [name, setName] = useState(group?.name ?? '')
  const [description, setDescription] = useState(group?.description ?? '')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  async function submit(e) {
    e.preventDefault()
    setError('')
    if (!name.trim()) return setError('Enter a group name.')
    setBusy(true)
    try {
      if (editing) await api.updateGroup({ id: group.id, name: name.trim(), description })
      else await api.createGroup(name.trim(), description)
      onSaved(editing ? 'Group updated.' : 'Group created.')
      onClose()
    } catch (e2) {
      setError(e2.message)
      setBusy(false)
    }
  }

  return (
    <Modal title={editing ? `Edit ${group.name}` : 'Create group'} onClose={onClose}>
      <form className="stack" onSubmit={submit} noValidate>
        {error && <p className="alert alert-error" role="alert">{error}</p>}
        <label className="field">
          <span>Name</span>
          <input className="input" value={name} maxLength={50} onChange={(e) => setName(e.target.value)} autoFocus />
        </label>
        <label className="field">
          <span>Description</span>
          <textarea className="input" rows={3} value={description} onChange={(e) => setDescription(e.target.value)} />
        </label>
        <div className="actions">
          <button type="button" className="btn" onClick={onClose} disabled={busy}>Cancel</button>
          <button className="btn btn-primary" disabled={busy}>{busy ? 'Saving…' : editing ? 'Save changes' : 'Create group'}</button>
        </div>
      </form>
    </Modal>
  )
}

export default function Groups() {
  const toast = useToast()
  const groups = useResource(fetchGroups)
  const users = useResource(fetchUsers)
  const [query, setQuery] = useState('')
  const [modal, setModal] = useState(null)

  const list = (groups.data ?? []).filter((g) => g.name.toLowerCase().includes(query.toLowerCase()))
  const close = () => setModal(null)

  return (
    <>
      <header className="page-head">
        <div>
          <h1>Groups</h1>
          <p className="muted">Collect users, then give the group access to databases.</p>
        </div>
        <button className="btn btn-primary" onClick={() => setModal({ type: 'form' })}>Create group</button>
      </header>

      <div className="toolbar">
        <input className="input" type="search" placeholder="Search by group name" aria-label="Search groups" value={query} onChange={(e) => setQuery(e.target.value)} />
      </div>

      <DataState
        loading={groups.loading}
        error={groups.error}
        empty={list.length === 0}
        emptyText={query ? 'No groups match your search.' : 'No groups yet. Create one to start granting access.'}
        onRetry={groups.reload}
      >
        <div className="table-wrap">
          <table className="table">
            <thead>
              <tr><th>Name</th><th>Description</th><th>Created</th><th><span className="sr-only">Actions</span></th></tr>
            </thead>
            <tbody>
              {list.map((g) => (
                <tr key={g.id}>
                  <td><strong>{g.name}</strong></td>
                  <td className="muted">{g.description || '—'}</td>
                  <td className="muted">{formatDate(g.created_at)}</td>
                  <td className="row-actions">
                    <button className="btn btn-small" onClick={() => setModal({ type: 'members', group: g })}>Members</button>
                    <button className="btn btn-small" onClick={() => setModal({ type: 'form', group: g })}>Edit</button>
                    <button className="btn btn-small btn-danger-quiet" onClick={() => setModal({ type: 'delete', group: g })}>Delete</button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </DataState>

      {modal?.type === 'form' && (
        <GroupForm group={modal.group} onClose={close} onSaved={(m) => { toast(m); groups.reload() }} />
      )}
      {modal?.type === 'members' && (
        <MembershipDialog
          mode="group"
          entity={modal.group}
          options={(users.data ?? []).map((u) => ({ id: u.id, label: u.username }))}
          onClose={close}
        />
      )}
      {modal?.type === 'delete' && (
        <ConfirmDialog
          title="Delete group"
          confirmLabel="Delete group"
          onClose={close}
          onConfirm={async () => { await api.deleteGroup(modal.group.id); toast('Group deleted.'); groups.reload() }}
        >
          Delete <strong>{modal.group.name}</strong>? Its members lose any database access they only had through this group.
        </ConfirmDialog>
      )}
    </>
  )
}

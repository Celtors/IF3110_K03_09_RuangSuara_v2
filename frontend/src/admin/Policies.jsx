import { useState } from 'react'
import { ACCESS_LEVELS, accessLabel, api } from '../api/endpoints'
import { useResource } from '../api/useResource'
import ConfirmDialog from '../components/ConfirmDialog'
import DataState from '../components/DataState'
import { useToast } from '../components/toast-context'
import { UUID_RE, formatDate } from '../components/format'

const fetchGroups = () => api.listGroups()
const fetchDatabases = () => api.listDatabases()
const fetchPolicies = () => api.listPolicies()

export default function Policies() {
  const toast = useToast()
  const groups = useResource(fetchGroups)
  const databases = useResource(fetchDatabases)
  const policies = useResource(fetchPolicies)

  const [groupId, setGroupId] = useState('')
  const [databaseId, setDatabaseId] = useState('')
  const [level, setLevel] = useState('ViewOnly')
  const [filter, setFilter] = useState('')
  const [formError, setFormError] = useState('')
  const [busy, setBusy] = useState(false)
  const [removing, setRemoving] = useState(null)

  const groupName = (id) => (groups.data ?? []).find((g) => g.id === id)?.name ?? id
  const databaseName = (id) => (databases.data ?? []).find((d) => d.id === id)?.name ?? id
  const rows = (policies.data ?? []).filter((p) => !filter || p.group_id === filter)

  async function add(e) {
    e.preventDefault()
    setFormError('')
    if (!groupId) return setFormError('Choose a group.')
    if (!UUID_RE.test(databaseId.trim())) return setFormError('Enter the database ID as a UUID.')
    setBusy(true)
    try {
      await api.addPolicy(groupId, databaseId.trim().toLowerCase(), level)
      toast('Access granted.')
      setDatabaseId('')
      policies.reload()
    } catch (err) {
      setFormError(err.message)
      policies.reload()
    } finally {
      setBusy(false)
    }
  }

  async function change(p, effect) {
    try {
      await api.updatePolicy(p.group_id, p.database_id, effect)
      toast('Access level changed.')
    } catch (err) {
      toast(err.message, 'error')
    }
    policies.reload()
  }

  return (
    <>
      <header className="page-head">
        <div>
          <h1>Database access</h1>
          <p className="muted">
            Groups get View only, View + edit or Blocked on each database. Users inherit access from their groups, and Blocked in any group overrides the rest.
          </p>
        </div>
      </header>

      <form className="grant" onSubmit={add} noValidate>
        <label className="field">
          <span>Group</span>
          <select className="select" value={groupId} onChange={(e) => setGroupId(e.target.value)}>
            <option value="">Choose a group</option>
            {(groups.data ?? []).map((g) => <option key={g.id} value={g.id}>{g.name}</option>)}
          </select>
        </label>
        <label className="field">
          <span>Group</span>
          <select className="select" value={databaseId} onChange={(e) => setDatabaseId(e.target.value)}>
            <option value="">Choose a Database</option>
            {(databases.data ?? []).map((d) => <option key={d.id} value={d.id}>{d.name}</option>)}
          </select>
        </label>
        <label className="field">
          <span>Access level</span>
          <select className="select" value={level} onChange={(e) => setLevel(e.target.value)}>
            {ACCESS_LEVELS.map((l) => <option key={l.value} value={l.value}>{l.label}</option>)}
          </select>
        </label>
        <button className="btn btn-primary" disabled={busy}>{busy ? 'Granting…' : 'Grant access'}</button>
        {formError && <p className="alert alert-error grant-error" role="alert">{formError}</p>}
      </form>

      <div className="toolbar">
        <select className="select" value={filter} onChange={(e) => setFilter(e.target.value)} aria-label="Filter by group">
          <option value="">All groups</option>
          {(groups.data ?? []).map((g) => <option key={g.id} value={g.id}>{g.name}</option>)}
        </select>
      </div>

      <DataState
        loading={policies.loading}
        error={policies.error}
        empty={rows.length === 0}
        emptyText={filter ? 'This group has no database access yet.' : 'No access granted yet. Use the form above to give a group access to a database.'}
        onRetry={policies.reload}
      >
        <div className="table-wrap">
          <table className="table">
            <thead>
              <tr><th>Group</th><th>Database ID</th><th>Access level</th><th>Granted</th><th><span className="sr-only">Actions</span></th></tr>
            </thead>
            <tbody>
              {rows.map((p) => (
                <tr key={p.id}>
                  <td><strong>{groupName(p.group_id)}</strong></td>
                  <td><strong>{databaseName(p.database_id)}</strong></td>
                  <td>
                    <select className="select select-inline" value={p.effect} onChange={(e) => change(p, e.target.value)} aria-label={`Access level for ${groupName(p.group_id)}`}>
                      {!ACCESS_LEVELS.some((l) => l.value === p.effect) && (
                        <option value={p.effect}>{accessLabel(p.effect)} (unset)</option>
                      )}
                      {ACCESS_LEVELS.map((l) => <option key={l.value} value={l.value}>{l.label}</option>)}
                    </select>
                  </td>
                  <td className="muted">{formatDate(p.created_at)}</td>
                  <td className="row-actions">
                    <button className="btn btn-small btn-danger-quiet" onClick={() => setRemoving(p)}>Revoke</button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </DataState>

      {removing && (
        <ConfirmDialog
          title="Revoke access"
          confirmLabel="Revoke access"
          onClose={() => setRemoving(null)}
          onConfirm={async () => {
            await api.removePolicy(removing.group_id, removing.database_id)
            toast('Access revoked.')
            policies.reload()
          }}
        >
          Revoke <strong>{groupName(removing.group_id)}</strong>’s access to database <span className="mono small">{removing.database_id}</span>?
        </ConfirmDialog>
      )}
    </>
  )
}

import { useCallback, useState } from 'react'
import { api } from '../api/endpoints'
import { useResource } from '../api/useResource'
import Modal from '../components/Modal'

// Choose the leader of a group. The leader is the group's named contact for database changes
// (any member can approve them), so it has to be a person in the group: the list offers the
// members only. Opened by admins with edit access; the server refuses anyone
// else.
export default function GroupLeaderDialog({ group, onClose, onSaved }) {
  const fetchMembers = useCallback(() => api.usersOfGroup(group.id), [group.id])
  const { data, error, loading } = useResource(fetchMembers)
  const [pick, setPick] = useState(null) // null until the admin chooses; then the user id
  const [busy, setBusy] = useState(false)
  const [actionError, setActionError] = useState('')

  const members = (data ?? []).map((m) => ({ id: m.user_id, name: m.username || m.user_id, status: m.status }))
  // The list only names the leader, and usernames are unique, so that is how to find them here.
  const current = members.find((m) => m.name === group.leader_name)
  const selected = pick ?? current?.id ?? ''
  const unchanged = Boolean(current) && selected === current.id

  async function save(e) {
    e.preventDefault()
    if (!selected || busy) return
    setBusy(true)
    setActionError('')
    try {
      await api.setGroupLeader(selected, group.id)
      const name = members.find((m) => m.id === selected)?.name ?? 'The user'
      onSaved(`${name} is now the leader of ${group.name}.`)
      onClose()
    } catch (err) {
      setActionError(err.message)
      setBusy(false)
    }
  }

  return (
    <Modal title={`Leader of ${group.name}`} onClose={onClose}>
      <form className="stack" onSubmit={save} noValidate>
        {(error || actionError) && <p className="alert alert-error" role="alert">{actionError || error.message}</p>}

        <p className="muted">
          {group.leader_name ? (
            <>Current leader: <strong>{group.leader_name}</strong>.</>
          ) : (
            'This group has no leader yet. Its members can still approve changes to databases it can access.'
          )}{' '}
          The leader is shown as the group's contact on change requests.
        </p>
        {group.leader_name && !loading && !error && !current && (
          <p className="alert alert-error" role="alert">
            {group.leader_name} is no longer a member of this group. Choose a current member.
          </p>
        )}

        {loading && !data ? (
          <p className="muted">Loading members…</p>
        ) : members.length === 0 && !error ? (
          <p className="empty">This group has no members. Add members first, then choose one as the leader.</p>
        ) : (
          <label className="field">
            <span>Leader</span>
            <select className="select" value={selected} onChange={(e) => setPick(e.target.value)} disabled={busy}>
              <option value="" disabled>Choose a member</option>
              {members.map((m) => (
                <option key={m.id} value={m.id}>{m.status && m.status !== 'Active' ? `${m.name} (${m.status})` : m.name}</option>
              ))}
            </select>
          </label>
        )}

        <div className="actions">
          <button type="button" className="btn" onClick={onClose} disabled={busy}>Cancel</button>
          <button className="btn btn-primary" disabled={!selected || unchanged || busy}>
            {busy ? 'Saving…' : group.leader_name ? 'Change leader' : 'Set leader'}
          </button>
        </div>
      </form>
    </Modal>
  )
}

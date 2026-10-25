import { useCallback, useState } from 'react'
import { api } from '../api/endpoints'
import { useResource } from '../api/useResource'
import Modal from '../components/Modal'
import { useToast } from '../components/toast-context'
import { formatDate } from '../components/format'

// mode "group": manage the users in a group. mode "user": manage the groups of a user.
export default function MembershipDialog({ mode, entity, options, onClose }) {
  const toast = useToast()
  const isGroup = mode === 'group'
  const fetcher = useCallback(
    () => (isGroup ? api.usersOfGroup(entity.id) : api.groupsOfUser(entity.id)),
    [isGroup, entity.id],
  )
  const { data, error, loading, reload } = useResource(fetcher)
  const [pick, setPick] = useState('')
  const [busy, setBusy] = useState(false)
  const [actionError, setActionError] = useState('')

  const members = (data ?? []).map((m) =>
    isGroup
      ? { id: m.user_id, label: m.username || m.user_id, sub: m.status, at: m.placed_at }
      : { id: m.group_id, label: m.name || m.group_id, sub: m.description, at: m.placed_at },
  )
  const memberIds = new Set(members.map((m) => m.id))
  const available = options.filter((o) => !memberIds.has(o.id))

  async function act(fn, message) {
    setBusy(true)
    setActionError('')
    try {
      await fn()
      toast(message)
      setPick('')
      reload()
    } catch (e) {
      setActionError(e.message)
    } finally {
      setBusy(false)
    }
  }

  const add = () =>
    act(
      () => (isGroup ? api.addMember(pick, entity.id) : api.addMember(entity.id, pick)),
      isGroup ? 'User added to group.' : 'Group added to user.',
    )
  const remove = (id) =>
    act(
      () => (isGroup ? api.removeMember(id, entity.id) : api.removeMember(entity.id, id)),
      isGroup ? 'User removed from group.' : 'Group removed from user.',
    )

  return (
    <Modal title={isGroup ? `Members of ${entity.name}` : `Groups of ${entity.username}`} onClose={onClose} wide>
      <div className="stack">
        {(error || actionError) && (
          <p className="alert alert-error" role="alert">{actionError || error.message}</p>
        )}

        <div className="input-row">
          <select className="select" value={pick} onChange={(e) => setPick(e.target.value)} aria-label={isGroup ? 'User to add' : 'Group to add'}>
            <option value="">{isGroup ? 'Choose a user to add' : 'Choose a group to add'}</option>
            {available.map((o) => (
              <option key={o.id} value={o.id}>{o.label}</option>
            ))}
          </select>
          <button type="button" className="btn btn-primary" onClick={add} disabled={!pick || busy}>
            Add
          </button>
        </div>

        {loading && !data ? (
          <p className="muted">Loading…</p>
        ) : members.length === 0 ? (
          <p className="empty">{isGroup ? 'No users in this group yet.' : 'This user is not in any group yet.'}</p>
        ) : (
          <ul className="member-list">
            {members.map((m) => (
              <li key={m.id}>
                <div>
                  <strong>{m.label}</strong>
                  <span className="muted small">
                    {m.sub ? `${m.sub} · ` : ''}added {formatDate(m.at)}
                  </span>
                </div>
                <button type="button" className="btn btn-small" onClick={() => remove(m.id)} disabled={busy}>
                  Remove
                </button>
              </li>
            ))}
          </ul>
        )}
      </div>
    </Modal>
  )
}

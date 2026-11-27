import { useRef, useState } from 'react'
import Modal from './Modal'

export default function ConfirmDialog({ title, children, confirmLabel, onConfirm, onClose }) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const inFlight = useRef(false)

  async function run() {
    if (inFlight.current) return
    inFlight.current = true
    setBusy(true)
    setError('')
    try {
      await onConfirm()
      onClose()
    } catch (e) {
      setError(e.message)
      inFlight.current = false
      setBusy(false)
    }
  }

  return (
    <Modal title={title} onClose={onClose} busy={busy}>
      <div className="stack">
        <p>{children}</p>
        {error && <p className="alert alert-error" role="alert">{error}</p>}
        <div className="actions">
          <button type="button" className="btn" onClick={onClose} disabled={busy}>Cancel</button>
          <button type="button" className="btn btn-danger" onClick={run} disabled={busy}>
            {busy ? 'Working…' : confirmLabel}
          </button>
        </div>
      </div>
    </Modal>
  )
}

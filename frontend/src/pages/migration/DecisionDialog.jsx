import { useState } from "react";
import Modal from "../../components/Modal.jsx";

const COPY = {
  approve: {
    title: "Approve this change?",
    body: "Your approval is recorded for every group you lead. Once every required leader has approved, the change joins the queue and runs automatically.",
    label: "Comment (optional)",
    confirm: "Approve",
    primary: "btn-primary",
  },
  reject: {
    title: "Reject this change",
    body: "Rejecting stops the request for everyone. The requester must submit a new one. Tell them what to fix.",
    label: "Reason (required)",
    confirm: "Reject request",
    primary: "btn-danger",
    required: true,
  },
  cancel: {
    title: "Cancel this request?",
    body: "The request is withdrawn and will not run. You can submit a new one later.",
    label: "Comment (optional)",
    confirm: "Cancel request",
    primary: "btn-danger",
  },
};

// One dialog for approve, reject and cancel. `onSubmit(comment)` does the call; a thrown
// error is shown here so the person can retry or fix the comment.
export function DecisionDialog({ kind, onSubmit, onClose }) {
  const copy = COPY[kind];
  const [comment, setComment] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const missing = copy.required && !comment.trim();
  async function run(event) {
    event.preventDefault();
    if (busy || missing) return;
    setBusy(true);
    setError("");
    try {
      await onSubmit(comment.trim());
      onClose();
    } catch (e) {
      setError(e.message);
      setBusy(false);
    }
  }
  return (
    <Modal title={copy.title} onClose={onClose} busy={busy}>
      <form onSubmit={run} className="stack">
        <p>{copy.body}</p>
        <label htmlFor="decision-comment">{copy.label}</label>
        <textarea
          className="input"
          id="decision-comment"
          value={comment}
          onChange={(e) => setComment(e.target.value)}
          maxLength={2000}
          rows={3}
          required={Boolean(copy.required)}
          autoFocus
        />
        {error && (
          <div className="alert alert-error notice error" role="alert">
            {error}
          </div>
        )}
        <div className="actions">
          <button type="button" className="btn" disabled={busy} onClick={onClose}>
            Back
          </button>
          <button type="submit" className={`btn ${copy.primary}`} disabled={busy || missing}>
            {busy ? "Working…" : copy.confirm}
          </button>
        </div>
      </form>
    </Modal>
  );
}

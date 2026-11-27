import { useState } from "react";
import Modal from "../../components/Modal.jsx";
import { requestHref } from "../../migration/api.js";
import { OPERATION_LABEL, submitChangeRequest } from "../../migration/changeRequests.js";
import { SQL } from "../../migration/components.jsx";
import { navigate } from "../../migration/router.js";

const COPY = {
  apply: {
    title: (name) => `Submit “${name}” for approval`,
    lead: "The SQL below will be applied to",
    confirm: "Submit for approval",
    field: "sql_script",
  },
  rollback: {
    title: (name) => `Request a rollback of “${name}”`,
    lead: "The SQL down below will be run on",
    confirm: "Request rollback",
    field: "sql_down",
  },
  forward: {
    title: (name) => `Request roll forward of “${name}”`,
    lead: "The SQL below will be re-applied to",
    confirm: "Request roll forward",
    field: "sql_script",
  },
};

// Asks to run an operation. Nothing runs yet: every group with access to the
// database must approve first, then the request waits its turn in the database's queue.
export function SubmitRequestDialog({ databaseId, migration, operation, onClose, onSubmitted }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const copy = COPY[operation];
  async function submit() {
    setBusy(true);
    setError("");
    try {
      const request = await submitChangeRequest(databaseId, migration.id, operation);
      if (onSubmitted) onSubmitted(request);
      else navigate(requestHref(databaseId, request.id));
    } catch (e) {
      setError(e.message);
      setBusy(false);
    }
  }
  return (
    <Modal title={copy.title(migration.name)} onClose={onClose} wide>
      <p>
        {copy.lead} database <strong>{databaseId}</strong>. {OPERATION_LABEL[operation]} requests do not run immediately.
      </p>
      <SQL value={migration[copy.field] ?? ""} />
      <ol className="cr-explain">
        <li>Every group with access to this database reviews and approves; only the group’s leader can decide for it.</li>
        <li>After the last approval the request joins the queue for this database.</li>
        <li>Requests on one database run one at a time, in order, without anyone clicking execute.</li>
      </ol>
      {error && (
        <div className="alert alert-error notice error" role="alert">
          {error}
        </div>
      )}
      <div className="actions">
        <button type="button" className="btn" disabled={busy} onClick={onClose}>
          Cancel
        </button>
        <button type="button" className="btn btn-primary" disabled={busy} onClick={submit}>
          {busy ? "Submitting…" : copy.confirm}
        </button>
      </div>
    </Modal>
  );
}

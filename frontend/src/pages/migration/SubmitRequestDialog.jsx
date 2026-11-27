import { useRef, useState } from "react";
import Modal from "../../components/Modal.jsx";
import { requestHref } from "../../migration/api.js";
import { OPERATION_LABEL, submitChangeRequest } from "../../migration/changeRequests.js";
import { SQL } from "../../migration/components.jsx";
import { batchErrors, possibleBlockers } from "../../migration/jobs.js";
import { BlockingJobs } from "./BlockingJobs.jsx";
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
export function SubmitRequestDialog({ databaseId, migration, operation, correctsBatchId, onClose, onSubmitted }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [mode, setMode] = useState("normal");
  const [attribute, setAttribute] = useState("id");
  const [size, setSize] = useState("1000");
  const [attempted, setAttempted] = useState(false);
  const [batchError, setBatchError] = useState("");
  const [blockers, setBlockers] = useState([]);
  const inFlight = useRef(false);
  const batch = operation === "apply" && mode === "batch";
  const invalid = batchErrors(attribute, size);
  const copy = COPY[operation];
  async function submit() {
    if (inFlight.current) return;
    setAttempted(true);
    setError("");
    setBatchError("");
    setBlockers([]);
    if (batch && (invalid.attribute || invalid.size)) return;
    inFlight.current = true;
    setBusy(true);
    try {
      const request = await submitChangeRequest(databaseId, migration.id, operation, batch
        ? { mode: "batch", batch_attribute: attribute.trim(), batch_size: Number(size) }
        : { mode: "normal", ...(correctsBatchId ? { corrects_batch_id: correctsBatchId } : {}) });
      if (onSubmitted) onSubmitted(request);
      else navigate(requestHref(databaseId, request.job_id));
    } catch (e) {
      if (batch && e.code === "invalid_input") setBatchError(e.message);
      else setError(e.message);
      if (e.code === "conflict" || e.code === "indeterminate") {
        try { setBlockers(await possibleBlockers(databaseId)); } catch { /* original error remains visible */ }
      }
      inFlight.current = false;
      setBusy(false);
    }
  }
  return (
    <Modal title={copy.title(migration.name)} onClose={onClose} wide busy={busy}>
      <p>
        {copy.lead} database <strong>{databaseId}</strong>. {OPERATION_LABEL[operation]} requests do not run immediately.
      </p>
      {correctsBatchId && <p>This correction runs as one transaction for the linked partial batch. Other jobs must wait for verified success.</p>}
      {operation === "apply" && !correctsBatchId && (
        <div className="stack">
          <label htmlFor="execution-mode">Execution mode</label>
          <select id="execution-mode" className="input" value={mode} disabled={busy} onChange={(e) => { setMode(e.target.value); setBatchError(""); }}>
            <option value="normal">Normal</option>
            <option value="batch">Batch</option>
          </select>
          {batch && <>
            <p>Batch mode requires a single integer-column UPDATE with an explicit WHERE cursor = ANY($1::bigint[]) predicate. The cursor must be a single integer GENERATED ALWAYS identity primary key.</p>
            <label htmlFor="batch-attribute">Batch attribute</label>
            <input id="batch-attribute" className="input" value={attribute} disabled={busy} aria-invalid={Boolean((attempted && invalid.attribute) || batchError)} aria-describedby="batch-attribute-error batch-server-error" onChange={(e) => { setAttribute(e.target.value); setBatchError(""); }} />
            <span id="batch-attribute-error" role={attempted && invalid.attribute ? "alert" : undefined}>{attempted && invalid.attribute}</span>
            <label htmlFor="batch-size">Batch size</label>
            <input id="batch-size" className="input" type="number" min="1" max="10000" step="1" value={size} disabled={busy} aria-invalid={Boolean((attempted && invalid.size) || batchError)} aria-describedby="batch-size-error batch-server-error" onChange={(e) => { setSize(e.target.value); setBatchError(""); }} />
            <span id="batch-size-error" role={attempted && invalid.size ? "alert" : undefined}>{attempted && invalid.size}</span>
            <div id="batch-server-error">{batchError && <p className="alert notice error" role="alert">{batchError}</p>}</div>
          </>}
        </div>
      )}
      <SQL value={migration[copy.field] ?? ""} />
      {batch && migration.sql_down?.trim() && <details><summary>SQL down (for eligible compensation)</summary><SQL value={migration.sql_down} /></details>}
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
      <BlockingJobs databaseId={databaseId} jobs={blockers} />
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

import { useEffect, useState } from "react";
import ConfirmDialog from "../../components/ConfirmDialog.jsx";
import { correctiveDraftHref, date, requestHref } from "../../migration/api.js";
import { OPERATION_LABEL } from "../../migration/changeRequests.js";
import { isJobActive, jobStatus, possibleBlockers, recoverJob, recoveryActions } from "../../migration/jobs.js";
import { ErrorBox } from "../../migration/components.jsx";
import { RequestBadge } from "./ChangeRequestParts.jsx";
import { BlockingJobs } from "./BlockingJobs.jsx";

const number = (value) => value == null ? "—" : value.toLocaleString();

export function JobProgress({ databaseId, jobId, resource, canEdit, isRequester, onRefresh }) {
  const { job, error, loading, reload } = resource;
  const status = jobStatus(job);
  const active = isJobActive(job);
  const [dialog, setDialog] = useState("");
  const [submittedJob, setSubmittedJob] = useState(null);
  const [blocked, setBlocked] = useState({ job: null, items: [] });
  useEffect(() => {
    if (status !== "queued" || !job) return;
    let current = true;
    possibleBlockers(databaseId, jobId).then((items) => {
      if (current) setBlocked({ job, items });
    }).catch(() => { if (current) setBlocked({ job, items: [] }); });
    return () => { current = false; };
  }, [databaseId, jobId, status, job]);
  const refresh = () => { reload(); onRefresh?.(); };
  const actions = error || submittedJob === job ? [] : recoveryActions(job, canEdit, isRequester);
  const compensation = job?.compensation_status;
  const continuing = compensation === "partially_reverted" || compensation === "failed";
  const compensate = dialog === "compensate";
  async function recover() {
    if (!actions.includes(dialog)) throw new Error("This recovery action is no longer available. Refresh the job to check its status.");
    await recoverJob(databaseId, jobId, dialog);
    setSubmittedJob(job);
    refresh();
  }
  const held = status === "unresolved" || (job?.paused && ["partial", "failed"].includes(status));
  const correctionNeeded = job?.corrective_available || job?.open_correction_id;
  const hasScope = job?.scope_rows > 0 || ["completed", "reverted"].includes(status);
  const checkpoint = job?.last_verified_checkpoint;
  return (
    <section className="panel" aria-label="Job status">
      <div className="panel-heading">
        <div><h2>Execution job</h2><p className="job-id">Job ID: <span className="mono">{jobId}</span></p></div>
        <div className="actions">
          {job && <RequestBadge status={status} />}
          {active && <span className="cr-live">Updating automatically</span>}
          <button className="btn" onClick={refresh} disabled={loading}>Refresh status</button>
        </div>
      </div>
      <ErrorBox error={error} />
      {!job ? (loading ? <p role="status">Loading job status…</p> : <p className="empty">Job status is not available. Try refreshing.</p>) : <>
        {status === "queued" && <p className="alert notice">Waiting in this database’s queue. It can be held by a running or unresolved migration.</p>}
        {status === "queued" && blocked.job === job && <BlockingJobs databaseId={databaseId} jobs={blocked.items} />}
        {status === "partial" && <p className="alert notice warning">Some batches have committed. {job.paused ? "Execution is paused; earlier commits are preserved." : "Execution is still processing batches."}</p>}
        {status === "unresolved" && <p className="alert notice warning">The execution outcome is uncertain. Do not retry blindly; verified recovery or reconciliation is required. Refresh status to check for reconciliation.</p>}
        {held && <p className="alert notice warning">Later migrations on this database are held until the batch is recovered or its approved correction succeeds.</p>}
        {status === "partial" && active && <p>Later migrations on this database wait until this operation finishes or is recovered.</p>}
        {job.error && status !== "corrected" && <div className={`alert notice ${["completed", "reverted"].includes(status) ? "" : "error"}`} role="alert">{job.error_code && !["completed", "reverted"].includes(status) && <strong>{job.error_code}: </strong>}{job.error}</div>}
        <dl className="details-grid">
          {[
            ["Target database", job.database_id], ["Migration", job.migration_id],
            ["Operation", OPERATION_LABEL[job.operation] ?? job.operation], ["Mode", job.mode === "batch" ? "Batch" : "Normal"],
            ["Committed batches", number(job.committed_batches)], ["Committed rows", number(job.committed_rows)],
            ...(job.mode === "batch" ? [["Batch attribute", job.batch_attribute], ["Batch size", number(job.batch_size)], ["Target scope (rows)", hasScope ? number(job.scope_rows) : "—"], ["Last verified batch", number(checkpoint?.batch_number)], ["Last verified cursor", number(checkpoint?.cursor ?? job.checkpoint)]] : []),
            ["Created", date(job.created_at)], ["Updated", date(job.updated_at)], ["Started", date(job.started_at)], ["Completed", date(job.completed_at)],
          ].map(([label, value]) => <div key={label}><dt>{label}</dt><dd>{value ?? "—"}</dd></div>)}
        </dl>
        {job.mode === "batch" && job.scope_rows > 0 && job.committed_rows != null && <div className="stack">
          <label htmlFor="job-progress">Committed rows: {number(job.committed_rows)} of {number(job.scope_rows)}</label>
          <progress id="job-progress" className="job-progress" max={job.scope_rows} value={job.committed_rows} />
        </div>}
        {compensation && <div className="stack">
          <h3>Compensation <RequestBadge status={compensation} /></h3>
          <dl className="details-grid">
            <div><dt>Reverted batches</dt><dd>{number(job.reverted_batches)}</dd></div>
            <div><dt>Reverted rows</dt><dd>{number(job.reverted_rows)}</dd></div>
            <div><dt>Remaining committed rows</dt><dd>{number(job.remaining_committed_rows)}</dd></div>
          </dl>
          {continuing && <p>Compensation stopped. The verified compensation checkpoint is preserved.</p>}
        </div>}
        {correctionNeeded && <p>Compensation is unsafe or unavailable. Only an approved corrective migration linked to this batch can proceed while the reservation is held.</p>}
        {job.corrective_available && canEdit && <p><a className="btn" href={correctiveDraftHref(databaseId, jobId)}>Create corrective migration</a></p>}
        {job.open_correction_id && <p><a className="cr-link" href={requestHref(databaseId, job.open_correction_id)}>View corrective request</a></p>}
        {job.resolved_by && <p>Resolved by <a className="cr-link" href={requestHref(databaseId, job.resolved_by)}>the corrective migration</a>. The original checkpoint is retained.</p>}
        {submittedJob === job && <p role="status">Recovery queued. Refreshing verified status…</p>}
        {actions.length > 0 && <div className="actions">
          {actions.includes("resume") && <button className="btn btn-primary" onClick={() => setDialog("resume")}>{continuing ? "Continue compensation" : "Resume"}</button>}
          {actions.includes("compensate") && !continuing && <button className="btn btn-danger-quiet" onClick={() => setDialog("compensate")}>Compensate (revert committed batches)</button>}
        </div>}
      </>}
      {dialog && <ConfirmDialog title={compensate ? "Compensate committed batches?" : continuing ? "Continue compensation?" : "Resume migration?"} confirmLabel={compensate ? "Compensate" : continuing ? "Continue compensation" : "Resume"} onClose={() => setDialog("")} onConfirm={recover}>
        {compensate ? "This runs the approved down SQL against recorded committed batches in reverse order, one transaction per batch. It is not a total transaction rollback. The server revalidates your original approval and permissions." : continuing ? "Continue reverting committed batches in reverse order from the last verified compensation checkpoint. The server revalidates your original approval and permissions." : "Continue from the last verified checkpoint, processing only uncommitted batches. The server revalidates your original approval and permissions."}
      </ConfirmDialog>}
    </section>
  );
}

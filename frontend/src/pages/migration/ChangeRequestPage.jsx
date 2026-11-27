import { useState } from "react";
import { date, href, viewerId } from "../../migration/api.js";
import {
  OPERATION_LABEL,
  approveChangeRequest,
  approvalProgress,
  cancelChangeRequest,
  rejectChangeRequest,
  shortId,
  sqlFor,
  viewerRole,
} from "../../migration/changeRequests.js";
import { ErrorBox, SQL } from "../../migration/components.jsx";
import {
  useChangeRequest,
  useMigrationDefinition,
  useSqlIntegrity,
} from "../../migration/useChangeRequests.js";
import { ActivityLog, ApprovalsTable, RequestBadge, StatusTrack, Who } from "./ChangeRequestParts.jsx";
import { namesFrom } from "./NamesFrom.jsx";
import { useJob } from "../../migration/useJob.js";
import { JobProgress } from "./JobProgress.jsx";
import { DecisionDialog } from "./DecisionDialog.jsx";

// What the request is doing now, in a sentence.
function Outcome({ request }) {
  const { status, queue_position: position, result_message: message, result_code: code, reject_reason: reason } = request;
  if (status === "pending_review") {
    const waiting = request.approvals.filter((a) => a.decision === "pending");
    return (
      <div className="alert notice">
        Waiting for approval from{" "}
        <strong>{waiting.map((a) => a.group_name || a.group_id).join(", ")}</strong>.
      </div>
    );
  }
  if (status === "approved") return <div className="alert notice">Approved by every group. Joining the queue…</div>;
  if (status === "queued")
    return (
      <div className="alert notice">
        {position === 1
          ? "Next in line for this database. It starts as soon as nothing else is running."
          : `Position ${position ?? "—"} in this database’s queue. Requests run one at a time, in order.`}
      </div>
    );
  if (status === "rejected")
    return (
      <div className="alert notice error" role="alert">
        Rejected{reason ? `: ${reason}` : "."}
      </div>
    );
  if (status === "cancelled")
    return (
      <div className="alert notice">
        Cancelled{reason || message ? `: ${reason || message}` : "."}
        {code && <small> ({code})</small>}
      </div>
    );
  return null;
}

function Integrity({ value }) {
  if (value === true)
    return (
      <div className="alert notice success" role="status">
        This SQL matches exactly what was submitted for approval.
      </div>
    );
  if (value === false)
    return (
      <div className="alert notice error" role="alert">
        This SQL has changed since the request was submitted, so it cannot be approved. Cancel the request and submit a
        new one.
      </div>
    );
  return null;
}

// One change request: progress, who must approve, the SQL under review, and the actions the
// signed-in user may take. Refreshes itself while the request can still change.
export function ChangeRequestPage({ databaseId, requestId, canEdit, onDecision }) {
  const jobResource = useJob(databaseId, requestId);
  const { job } = jobResource;
  // Each pending job read also refreshes the reviewers. After review, a job
  // status change is enough to refresh the request and its audit trail.
  const revision = job?.approval_status === "pending_review"
    ? job
    : `${job?.approval_status ?? ""}|${job?.status ?? ""}|${job?.compensation_status ?? ""}`;
  const { request, error, loading, reload, replace } = useChangeRequest(databaseId, requestId, {
    poll: !job || Boolean(jobResource.error),
    revision,
  });
  const definition = useMigrationDefinition(databaseId, request?.migration_id);
  const sql = sqlFor(definition.migration, request);
  const integrity = useSqlIntegrity(sql, request?.sql_hash);
  const downSql = definition.migration?.sql_down;
  const downIntegrity = useSqlIntegrity(downSql, request?.compensation_sql_hash);
  const [dialog, setDialog] = useState(null);
  const me = viewerId();
  const names = namesFrom(request ? [request] : []);

  if (!requestId) return <ErrorBox error="No change request selected. Open one from the migration history." />;
  if (!request)
    return (
      <>
        {loading ? <p role="status">Loading change request…</p> : <>
          <ErrorBox error={error || "Change request not found."} />
          <button className="btn btn-primary" onClick={reload}>Try again</button>
        </>}
        <JobProgress databaseId={databaseId} jobId={requestId} resource={jobResource} canEdit={false} isRequester={false} onRefresh={reload} />
      </>
    );

  const role = viewerRole(request, me);
  const { approved, total } = approvalProgress(request);
  const canDecide = !error && role.canDecide;
  const canCancel = !error && role.canCancel;
  const canApprove = canDecide && sql && integrity !== false
    && (!request.compensation_sql_hash || (downSql && downIntegrity !== false));
  const trackReview = ["pending_review", "approved", "rejected", "cancelled"].includes(request.status);

  async function submitDecision(comment) {
    const allowed = { approve: canApprove, reject: canDecide, cancel: canCancel };
    if (!allowed[dialog]) throw new Error("This action is no longer available. Refresh the request to check its status.");
    const decide = { approve: approveChangeRequest, reject: rejectChangeRequest, cancel: cancelChangeRequest }[dialog];
    replace(await decide(databaseId, request.id, comment));
    jobResource.reload();
    onDecision?.();
  }

  return (
    <>
      <ErrorBox error={error} />
      <section className="panel">
        <div className="panel-heading">
          <div>
            <h2>
              {OPERATION_LABEL[request.operation] ?? request.operation} “{request.migration_name}”
            </h2>
            <p>
              Submitted by <Who id={request.requested_by} me={me} names={names} /> · {date(request.created_at)} ·{" "}
              <a className="cr-link" href={href("details", databaseId, request.migration_id)}>
                View migration →
              </a>
            </p>
          </div>
          <div className="actions">
            <span>Request status: <RequestBadge status={request.status} /></span>
            {canCancel && (
              <button className="btn btn-danger-quiet" onClick={() => setDialog("cancel")}>
                Cancel request
              </button>
            )}
          </div>
        </div>
        {trackReview && <StatusTrack request={request} />}
        <Outcome request={request} />
        <dl className="details-grid">
          {[
            ["Database", request.database_id],
            ["Operation", OPERATION_LABEL[request.operation] ?? request.operation],
            ["Approved", date(request.approved_at)],
            ["Queued", date(request.queued_at)],
            ["Started", date(request.started_at)],
            ["Completed", date(request.completed_at)],
            ["SQL fingerprint", <span className="mono" title={request.sql_hash} key="h">{shortId(request.sql_hash)}</span>],
            [
              "Execution attempt",
              request.attempt_id ? (
                <a className="cr-link" href={href("details", databaseId, request.migration_id)} key="a">
                  {request.attempt_id}
                </a>
              ) : (
                "—"
              ),
            ],
          ].map(([k, v]) => (
            <div key={k}>
              <dt>{k}</dt>
              <dd>{v}</dd>
            </div>
          ))}
        </dl>
      </section>

      <JobProgress databaseId={databaseId} jobId={requestId} resource={jobResource} canEdit={canEdit} isRequester={role.isRequester} onRefresh={reload} />

      <section className="panel">
        <div className="panel-heading">
          <h2>Required approvals</h2>
          <span className="mono">
            {approved} of {total}
          </span>
        </div>
        <p className="cr-lead">
          Every group with access to this database must approve before the change can run. Only a group’s leader can decide for it.
        </p>
        <ApprovalsTable approvals={request.approvals} requestStatus={error ? undefined : request.status} />
        {canDecide && (
          <div className="cr-decision">
            <p>
              <strong>Your decision is needed</strong> for{" "}
              {role.pending.map((a) => a.group_name || a.group_id).join(", ")}
              . Read the SQL below first.
            </p>
            <div className="actions">
              <button className="btn btn-danger-quiet" onClick={() => setDialog("reject")}>
                Reject…
              </button>
              <button className="btn btn-primary" disabled={!canApprove} onClick={() => setDialog("approve")}>
                Approve…
              </button>
            </div>
          </div>
        )}
      </section>

      <section className="panel">
        <div className="panel-heading">
          <h2>{request.operation === "rollback" ? "SQL down to run" : "SQL to run"}</h2>
          <span className="mono">PostgreSQL</span>
        </div>
        {request.batch && <p>Batch execution · attribute: <strong>{request.batch.batch_attribute}</strong> · size: <strong>{request.batch.batch_size}</strong></p>}
        <Integrity value={integrity} />
        {definition.error ? (
          <ErrorBox error={definition.error} />
        ) : definition.loading ? (
          <p role="status">Loading SQL…</p>
        ) : (
          <SQL value={sql} />
        )}
        {request.compensation_sql_hash && definition.migration && <>
          <h3>Approved compensation SQL down</h3>
          <Integrity value={downIntegrity} />
          <SQL value={downSql} />
        </>}
      </section>

      <section className="panel">
        <div className="panel-heading">
          <h2>Activity</h2>
        </div>
        <ActivityLog events={request.events} me={me} names={names} />
      </section>

      {dialog && (
        <DecisionDialog
          kind={dialog}
          onClose={() => setDialog(null)}
          onSubmit={submitDecision}
        />
      )}
    </>
  );
}

import { useState } from "react";
import { date, href, viewerId } from "../../migration/api.js";
import {
  OPERATION_LABEL,
  approveChangeRequest,
  cancelChangeRequest,
  rejectChangeRequest,
  shortId,
  sqlFor,
  viewerRole,
} from "../../migration/changeRequests.js";
import { ErrorBox, SQL } from "../../migration/components.jsx";
import { requestId } from "../../migration/navigation.js";
import {
  useChangeRequest,
  useMigrationDefinition,
  useSqlIntegrity,
} from "../../migration/useChangeRequests.js";
import { ActivityLog, ApprovalsTable, RequestBadge, StatusTrack, Who} from "./ChangeRequestParts.jsx";
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
        Waiting for the leader of{" "}
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
  if (status === "running") return <div className="alert notice warning">Running on the target database now.</div>;
  if (status === "succeeded") return <div className="alert notice success">The change was applied successfully.</div>;
  if (status === "failed")
    return (
      <div className="alert notice error" role="alert">
        The change failed{message ? `: ${message}` : "."}
        {code && <small> ({code})</small>}
      </div>
    );
  if (status === "unknown")
    return (
      <div className="alert notice warning" role="alert">
        The outcome could not be confirmed on the target database. It is being checked automatically; if it stays
        unresolved, use “Reconcile outcomes” on the Migrations tab.
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
export function ChangeRequestPage({ databaseId, canEdit }) {
  const job = useJob(databaseId, requestId);
  const { request, error, loading, reload, replace } = useChangeRequest(databaseId, requestId, {
    poll: job.job?.approval_status === "pending_review",
    revision: `${job.job?.approval_status ?? ""}|${job.job?.status ?? ""}|${job.job?.compensation_status ?? ""}`,
  });
  const definition = useMigrationDefinition(databaseId, request?.migration_id);
  const sql = sqlFor(definition.migration, request);
  const integrity = useSqlIntegrity(sql, request?.sql_hash);
  const downIntegrity = useSqlIntegrity(definition.migration?.sql_down, request?.compensation_sql_hash);
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
        <JobProgress databaseId={databaseId} jobId={requestId} resource={job} canEdit={false} isRequester={false} onRefresh={reload} />
      </>
    );

  const role = viewerRole(request, me);
  const act = (fn) => async (comment) => {
    replace(await fn(databaseId, request.id, comment));
    job.reload();
  };
  const mismatched = integrity === false || downIntegrity === false;

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
            <span>Approval workflow: <RequestBadge status={request.status} /></span>
            {role.canCancel && (
              <button className="btn btn-danger-quiet" onClick={() => setDialog("cancel")}>
                Cancel request
              </button>
            )}
          </div>
        </div>
        {["pending_review", "approved", "rejected", "cancelled"].includes(request.status) && <>
          <StatusTrack request={request} />
          <Outcome request={request} />
        </>}
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
                  View attempts →
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

      <JobProgress databaseId={databaseId} jobId={requestId} resource={job} canEdit={canEdit} isRequester={role.isRequester} onRefresh={reload} />

      <section className="panel">
        <div className="panel-heading">
          <h2>Required approvals</h2>
          <span className="mono">
            {request.approvals.filter((a) => a.decision === "approved").length} of {request.approvals.length}
          </span>
        </div>
        <p className="cr-lead">
          Every group with access to this database must approve before the change can run. Only a group’s leader can decide for it.
        </p>
        <ApprovalsTable approvals={request.approvals}/>
        {role.canDecide && (
          <div className="cr-decision">
            <p>
              <strong>Your decision is needed</strong> for{" "}
              {role.mine
                .filter((a) => a.decision === "pending")
                .map((a) => a.group_name || a.group_id)
                .join(", ")}
              . Read the SQL below first.
            </p>
            <div className="actions">
              <button className="btn btn-danger-quiet" onClick={() => setDialog("reject")}>
                Reject…
              </button>
              <button className="btn btn-primary" disabled={mismatched} onClick={() => setDialog("approve")}>
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
          <SQL value={definition.migration.sql_down} />
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
          onSubmit={act({ approve: approveChangeRequest, reject: rejectChangeRequest, cancel: cancelChangeRequest }[dialog])}
        />
      )}
    </>
  );
}

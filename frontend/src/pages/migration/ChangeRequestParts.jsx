import { date } from "../../migration/api.js";
import { STATUS, STATUS_LABEL, shortId, trackFor } from "../../migration/changeRequests.js";

export function RequestBadge({ status }) {
  return <span className={`badge ${STATUS[status]?.badge ?? "badge-off"}`}>{STATUS_LABEL[status] ?? status}</span>;
}

export function DecisionBadge({ decision }) {
  const label = { pending: "Waiting", approved: "Approved", rejected: "Rejected" }[decision] ?? decision;
  return <span className={`badge ${decision}`}>{label}</span>;
}

export function Who({ id, me, names }) {
  if (!id) return <span>System</span>;
  if (id === me) return <strong>You</strong>;
  if (names?.[id]) return <span title={id}>{names[id]}</span>;
  return (
    <span className="mono" title={id}>
      {shortId(id)}
    </span>
  );
}

// Draft -> Pending review -> Approved -> In queue -> Running -> Success / Failed.
export function StatusTrack({ request }) {
  const steps = trackFor(request);
  return (
    <ol className="cr-track" aria-label="Change request progress">
      {steps.map((step) => (
        <li
          key={step.label}
          className={`cr-step ${step.state}`}
          aria-current={step.state === "current" ? "step" : undefined}
        >
          <span>{step.label}</span>
          <small className="sr-only">
            {{ done: "done", current: "current", bad: "stopped here", todo: "not reached" }[step.state]}
          </small>
        </li>
      ))}
    </ol>
  );
}

// "1 of 2 groups approved", as a compact meter for lists.
export function ApprovalMeter({ request }) {
  const approvals = request.approvals ?? [];
  const approved = approvals.filter((a) => a.decision === "approved").length;
  const rejected = approvals.some((a) => a.decision === "rejected");
  if (!approvals.length) return <span>—</span>;
  return (
    <span className={rejected ? "cr-meter bad" : "cr-meter"} title={`${approved} of ${approvals.length} groups approved`}>
      <span className="cr-meter-bar" aria-hidden="true">
        {approvals.map((a) => (
          <i key={a.id} className={a.decision} />
        ))}
      </span>
      {approved}/{approvals.length}
    </span>
  );
}

export function ApprovalsTable({ approvals, me }) {
  return (
    <div className="table-scroll" role="region" aria-label="Required approvals" tabIndex={0}>
      <table className="table">
        <thead>
          <tr>
            <th scope="col">Group</th>
            <th scope="col">Leader</th>
            <th scope="col">Decision</th>
            <th scope="col">Decided</th>
            <th scope="col">Comment</th>
          </tr>
        </thead>
        <tbody>
          {approvals.map((a) => (
            <tr key={a.id} className={a.required_leader_id === me ? "mine" : undefined}>
              <td>
                <strong>{a.group_name || a.group_id}</strong>
              </td>
              <td>
                {a.leader_username || shortId(a.required_leader_id)}
                {a.required_leader_id === me && <small>You are this group’s leader</small>}
              </td>
              <td>
                <DecisionBadge decision={a.decision} />
              </td>
              <td>{date(a.decided_at)}</td>
              <td className="cr-comment">{a.comment || "—"}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

const EVENT_LABEL = {
  submitted: "Submitted for approval",
  approved: "Approved",
  rejected: "Rejected",
  queued: "Added to the queue",
  started: "Started running",
  finished: "Finished",
  cancelled: "Cancelled",
};

function eventNote(event) {
  const d = event.detail ?? {};
  if (event.event === "finished") return [STATUS_LABEL[d.status], d.message].filter(Boolean).join(" · ");
  if (event.event === "cancelled") return d.message || d.comment || "";
  if (event.event === "submitted") return d.required_groups ? `${d.required_groups} group approval(s) required` : "";
  return d.comment ?? "";
}

export function ActivityLog({ events, me, names }) {
  if (!events?.length) return <p className="empty">No activity recorded.</p>;
  return (
    <ol className="cr-timeline">
      {events.map((e) => (
        <li key={e.id}>
          <div>
            <strong>{EVENT_LABEL[e.event] ?? e.event}</strong>
            {eventNote(e) && <span className="cr-note">{eventNote(e)}</span>}
          </div>
          <small>
            <Who id={e.actor} me={me} names={names} /> · {date(e.created_at)}
          </small>
        </li>
      ))}
    </ol>
  );
}

import { date, requestHref, viewerId } from "../../migration/api.js";
import { OPERATION_LABEL } from "../../migration/changeRequests.js";
import { ErrorBox } from "../../migration/components.jsx";
import { Who } from "./ChangeRequestParts.jsx";
import { namesFrom } from "./NamesFrom.jsx";

// Requests, across every database, that wait on a group the signed-in user belongs to.
// `approvals` comes from usePendingApprovals in MigrationApp, which also drives the sidebar count.
export function ApprovalInbox({ approvals, databases = [] }) {
  const me = viewerId();
  const names = namesFrom(approvals.items);
  const nameOf = (id) => databases.find((d) => d.id === id)?.name ?? id;
  const { items, error, loading, reload } = approvals;
  return (
    <section className="panel" aria-label="Approvals waiting for you">
      <div className="panel-heading">
        <div>
          <h2>
            Waiting for your approval <span className="badge badge-off count">{items.length}</span>
          </h2>
          <p>You belong to a group with access to these databases. Open a request to read its SQL and decide.</p>
        </div>
        <button className="btn" onClick={reload} disabled={loading}>
          {loading ? "Refreshing…" : "Refresh"}
        </button>
      </div>
      <ErrorBox error={error} />
      {items.length ? (
        <div className="table-scroll" role="region" aria-label="Approvals waiting for you table" tabIndex={0}>
          <table className="table">
            <thead>
              <tr>
                <th scope="col">Request</th>
                <th scope="col">Database</th>
                <th scope="col">Your groups</th>
                <th scope="col">Requested by</th>
                <th scope="col">Submitted</th>
                <th scope="col">Review</th>
              </tr>
            </thead>
            <tbody>
              {items.map((r) => (
                <tr key={r.id}>
                  <td>
                    <strong>{OPERATION_LABEL[r.operation] ?? r.operation}</strong>
                    <small>{r.migration_name}</small>
                  </td>
                  <td>{nameOf(r.database_id)}</td>
                  <td>
                    {r.approvals
                      .filter((a) => a.mine && a.decision === "pending")
                      .map((a) => a.group_name || a.group_id)
                      .join(", ") || "—"}
                  </td>
                  <td>
                    <Who id={r.requested_by} me={me} names={names} />
                  </td>
                  <td>{date(r.created_at)}</td>
                  <td>
                    <a className="btn btn-primary btn-small" href={requestHref(r.database_id, r.id)}>
                      Review →
                    </a>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : (
        !error &&
        !loading && (
          <div className="empty">
            <span>✓</span>
            <h3>Nothing is waiting for you</h3>
            <p>When someone submits a change to a database your group can access, it will appear here.</p>
          </div>
        )
      )}
    </section>
  );
}

import { date, requestHref, viewerId } from "../../migration/api.js";
import { OPERATION_LABEL, namesFrom } from "../../migration/changeRequests.js";
import { ErrorBox } from "../../migration/components.jsx";
import { ApprovalMeter, RequestBadge, Who } from "./ChangeRequestParts.jsx";

// Change requests of a database, or of one migration. `list` is the result of
// useChangeRequestList, so a caller that also needs the data (to disable a button while a
// request is open, say) fetches and polls once.
export function ChangeRequestList({ list, databaseId, title = "Change requests", hideMigration = false }) {
  const { items, error, loading, live, reload } = list;
  const me = viewerId();
  const names = namesFrom(items);

  return (
    <section className="panel" aria-label={title}>
      <div className="panel-heading">
        <h2>
          {title} <span className="badge badge-off count">{items.length}</span>
        </h2>
        {live && <span className="cr-live">Updating automatically</span>}
      </div>
      <ErrorBox error={error} />
      {error && <button className="btn" onClick={reload}>Retry change requests</button>}
      {loading ? (
        <p role="status">Loading change requests…</p>
      ) : items.length ? (
        <div className="table-scroll" role="region" aria-label={`${title} table`} tabIndex={0}>
          <table className="table">
            <thead>
              <tr>
                <th scope="col">Request</th>
                <th scope="col">Status</th>
                <th scope="col">Approvals</th>
                <th scope="col">Requested by</th>
                <th scope="col">Submitted</th>
                <th scope="col">Details</th>
              </tr>
            </thead>
            <tbody>
              {items.map((r) => (
                <tr key={r.id}>
                  <td>
                    <strong>{OPERATION_LABEL[r.operation] ?? r.operation}</strong>
                    {!hideMigration && <small>{r.migration_name}</small>}
                  </td>
                  <td>
                    <RequestBadge status={r.status} />
                    {r.queue_position ? (
                      <small>{r.queue_position === 1 ? "Next to run" : `#${r.queue_position} in queue`}</small>
                    ) : null}
                  </td>
                  <td>
                    <ApprovalMeter request={r} />
                  </td>
                  <td>
                    <Who id={r.requested_by} me={me} names={names} />
                  </td>
                  <td>{date(r.created_at)}</td>
                  <td>
                    <a className="cr-link" href={requestHref(databaseId, r.id)}>
                      View →
                    </a>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : !error && (
        <div className="empty">
          <span>✓</span>
          <h3>No change requests yet</h3>
          <p>Submitting a migration for approval creates a request that appears here.</p>
        </div>
      )}
    </section>
  );
}

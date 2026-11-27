import { useEffect, useMemo, useState } from "react";
import { date, href, requestHref } from "../../migration/api.js";
import { OPERATION_LABEL, isOpen } from "../../migration/changeRequests.js";
import { ErrorBox } from "../../migration/components.jsx";
import { fetchExecutionHistory, latestByMigration, nextOperations } from "../../migration/executionHistory.js";
import { SubmitRequestDialog } from "./SubmitRequestDialog.jsx";

// Every execution on the database in order, with a switch that hides rollbacks together with
// the runs they undid. Rows of a migration that can be rolled back or rolled forward carry
// that action; it opens the same approval request dialog as the details page.
//   migrations  the migration definitions (state, SQL), to decide which action a row offers
//   requests    useChangeRequestList result, so a button stays off while a request is open
export function ExecutionHistory({ databaseId, migrations, canEdit, requests }) {
  const [hideRolledBack, setHideRolledBack] = useState(false);
  const [retry, setRetry] = useState(0);
  const [state, setState] = useState(null);
  const [target, setTarget] = useState(null); // { migration, operation }

  // A failed rollback or forward can add an attempt without changing definition state.
  useEffect(() => {
    let active = true;
    (async () => {
      try {
        const entries = await fetchExecutionHistory(databaseId, hideRolledBack);
        if (active) setState({ databaseId, hideRolledBack, migrations, retry, entries, error: "" });
      } catch (e) {
        if (active) setState({ databaseId, hideRolledBack, migrations, retry, entries: [], error: e.message });
      }
    })();
    return () => {
      active = false;
    };
  }, [databaseId, hideRolledBack, migrations, retry]);

  const loading = !state || state.databaseId !== databaseId || state.hideRolledBack !== hideRolledBack
    || state.migrations !== migrations || state.retry !== retry;
  const entries = loading ? [] : state.entries;
  const latest = latestByMigration(entries);
  const byId = useMemo(() => new Map(migrations.map((m) => [m.id, m])), [migrations]);
  const openFor = (migrationId) =>
    requests.items.find((r) => r.migration_id === migrationId && isOpen(r.status));

  return (
    <section className="panel" aria-label="Execution history">
      <div className="panel-heading">
        <h2>
          Execution history <span className="badge badge-off count">{entries.length}</span>
        </h2>
        <label className="exec-filter">
          <input
            type="checkbox"
            checked={hideRolledBack}
            onChange={(e) => setHideRolledBack(e.target.checked)}
          />{" "}
          Hide rolled-back executions
        </label>
      </div>
      {loading ? (
        <p role="status">Loading execution history…</p>
      ) : state.error ? (
        <>
          <ErrorBox error={state.error} />
          <button className="btn" onClick={() => setRetry((n) => n + 1)}>Try again</button>
        </>
      ) : entries.length ? (
        <div className="table-scroll" role="region" aria-label="Execution history table" tabIndex={0}>
          <table className="table">
            <thead>
              <tr>
                <th scope="col">Success order</th>
                <th scope="col">Migration</th>
                <th scope="col">Execution ID</th>
                <th scope="col">Operation</th>
                <th scope="col">Status</th>
                <th scope="col">Executor</th>
                <th scope="col">Started</th>
                {canEdit && <th scope="col">Next step</th>}
              </tr>
            </thead>
            <tbody>
              {entries.map((e) => {
                const migration = byId.get(e.migration_id);
                const operations =
                  canEdit && migration && latest.get(e.migration_id) === e ? nextOperations(migration) : [];
                const open = operations.length ? openFor(e.migration_id) : undefined;
                return (
                  <tr key={e.execution_id}>
                    <td className="mono">{e.success_order ?? "—"}</td>
                    <td>
                      <a className="text-link" href={href("details", databaseId, e.migration_id)}>{e.migration_name}</a>
                      {e.migration_version ? <small>v{e.migration_version}</small> : null}
                    </td>
                    <td className="mono">
                      {e.execution_id}
                      {e.reverses_execution_id && <small>Reverses {e.reverses_execution_id}</small>}
                    </td>
                    <td>{OPERATION_LABEL[e.operation] ?? e.operation}</td>
                    <td>
                      <span className={`badge ${e.status}`}>{e.status}</span>
                      {e.error && <small title={e.error}>{e.postgres_error_code || "Error"}: {e.error}</small>}
                    </td>
                    <td>{e.executor_user_id}</td>
                    <td>{date(e.started_at)}</td>
                    {canEdit && (
                      <td>
                        {open ? (
                          <a className="cr-link" href={requestHref(databaseId, open.id)}>
                            Request open →
                          </a>
                        ) : (
                          operations.map(([operation, label]) => (
                            <button
                              key={operation}
                              type="button"
                              className="btn"
                              disabled={requests.loading || Boolean(requests.error)}
                              onClick={() => setTarget({ migration, operation })}
                            >
                              {label}
                            </button>
                          ))
                        )}
                      </td>
                    )}
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      ) : (
        <div className="empty">
          <h3>{hideRolledBack ? "Nothing to show" : "No executions yet"}</h3>
          <p>
            {hideRolledBack
              ? "Every execution so far was rolled back. Turn the filter off to see them."
              : "Executions appear here once a change request has run."}
          </p>
        </div>
      )}
      {target && (
        <SubmitRequestDialog
          databaseId={databaseId}
          migration={target.migration}
          operation={target.operation}
          onClose={() => setTarget(null)}
        />
      )}
    </section>
  );
}

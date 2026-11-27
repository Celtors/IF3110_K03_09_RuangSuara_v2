import { useState } from "react";
import { href, date, requestHref } from "../../migration/api.js";
import { database } from "../../migration/navigation.js";
import { Badge, SQL } from "../../migration/components.jsx";
import { isOpen } from "../../migration/changeRequests.js";
import { isUnattemptedDraft, nextOperations } from "../../migration/executionHistory.js";
import { useChangeRequestList } from "../../migration/useChangeRequests.js";
import { ChangeRequestList } from "./ChangeRequestList.jsx";
import { SubmitRequestDialog } from "./SubmitRequestDialog.jsx";

export function Details({ current: m, canEdit, nextOperation, reload }) {
  const [operation, setOperation] = useState("");
  const requests = useChangeRequestList(database, m.id, reload);
  const openRequest = requests.items.find((r) => isOpen(r.status));
  const actionsReady = canEdit && !requests.loading && !requests.error && !openRequest;
  const operations = canEdit ? nextOperations({ ...m, next_operation: nextOperation }) : [];
  return (
    <>
      <section className="panel">
        <div className="panel-heading">
          <div>
            <h2>{m.name}</h2>
            <p>{m.description}</p>
          </div>
          <div className="actions">
            <Badge state={m.state} />
            {actionsReady && isUnattemptedDraft(m) && !requests.items.some((r) => r.attempt_id) && (
              <a
                className="btn btn-primary"
                href={href("editor", database, m.id)}
              >
                Edit draft
              </a>
            )}
          </div>
        </div>
        <dl className="details-grid">
          {[
            ["Version", m.version ? `v${m.version}` : "Unapplied"],
            ["Database", m.database_id],
            ["Author", m.author_user_id],
            ["Release", m.release_label || "—"],
            ["Created", date(m.created_at)],
            ["Updated", date(m.updated_at)],
          ].map(([k, v]) => (
            <div key={k}>
              <dt>{k}</dt>
              <dd>{v}</dd>
            </div>
          ))}
        </dl>
        <SQL value={m.sql_script} />
        {m.sql_down?.trim() && (
          <details>
            <summary>SQL down (used by rollback)</summary>
            <SQL value={m.sql_down} />
          </details>
        )}
        {m.irreversible && (
          <div className="alert notice">
            Irreversible: {m.irreversible_reason || "no reason recorded"}. It cannot be rolled back.
          </div>
        )}
      </section>

      {(operations.length > 0 || openRequest) && (
        <section className="panel">
          <div className="panel-heading">
            <div>
              <h2>Run this migration</h2>
              <p>
                Every execution needs approval from the leader of each group with access to this database, then runs from the
                queue.
              </p>
            </div>
            <div className="actions">
              {operations.map(([op, label]) => (
                <button
                  key={op}
                  className="btn btn-primary"
                  disabled={!actionsReady}
                  onClick={() => setOperation(op)}
                >
                  {label}
                </button>
              ))}
            </div>
          </div>
          {openRequest && (
            <div className="alert notice">
              A change request is already open for this migration.{" "}
              <a className="cr-link" href={requestHref(database, openRequest.id)}>
                View request →
              </a>
            </div>
          )}
        </section>
      )}

      <ChangeRequestList list={requests} databaseId={database} hideMigration title="Change requests" />
      <section className="panel attempts">
        <div className="panel-heading">
          <h2>
            Execution attempts{" "}
            <span className="badge badge-off count">
              {m.attempts?.length ?? 0}
            </span>
          </h2>
        </div>
        {m.attempts?.length ? (
          m.attempts.map((a, i) => (
            <details key={a.id} open={i === m.attempts.length - 1}>
              <summary>
                <Badge state={a.status} /> {a.operation} · {a.version ? `#${a.version} · ` : ""}
                {a.executor_user_id} · {date(a.started_at)}
              </summary>
              <div className="attempt-body">
                <p>Completed: {date(a.completed_at)}</p>
                <p>Execution ID <code>{a.id}</code></p>
                {a.reverses_execution_id && <p>Reverses execution <code>{a.reverses_execution_id}</code></p>}
                {a.error && (
                  <div
                    className={`alert notice ${a.status === "failed" ? "error" : ""}`}
                  >
                    {a.postgres_error_code && (
                      <strong>SQLSTATE {a.postgres_error_code}: </strong>
                    )}
                    {a.error}
                  </div>
                )}
                <SQL value={a.sql_script_snapshot} />
              </div>
            </details>
          ))
        ) : (
          <p className="empty">This migration has not been executed.</p>
        )}
      </section>
      {operation && (
        <SubmitRequestDialog
          databaseId={database}
          migration={m}
          operation={operation}
          onClose={() => setOperation("")}
        />
      )}
    </>
  );
}

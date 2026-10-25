import { useState } from "react";
import { api, href, date } from "../../migration/api.js";
import { database, base } from "../../migration/navigation.js";
import { Badge, ErrorBox } from "../../migration/components.jsx";
export function HistoryView({ history, reload }) {
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  async function reconcile() {
    setBusy(true);
    setError("");
    try {
      await api(`${base}/migrations/reconcile`, "POST");
      reload();
    } catch (e) {
      setError(e.message);
    } finally {
      setBusy(false);
    }
  }
  const applied = history.migrations.filter(
    (m) => m.state === "applied",
  ).length;
  return (
    <>
      <div className="stats">
        <div>
          <span>Applied migrations</span>
          <strong>{applied.toString().padStart(2, "0")}</strong>
        </div>
        <div>
          <span>Drafts & unresolved</span>
          <strong>
            {(history.migrations.length - applied).toString().padStart(2, "0")}
          </strong>
        </div>
        <div>
          <span>Current version</span>
          <strong>
            v{Math.max(0, ...history.migrations.map((m) => m.version ?? 0))}
          </strong>
        </div>
      </div>
      <ErrorBox error={error} />
      <section className="panel">
        <div className="panel-heading">
          <h2>
            All migrations{" "}
            <span className="badge badge-off count">
              {history.migrations.length}
            </span>
          </h2>
          <div className="actions">
            {history.can_edit && (
              <>
                <button className="btn" onClick={reconcile} disabled={busy}>
                  {busy ? "Reconciling…" : "Reconcile outcomes"}
                </button>
                <a className="btn btn-primary" href={href("editor", database)}>
                  + New migration
                </a>
              </>
            )}
          </div>
        </div>
        {history.migrations.length ? (
          <div
            className="table-scroll"
            role="region"
            aria-label="Migration history"
            tabIndex={0}
          >
            <table className="table">
              <thead>
                <tr>
                  <th scope="col">Version</th>
                  <th scope="col">Migration</th>
                  <th scope="col">Status</th>
                  <th scope="col">Executor</th>
                  <th scope="col">Executed</th>
                  <th scope="col">Details</th>
                </tr>
              </thead>
              <tbody>
                {history.migrations.map((m) => (
                  <tr key={m.id}>
                    <td className="mono">
                      {m.version ? `v${m.version}` : "—"}
                    </td>
                    <td>
                      <strong>{m.name}</strong>
                      <small>
                        {m.description || m.release_label || "No description"}
                      </small>
                    </td>
                    <td>
                      <Badge state={m.state} />
                    </td>
                    <td>{m.last_executor_user_id || "—"}</td>
                    <td>{date(m.last_executed_at)}</td>
                    <td>
                      <a
                        className="text-link"
                        href={href("details", database, m.id)}
                      >
                        View →
                      </a>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : (
          <div className="empty">
            <span>◷</span>
            <h3>Your history starts here</h3>
            <p>
              {history.can_edit
                ? "Create a draft to prepare your first database change."
                : "Applied migrations and saved drafts will appear here."}
            </p>
          </div>
        )}
      </section>
    </>
  );
}

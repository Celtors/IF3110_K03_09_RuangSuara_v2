import { useRef, useState } from "react";
import { apiWithSessionRetry, databaseBase } from "../../migration/api.js";
import { SqlResultViewer } from "./SqlResultViewer.jsx";

const EXAMPLE = `SELECT d.name AS department, COUNT(s.id) AS total_students
FROM departments d
LEFT JOIN students s ON d.id = s.department_id
GROUP BY d.name;`;

// The database comes from the shared Migration-workspace state (prop). This
// component has no selector and no database state of its own.
export function SqlConsole({ databaseId }) {
  const [sql, setSql] = useState("");
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState();
  const [error, setError] = useState();
  const inFlight = useRef(false); // blocks double submits before React re-renders

  async function run(event) {
    event?.preventDefault();
    if (!databaseId || !sql.trim() || busy || inFlight.current) return;
    inFlight.current = true;
    setBusy(true);
    setResult(undefined);
    setError(undefined);
    try {
      setResult(await apiWithSessionRetry(`${databaseBase(databaseId)}/query`, "POST", { query: sql }));
    } catch (e) {
      setError({ message: e.message, code: e.code, details: e.details });
    } finally {
      inFlight.current = false;
      setBusy(false);
    }
  }
  function clear() {
    if (busy) return;
    setSql("");
    setResult(undefined);
    setError(undefined);
  }
  function onKeyDown(event) {
    if (event.key === "Enter" && (event.ctrlKey || event.metaKey)) run(event);
  }

  return (
    <>
      <div className="alert notice">
        The SQL Console is read-only. Submit a migration to change data or schema.
      </div>
      <form className="panel" onSubmit={run}>
        <div className="panel-heading">
          <h2>SQL</h2>
          <span className="mono">PostgreSQL · one statement</span>
        </div>
        <label className="sr-only" htmlFor="console-sql">SQL statement</label>
        <textarea
          className="input code-editor"
          id="console-sql"
          value={sql}
          onChange={(e) => setSql(e.target.value)}
          onKeyDown={onKeyDown}
          readOnly={busy}
          spellCheck={false}
          maxLength={262144}
          placeholder={EXAMPLE}
        />
        <div className="panel-bottom console-bar">
          <span className="console-status" role="status" aria-live="polite">
            {busy ? "Running…" : error ? "Last run failed" : result ? "Last run succeeded" : "Ready · Ctrl+Enter to run"}
          </span>
          <div className="actions">
            <button type="button" className="btn" onClick={clear} disabled={busy}>
              Clear
            </button>
            <button className="btn btn-primary" disabled={busy || !databaseId || !sql.trim()}>
              {busy ? "Running…" : "Execute"}
            </button>
          </div>
        </div>
      </form>
      <SqlResultViewer result={result} error={error} />
    </>
  );
}
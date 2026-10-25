import { useState } from "react";
import { api } from "../../migration/api.js";
import { base } from "../../migration/navigation.js";
import { Badge, ErrorBox, SQL } from "../../migration/components.jsx";
export function QueryPlan() {
  const [sql, setSQL] = useState("SELECT 1;");
  const [result, setResult] = useState();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  async function analyze(event) {
    event.preventDefault();
    setBusy(true);
    setError("");
    setResult(undefined);
    try {
      setResult(await api(`${base}/query-plan`, "POST", { sql_script: sql }));
    } catch (e) {
      setError(e.message);
    } finally {
      setBusy(false);
    }
  }
  return (
    <>
      <ErrorBox error={error} />
      <div className="alert notice">
        Plan only. Supports a single SELECT, INSERT, UPDATE, or DELETE. Schema
        changes and EXPLAIN ANALYZE are unsupported. Functions are limited to
        explicitly qualified supported pg_catalog functions.
      </div>
      <form className="panel" onSubmit={analyze}>
        <div className="panel-heading">
          <h2>SQL statement</h2>
          <Badge state="plan only" />
        </div>
        <label className="sr-only" htmlFor="plan-sql">
          Statement to explain
        </label>
        <textarea
          className="input code-editor"
          id="plan-sql"
          value={sql}
          onChange={(e) => {
            setSQL(e.target.value);
            setResult(undefined);
          }}
          required
          maxLength={262144}
        />
        <div className="panel-bottom">
          <small>
            Estimated costs and rows; actual execution statistics are
            unavailable.
          </small>
          <button className="btn btn-primary" disabled={busy}>
            {busy ? "Planning…" : "Explain query"}
          </button>
        </div>
      </form>
      {result && (
        <section className="panel attempts">
          <div className="panel-heading">
            <h2>Execution plan</h2>
          </div>
          <div
            className="table-scroll"
            role="region"
            aria-label="Query plan"
            tabIndex={0}
          >
            <table className="table">
              <thead>
                <tr>
                  <th scope="col">Plan node</th>
                  <th scope="col">Relation / index</th>
                  <th scope="col">Join</th>
                  <th scope="col">Cost</th>
                  <th scope="col">Rows</th>
                </tr>
              </thead>
              <tbody>
                {result.nodes.map((n, i) => (
                  <tr key={i}>
                    <td className="mono">
                      {"│ ".repeat(n.depth)}
                      {n.depth ? "↳ " : ""}
                      {n.node_type}
                    </td>
                    <td>
                      {n.relation || "—"}
                      {n.index && <small>{n.index}</small>}
                    </td>
                    <td>{n.join_type || "—"}</td>
                    <td>{n.total_cost}</td>
                    <td>{n.estimated_rows}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <details>
            <summary>Raw PostgreSQL JSON</summary>
            <SQL value={JSON.stringify(result.plan, null, 2)} />
          </details>
        </section>
      )}
    </>
  );
}

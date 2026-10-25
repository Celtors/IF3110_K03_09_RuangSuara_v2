import { useState } from "react";
import { api, href, draftKey } from "../../migration/api.js";
import { database, base } from "../../migration/navigation.js";
import { ErrorBox, SQL } from "../../migration/components.jsx";
export function SchemaDiff({ canEdit }) {
  const [text, setText] = useState('{\n  "name": "public",\n  "tables": []\n}');
  const [schemaName, setSchemaName] = useState("public");
  const [result, setResult] = useState();
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  async function load() {
    setBusy(true);
    setError("");
    try {
      const s = await api(
        `${base}/schema?name=${encodeURIComponent(schemaName)}`,
      );
      setText(JSON.stringify(s, null, 2));
      setResult(undefined);
    } catch (e) {
      setError(e.message);
    } finally {
      setBusy(false);
    }
  }
  async function compare() {
    setBusy(true);
    setError("");
    setResult(undefined);
    try {
      setResult(
        await api(`${base}/schema-diff`, "POST", {
          desired: JSON.parse(text),
        }),
      );
    } catch (e) {
      setError(e.message);
    } finally {
      setBusy(false);
    }
  }
  function useSQL() {
    if (!result?.sql_script) return;
    sessionStorage.setItem(draftKey(database), result.sql_script);
    location.assign(href("editor", database));
  }
  return (
    <>
      <ErrorBox error={error} />
      <div className="alert notice">
        The desired schema is a full snapshot. Omitted tables and columns are
        proposed for removal. Start by loading the current schema, then edit it.
      </div>
      <section className="panel">
        <div className="panel-heading">
          <h2>Desired schema · JSON</h2>
          <div className="actions">
            <label className="sr-only" htmlFor="schema-name">
              Schema name
            </label>
            <input
              className="input"
              id="schema-name"
              value={schemaName}
              onChange={(e) => setSchemaName(e.target.value)}
              maxLength={63}
            />
            <button className="btn" onClick={load} disabled={busy}>
              Load current schema
            </button>
          </div>
        </div>
        <label className="sr-only" htmlFor="schema-json">
          Desired schema JSON
        </label>
        <textarea
          className="input"
          id="schema-json"
          className="input code-editor"
          value={text}
          onChange={(e) => {
            setText(e.target.value);
            setResult(undefined);
          }}
          spellCheck={false}
        />
        <div className="panel-bottom">
          <small>
            Supports ordinary tables, columns, constraints, and simple btree
            indexes.
          </small>
          <button className="btn btn-primary" onClick={compare} disabled={busy}>
            {busy ? "Comparing…" : "Compare schemas"}
          </button>
        </div>
      </section>
      {result && (
        <section className="panel attempts">
          <div className="panel-heading">
            <h2>
              Proposed changes{" "}
              <span className="badge badge-off count">
                {result.differences.length}
              </span>
            </h2>
            {canEdit && result.sql_script && (
              <button className="btn btn-primary" onClick={useSQL}>
                Open SQL in editor →
              </button>
            )}
          </div>
          {result.warnings.map((w, i) => (
            <div className="alert notice warning" key={i}>
              {w}
            </div>
          ))}
          {result.differences.length ? (
            <>
              <div
                className="table-scroll"
                role="region"
                aria-label="Schema differences"
                tabIndex={0}
              >
                <table className="table">
                  <thead>
                    <tr>
                      <th scope="col">Change</th>
                      <th scope="col">Object</th>
                      <th scope="col">Review</th>
                    </tr>
                  </thead>
                  <tbody>
                    {result.differences.map((d, i) => (
                      <tr key={i}>
                        <td>{d.kind.replaceAll("_", " ")}</td>
                        <td className="mono">{d.object}</td>
                        <td>
                          {d.destructive
                            ? "Potentially destructive"
                            : "Review required"}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
              <SQL value={result.sql_script} />
            </>
          ) : (
            <p className="empty">No supported differences found.</p>
          )}
        </section>
      )}
    </>
  );
}

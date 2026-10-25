import { useState } from "react";
import { api, href, draftKey } from "../../migration/api.js";
import { database, base } from "../../migration/navigation.js";
import { ErrorBox, SQL } from "../../migration/components.jsx";
import Modal from "../../components/Modal.jsx";
export function Editor({ current, canEdit }) {
  const [draft, setDraft] = useState(() => ({
    name: current?.name ?? "",
    description: current?.description ?? "",
    release_label: current?.release_label ?? "",
    sql_script:
      current?.sql_script ?? sessionStorage.getItem(draftKey(database)) ?? "",
  }));
  const [saved, setSaved] = useState(current);
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const [confirm, setConfirm] = useState(false);
  const editable =
    canEdit && (!saved || ["draft", "failed"].includes(saved.state));
  const dirty =
    !saved ||
    ["name", "description", "release_label", "sql_script"].some(
      (k) => draft[k] !== saved[k],
    );
  const change = (key, value) => {
    setDraft((d) => ({ ...d, [key]: value }));
    setMessage("");
    setConfirm(false);
  };
  async function save(event) {
    event.preventDefault();
    setBusy(true);
    setError("");
    try {
      const m = await api(
        saved ? `${base}/migrations/${saved.id}` : `${base}/migrations`,
        saved ? "PATCH" : "POST",
        draft,
      );
      setSaved(m);
      // Keep the editable form in sync with server normalization (e.g. a
      // trimmed name), so a successful save really clears the dirty state.
      setDraft({
        name: m.name,
        description: m.description,
        release_label: m.release_label,
        sql_script: m.sql_script,
      });
      sessionStorage.removeItem(draftKey(database));
      setMessage("Draft saved. Review the SQL before executing.");
      window.history.replaceState(null, "", href("editor", database, m.id));
    } catch (e) {
      setError(e.message);
    } finally {
      setBusy(false);
    }
  }
  async function execute() {
    if (!saved) return;
    setBusy(true);
    setError("");
    try {
      await api(`${base}/migrations/${saved.id}/execute`, "POST");
      location.assign(href("details", database, saved.id));
    } catch (e) {
      setError(e.message);
      setConfirm(false);
      try {
        setSaved(await api(`${base}/migrations/${saved.id}`));
      } catch {
        /* keep execution error visible */
      }
    } finally {
      setBusy(false);
    }
  }
  return (
    <>
      <ErrorBox error={error} />
      {message && (
        <div className="alert notice success" role="status">
          {message}
        </div>
      )}
      {!editable && (
        <div className="alert notice">
          {canEdit
            ? "This migration is locked because it has been applied or its outcome is unresolved."
            : "You have viewing permission for this database."}
        </div>
      )}
      <form onSubmit={save}>
        <div className="editor-grid">
          <section className="panel">
            <div className="panel-heading">
              <h2>SQL script</h2>
              <span className="mono">PostgreSQL</span>
            </div>
            <label className="sr-only" htmlFor="sql">
              Migration SQL
            </label>
            <textarea
              className="input code-editor"
              id="sql"
              value={draft.sql_script}
              onChange={(e) => change("sql_script", e.target.value)}
              readOnly={!editable || busy}
              required
              maxLength={262144}
              placeholder={
                "CREATE TABLE public.example (\n  id integer PRIMARY KEY\n);"
              }
            />
            <div className="editor-foot">
              Multiple statements · one transaction · one applied version
            </div>
          </section>
          <section className="panel metadata-form">
            <h2>Migration information</h2>
            <label htmlFor="name">Name</label>
            <input
              className="input"
              id="name"
              value={draft.name}
              onChange={(e) => change("name", e.target.value)}
              required
              maxLength={200}
              readOnly={!editable || busy}
              placeholder="Add customer preferences"
            />
            <label htmlFor="description">Description</label>
            <textarea
              className="input"
              id="description"
              value={draft.description}
              onChange={(e) => change("description", e.target.value)}
              maxLength={4000}
              readOnly={!editable || busy}
            />
            <label htmlFor="release">
              Release label <small>optional</small>
            </label>
            <input
              className="input"
              id="release"
              value={draft.release_label}
              onChange={(e) => change("release_label", e.target.value)}
              maxLength={100}
              readOnly={!editable || busy}
              placeholder="October release"
            />
            <div className="alert notice">
              Execution applies this SQL to <strong>{database}</strong>. Failed
              transactions roll back. Uncertain outcomes require reconciliation.
            </div>
            {editable && (
              <>
                <button
                  className="btn btn-primary"
                  type="submit"
                  disabled={busy || !dirty}
                >
                  {busy ? "Working…" : "Save draft"}
                </button>
                <button
                  className="btn"
                  type="button"
                  disabled={busy || dirty || !saved}
                  onClick={() => setConfirm(true)}
                >
                  Review & execute
                </button>
              </>
            )}
            {saved && (
              <a
                className="text-link"
                href={href("details", database, saved.id)}
              >
                View details & attempts →
              </a>
            )}
          </section>
        </div>
      </form>
      {confirm && (
        <Modal
          title={`Execute “${saved?.name}”?`}
          onClose={() => setConfirm(false)}
          wide
        >
          <p>
            This changes database <strong>{database}</strong>. Confirm the exact
            saved script below.
          </p>
          <SQL value={saved?.sql_script ?? ""} />
          <div className="actions">
            <button
              className="btn"
              disabled={busy}
              onClick={() => setConfirm(false)}
            >
              Cancel
            </button>
            <button
              className="btn btn-primary"
              disabled={busy}
              onClick={execute}
            >
              {busy ? "Executing…" : "Execute migration"}
            </button>
          </div>
        </Modal>
      )}
    </>
  );
}

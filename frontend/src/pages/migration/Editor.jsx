import { useState } from "react";
import { apiWithSessionRetry, href, draftKey, requestHref } from "../../migration/api.js";
import { database, base } from "../../migration/navigation.js";
import { ErrorBox } from "../../migration/components.jsx";
import { isOpen } from "../../migration/changeRequests.js";
import { isUnattemptedDraft } from "../../migration/executionHistory.js";
import { replaceUrl } from "../../migration/router.js";
import { useChangeRequestList } from "../../migration/useChangeRequests.js";
import { SubmitRequestDialog } from "./SubmitRequestDialog.jsx";
export function Editor({ current, canEdit }) {
  const [draft, setDraft] = useState(() => ({
    name: current?.name ?? "",
    description: current?.description ?? "",
    release_label: current?.release_label ?? "",
    sql_script:
      current?.sql_script ?? sessionStorage.getItem(draftKey(database)) ?? "",
    sql_down: current?.sql_down ?? "",
    irreversible: current?.irreversible ?? false,
    irreversible_reason: current?.irreversible_reason ?? "",
  }));
  const [saved, setSaved] = useState(current);
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const [confirm, setConfirm] = useState(false);
  // The SQL is frozen while a change request for it is open: approval covers exactly that SQL.
  const requests = useChangeRequestList(saved ? database : "", saved?.id ?? "");
  const openRequest = requests.items.find((r) => isOpen(r.status));
  const attempted = requests.items.some((r) => r.attempt_id);
  const editable =
    canEdit &&
    !requests.loading &&
    !requests.error &&
    !openRequest &&
    !attempted &&
    (!saved || isUnattemptedDraft(saved));
  const dirty =
    !saved ||
    [
      "name",
      "description",
      "release_label",
      "sql_script",
      "sql_down",
      "irreversible",
      "irreversible_reason",
    ].some(
      (k) => draft[k] !== saved[k],
    );
  const change = (key, value) => {
    setDraft((d) => {
      const next = { ...d, [key]: value };
      // The server rejects SQL down on an irreversible migration, and a reason on a
      // reversible one, so the form never lets both be filled in.
      if (key === "irreversible") {
        if (value) next.sql_down = "";
        else next.irreversible_reason = "";
      }
      return next;
    });
    setMessage("");
    setConfirm(false);
  };
  async function save(event) {
    event.preventDefault();
    if (!editable || busy || !dirty) return;
    setBusy(true);
    setError("");
    try {
      const payload = draft;
      const m = await apiWithSessionRetry(
        saved ? `${base}/migrations/${saved.id}` : `${base}/migrations`,
        saved ? "PATCH" : "POST",
        payload,
      );
      setSaved(m);
      // Keep the editable form in sync with server normalization (e.g. a
      // trimmed name), so a successful save really clears the dirty state.
      setDraft({
        name: m.name,
        description: m.description,
        release_label: m.release_label,
        sql_script: m.sql_script,
        sql_down: m.sql_down ?? "",
        irreversible: m.irreversible ?? false,
        irreversible_reason: m.irreversible_reason ?? "",
      });
      sessionStorage.removeItem(draftKey(database));
      setMessage("Draft saved. Review the SQL, then submit it for approval.");
      replaceUrl(href("editor", database, m.id));
    } catch (e) {
      setError(e.message);
    } finally {
      setBusy(false);
    }
  }
  return (
    <>
      <ErrorBox error={error} />
      <ErrorBox error={requests.error} />
      {requests.error && <button className="btn" onClick={requests.reload}>Retry change requests</button>}
      {message && (
        <div className="alert notice success" role="status">
          {message}
        </div>
      )}
      {openRequest && (
        <div className="alert notice">
          This migration has an open change request, so it is locked until the request finishes or is cancelled.{" "}
          <a className="cr-link" href={requestHref(database, openRequest.id)}>
            View request →
          </a>
        </div>
      )}
      {!editable && !openRequest && (
        !canEdit ? (
          <div className="alert notice">You have viewing permission for this database.</div>
        ) : requests.loading ? (
          <p role="status">Checking change requests…</p>
        ) : !requests.error && (
          <div className="alert notice">This definition can no longer be edited. Create a new migration to change its SQL.</div>
        )
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
            <div className="panel-heading">
              <h2>SQL down (rollback)</h2>
              <label>
                <input
                  type="checkbox"
                  checked={draft.irreversible}
                  onChange={(e) => change("irreversible", e.target.checked)}
                  disabled={!editable || busy}
                />{" "}
                Irreversible
              </label>
            </div>
            {draft.irreversible ? (
              <>
                <label htmlFor="irreversible-reason">
                  Why this cannot be undone
                </label>
                <textarea
                  className="input"
                  id="irreversible-reason"
                  value={draft.irreversible_reason}
                  onChange={(e) => change("irreversible_reason", e.target.value)}
                  readOnly={!editable || busy}
                  maxLength={4000}
                  placeholder="Drops a column; the data cannot be restored by SQL down."
                />
                <div className="editor-foot">
                  Irreversible migrations cannot be rolled back.
                </div>
              </>
            ) : (
              <>
                <label className="sr-only" htmlFor="sql-down">
                  Rollback SQL
                </label>
                <textarea
                  className="input code-editor"
                  id="sql-down"
                  value={draft.sql_down}
                  onChange={(e) => change("sql_down", e.target.value)}
                  readOnly={!editable || busy}
                  maxLength={262144}
                  placeholder={"DROP TABLE IF EXISTS public.example;"}
                />
                <div className="editor-foot">
                  Undoes the SQL script, in reverse order. Required before
                  submitting unless the migration is irreversible.
                </div>
              </>
            )}
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
              Submitting asks every group with access to{" "}
              <strong>{database}</strong> to approve this SQL. Once approved it
              runs from the queue. Failed transactions roll back. Uncertain
              outcomes require reconciliation.
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
                  Submit for approval
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
      {confirm && saved && (
        <SubmitRequestDialog
          databaseId={database}
          migration={saved}
          operation="apply"
          onClose={() => setConfirm(false)}
        />
      )}
    </>
  );
}

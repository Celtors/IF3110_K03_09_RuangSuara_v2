import { href, date } from "../../migration/api.js";
import { database } from "../../migration/navigation.js";
import { Badge, SQL } from "../../migration/components.jsx";
export function Details({ current: m, canEdit }) {
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
            {canEdit && ["draft", "failed"].includes(m.state) && (
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
      </section>
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
                <Badge state={a.status} /> {a.version ? `v${a.version} · ` : ""}
                {a.executor_user_id} · {date(a.started_at)}
              </summary>
              <div className="attempt-body">
                <p>Completed: {date(a.completed_at)}</p>
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
    </>
  );
}

import { href } from "../../migration/api.js";
import { database } from "../../migration/navigation.js";
export function Home() {
  return (
    <>
      <form
        className="panel database-form"
        action="/migrations/history.html"
        method="get"
      >
        <label htmlFor="database">Managed database ID</label>
        <div>
          <input
            className="input"
            id="database"
            name="database"
            defaultValue={database}
            required
            maxLength={200}
          />
          <button className="btn btn-primary">Open migrations →</button>
        </div>
        <small>Use the identifier supplied by Database Management.</small>
      </form>
      <div className="card-grid">
        {[
          [
            "history",
            "01",
            "Review every change",
            "Browse applied versions, drafts, and execution outcomes.",
          ],
          [
            "schema-diff",
            "02",
            "Design the next schema",
            "Compare a desired schema and generate candidate SQL.",
          ],
          [
            "query-plan",
            "03",
            "Inspect before execution",
            "See scan types, indexes, joins, and estimated costs.",
          ],
        ].map(([p, n, t, d]) => (
          <a className="panel feature-card" href={href(p, database)} key={p}>
            <span className="card-number">{n}</span>
            <h2>{t}</h2>
            <p>{d}</p>
            <span className="arrow">↗</span>
          </a>
        ))}
      </div>
    </>
  );
}

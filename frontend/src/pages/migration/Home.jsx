import { href, workspaceHref } from "../../migration/api.js";
import { DatabaseSelector } from "./DatabaseSelector.jsx";
import { permissionLabel } from "../../migration/permission.js";

const CARDS = [
  ["migration", "01", "Review every change", "Browse applied versions, drafts, and execution outcomes."],
  ["console", "02", "Run ad-hoc SQL", "Run read-only SQL and inspect the results."],
  ["schema-diff", "03", "Design the next schema", "Compare a desired schema and generate candidate SQL."],
  ["plan", "04", "Inspect before execution", "See scan types, indexes, joins, and estimated costs."],
];

// Overview tab. databaseId / onDatabaseChange come from MigrationApp.
export function Home({ databaseId, onDatabaseChange, databases, selected, onOpenTab }) {
  const usable = Boolean(databaseId && selected);
  return (
    <>
      <DatabaseSelector
        databases={databases.items}
        loading={databases.loading}
        error={databases.error}
        onRetry={databases.reload}
        value={databaseId}
        onChange={onDatabaseChange}
      />
      {selected && (
        <p className="overview-summary">
          Access level <b>{permissionLabel(selected.permission)}</b> on <b>{selected.name}</b>
        </p>
      )}
      <div className="card-grid">
        {CARDS.map(([target, n, t, d]) => {
          const link =
            target === "schema-diff"
              ? { href: usable ? href("schema-diff", databaseId) : undefined }
              : {
                  href: workspaceHref(target, databaseId),
                  onClick: (e) => {
                    e.preventDefault();
                    if (usable) onOpenTab(target);
                  },
                };
          return (
            <a
              className={usable ? "panel feature-card" : "panel feature-card disabled"}
              aria-disabled={!usable}
              key={target}
              {...link}
            >
              <span className="card-number">{n}</span>
              <h2>{t}</h2>
              <p>{d}</p>
              <span className="arrow">↗</span>
            </a>
          );
        })}
      </div>
    </>
  );
}
import { useEffect, useState } from "react";
import { api, session, href } from "./api.js";
import { database, id, page, base, detailPath, titles } from "./navigation.js";
import { ErrorBox } from "./components.jsx";
import { Home } from "../pages/migration/Home.jsx";
import { HistoryView } from "../pages/migration/HistoryView.jsx";
import { Editor } from "../pages/migration/Editor.jsx";
import { Details } from "../pages/migration/Details.jsx";
import { SchemaDiff } from "../pages/migration/SchemaDiff.jsx";
import { QueryPlan } from "../pages/migration/QueryPlan.jsx";
export function MigrationApp() {
  const [ready, setReady] = useState(!database);
  const [error, setError] = useState("");
  const [history, setHistory] = useState({
    migrations: [],
    can_edit: false,
  });
  const [current, setCurrent] = useState();
  const [refresh, setRefresh] = useState(0);
  useEffect(() => {
    if (!database) return;
    let active = true;
    (async () => {
      try {
        await session();
        const h = await api(`${base}/migrations`);
        if (!active) return;
        setHistory(h);
        if (id) {
          const m = await api(detailPath);
          if (active) setCurrent(m);
        }
        if (active) {
          setError("");
          setReady(true);
        }
      } catch (e) {
        if (active) {
          setError(e.message);
          setReady(true);
        }
      }
    })();
    return () => {
      active = false;
    };
  }, [refresh]);
  const [title, subtitle] = titles[page] ?? titles.index;
  return (
    <div className="shell migration-app">
      <aside className="sidebar">
        <a className="brand-mark migration-brand" href="/index.html">
          RuangSuara
        </a>
        <div className="migration-navigation">
          <nav aria-label="Application navigation">
            <a className="nav-link" href="/admin/users.html">
              Administration
            </a>
          </nav>
          <div className="nav-label">MIGRATION</div>
          <nav aria-label="Migration navigation">
            {[
              ["index", "⌂", "Overview"],
              ["history", "◷", "Migrations"],
              ["schema-diff", "⇄", "Schema comparison"],
              ["query-plan", "⌘", "Query plans"],
            ].map(([p, icon, text]) => (
              <a
                aria-current={page === p ? "page" : undefined}
                className={
                  page === p ||
                  (p === "history" && ["editor", "details"].includes(page))
                    ? "nav-link active"
                    : "nav-link"
                }
                href={href(p, database)}
                key={p}
              >
                <span aria-hidden="true">{icon}</span>
                {text}
              </a>
            ))}
          </nav>
        </div>
        <div className="sidebar-foot">
          <span>PostgreSQL migrations</span>
          <small>Every change has a history.</small>
        </div>
      </aside>
      <div className="workspace">
        <header className="migration-header">
          <nav aria-label="Breadcrumb">
            <a href="/index.html">RuangSuara</a>
            <span aria-hidden="true">/</span>
            <a href={href("index", database)}>Migration</a>
          </nav>
          <span className="header-db">
            Database <b>{database || "Not selected"}</b>
          </span>
        </header>
        <main className="content">
          <div className="eyebrow">DATABASE / {database || "Not selected"}</div>
          <div className="page-head page-heading">
            <div>
              <h1>{title}</h1>
              <p>{subtitle}</p>
            </div>
            <span className="badge access-label">
              {!database
                ? "Select a database"
                : ready
                  ? error
                    ? "Unavailable"
                    : history.can_edit
                      ? "View + Edit"
                      : "View Only"
                  : "Connecting…"}
            </span>
          </div>
          <ErrorBox error={error} />
          {!database ? (
            <Home />
          ) : !ready ? (
            <div className="panel" role="status">
              Loading workspace…
            </div>
          ) : error && page !== "index" ? (
            <button
              className="btn btn-primary"
              onClick={() => setRefresh((v) => v + 1)}
            >
              Try again
            </button>
          ) : (
            <>
              {page === "index" && <Home />}
              {page === "history" && (
                <HistoryView
                  history={history}
                  reload={() => setRefresh((v) => v + 1)}
                />
              )}
              {page === "editor" &&
                (id && !current ? (
                  <ErrorBox error="Migration not found" />
                ) : (
                  <Editor current={current} canEdit={history.can_edit} />
                ))}
              {page === "details" &&
                (current ? (
                  <Details current={current} canEdit={history.can_edit} />
                ) : (
                  <ErrorBox error="Select a migration from history." />
                ))}
              {page === "schema-diff" && (
                <SchemaDiff canEdit={history.can_edit} />
              )}
              {page === "query-plan" && <QueryPlan />}
            </>
          )}
        </main>
        <footer className="migration-footer">
          RuangSuara <span>Migration · Database tools</span>
        </footer>
      </div>
    </div>
  );
}

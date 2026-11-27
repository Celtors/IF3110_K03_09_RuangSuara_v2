import { Fragment, useCallback, useEffect, useRef, useState } from "react";
import { api, session, href, workspaceHref, databaseBase } from "./api.js";
import { titles } from "./navigation.js";
import { replaceUrl, useRoute } from "./router.js";
import { ErrorBox } from "./components.jsx";
import { useAccessibleDatabases } from "./useAccessibleDatabases.js";
import { usePendingApprovals } from "./useChangeRequests.js";
import { ChangeRequestPage } from "../pages/migration/ChangeRequestPage.jsx";
import { Editor } from "../pages/migration/Editor.jsx";
import { Details } from "../pages/migration/Details.jsx";
import { SchemaDiff } from "../pages/migration/SchemaDiff.jsx";
import { Workspace } from "../pages/migration/Workspace.jsx";
import "./style.css";
import "./changeRequests.css";

const WORKSPACE_PAGES = ["index", "history", "query-plan"];
const TABS = ["overview", "viewer", "migration", "approvals", "console", "plan"];
const PAGE_TAB = { index: "overview", history: "migration", "query-plan": "plan" };
const EMPTY = { migrations: [], can_edit: false };
const tabTitles = {
  overview: titles.index,
  migration: titles.history,
  viewer: ["Database viewer", "Browse schemas, tables, and data."],
  console: [
    "SQL console",
    "Run read-only SQL on the selected database.",
  ],
  approvals: [
    "Approvals",
    "Changes across databases waiting for your decision as a group leader.",
  ],
  plan: titles["query-plan"],
};
const NAV = [
  ["overview", "⌂", "Overview"],
  ["viewer", "▦", "Database viewer"],
  ["migration", "◷", "Migrations"],
  ["approvals", "✓", "Approvals"],
  ["console", "▤", "SQL console"],
  ["schema-diff", "⇄", "Schema comparison"],
  ["plan", "⌘", "Query plans"],
];

function tabFor(route) {
  return TABS.includes(route.tab) ? route.tab : (PAGE_TAB[route.page] ?? "overview");
}
// The URL stays authoritative so a refresh restores database and tab. The view already holds
// this state, so only the address bar changes.
function syncLocation(databaseId, tab) {
  const q = new URLSearchParams(location.search);
  if (databaseId) q.set("database", databaseId);
  else q.delete("database");
  q.set("tab", tab);
  replaceUrl(`${location.pathname}?${q}`);
}

export function MigrationApp() {
  // This component is the shell of a single-page app: it stays mounted while the user moves
  // between pages, so the session, the database list and the approvals count are loaded once.
  const route = useRoute();
  const { page, id } = route;
  const workspace = WORKSPACE_PAGES.includes(page);
  // The selected database. Overview changes it in place; navigating to another URL sets it.
  const [databaseId, setDatabaseId] = useState(route.database);
  const [tab, setTab] = useState(() => tabFor(route));
  // A navigation (link, back or forward) hands the shell a new route; take its database and
  // tab in the same render so the old page never shows with the new URL.
  const [seenRoute, setSeenRoute] = useState(route);
  if (seenRoute !== route) {
    setSeenRoute(route);
    setDatabaseId(route.database);
    setTab(tabFor(route));
  }
  const [loaded, setLoaded] = useState({
    databaseId: "",
    id: "",
    history: EMPTY,
    current: undefined,
    error: "",
  });
  const [refresh, setRefresh] = useState(0);
  const sessionPromise = useRef(null);
  const ensureSession = useCallback(() => {
    if (!sessionPromise.current) {
      sessionPromise.current = session().catch((e) => {
        sessionPromise.current = null;
        throw e;
      });
    }
    return sessionPromise.current;
  }, []);
  const databases = useAccessibleDatabases(workspace, ensureSession);
  const approvals = usePendingApprovals(ensureSession);

  // A new page starts at the top, like a page load did, and refetches what it lists (a migration
  // just submitted, a request that moved on). The first render is the page load itself. This
  // runs before the fetch below so it can tell whether that fetch is already about to happen.
  const mainRef = useRef(null);
  const firstView = useRef(route.key);
  const fetchedFor = useRef("");
  useEffect(() => {
    if (firstView.current === route.key) return;
    firstView.current = route.key;
    if (fetchedFor.current === `${databaseId}|${id}`) setRefresh((v) => v + 1);
    approvals.reload();
    window.scrollTo(0, 0);
    mainRef.current?.focus({ preventScroll: true });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [route.key]);

  useEffect(() => {
    if (!databaseId) return;
    let active = true;
    fetchedFor.current = `${databaseId}|${id}`;
    (async () => {
      try {
        await ensureSession();
        const base = databaseBase(databaseId);
        const h = await api(`${base}/migrations`);
        let current;
        if (id) current = await api(`${base}/migrations/${encodeURIComponent(id)}`);
        if (active) setLoaded({ databaseId, id, history: h, current, error: "" });
      } catch (e) {
        if (active)
          setLoaded({ databaseId, id, history: EMPTY, current: undefined, error: e.message });
      }
    })();
    return () => {
      active = false;
    };
  }, [databaseId, id, refresh, ensureSession]);

  const ready = !databaseId || (loaded.databaseId === databaseId && loaded.id === id);
  const current = ready ? loaded.current : undefined;
  const historyData = ready && databaseId ? loaded.history : EMPTY;
  const error = ready && databaseId ? loaded.error : "";
  const selected = databases.items.find((d) => d.id === databaseId);
  const reload = useCallback(() => setRefresh((v) => v + 1), []);

  const changeDatabase = (next) => {
    setDatabaseId(next);
    syncLocation(next, tab);
  };
  const changeTab = (next) => {
    setTab(next);
    syncLocation(databaseId, next);
    if (next === "approvals") approvals.reload();
  };
  const navigateTab = (event, key) => {
    if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey || event.button !== 0) return;
    event.preventDefault(); // in-place switch; modified clicks keep normal link behaviour
    changeTab(key);
  };

  const [title, subtitle] = workspace ? tabTitles[tab] : (titles[page] ?? titles.index);
  const activeNav = workspace ? tab : page === "schema-diff" ? "schema-diff" : "migration";
  const shownName = selected?.name ?? (databaseId || "Not selected");
  useEffect(() => {
    document.title = `${title} · RuangSuara`;
  }, [title]);

  return (
    <div className="shell migration-app">
      <aside className="sidebar">
        <a className="brand-mark migration-brand" href="/">
          RuangSuara
        </a>
        <div className="migration-navigation">
          <nav aria-label="Application navigation">
            <a className="nav-link" href="/admin/users">
              Administration
            </a>
          </nav>
          <div className="nav-label">MIGRATION</div>
          <nav aria-label="Migration navigation">
            {NAV.map(([key, icon, text]) => {
              const isTab = TABS.includes(key);
              return (
                <a
                  aria-current={activeNav === key ? "page" : undefined}
                  id={`migration-nav-${key}`}
                  className={activeNav === key ? "nav-link active" : "nav-link"}
                  href={isTab ? workspaceHref(key, databaseId) : href(key, databaseId)}
                  onClick={isTab && workspace ? (e) => navigateTab(e, key) : undefined}
                  key={key}
                >
                  <span aria-hidden="true">{icon}</span>
                  {text}
                  {key === "approvals" && approvals.items.length > 0 && (
                    <span className="nav-count" aria-label={`${approvals.items.length} waiting`}>
                      {approvals.items.length}
                    </span>
                  )}
                </a>
              );
            })}
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
            <a href="/">RuangSuara</a>
            <span aria-hidden="true">/</span>
            <a href={workspaceHref("overview", databaseId)}>Migration</a>
          </nav>
          <span className="header-db">
            Database <b>{shownName}</b>
          </span>
        </header>
        <main className="content" ref={mainRef} tabIndex={-1}>
          <div className="eyebrow">DATABASE / {databaseId || "Not selected"}</div>
          <div className="page-head page-heading">
            <div>
              <h1>{title}</h1>
              <p>{subtitle}</p>
            </div>
            <span className="badge access-label">
              {!databaseId
                ? "Select a database"
                : !ready
                  ? "Connecting…"
                  : error
                    ? "Unavailable"
                    : historyData.can_edit
                      ? "View + Edit"
                      : "View Only"}
            </span>
          </div>
          {workspace ? (
            <Workspace
              databaseId={databaseId}
              onDatabaseChange={changeDatabase}
              tab={tab}
              onTabChange={changeTab}
              databases={databases}
              selected={selected}
              ready={ready}
              error={error}
              history={historyData}
              reload={reload}
              approvals={approvals}
            />
          ) : (
            <>
              <ErrorBox error={error} />
              {!databaseId ? (
                <div className="panel empty">
                  <h3>No database selected</h3>
                  <p>Choose a database in the Overview tab first.</p>
                  <a className="btn btn-primary" href={workspaceHref("overview", "")}>
                    Go to Overview
                  </a>
                </div>
              ) : !ready ? (
                <div className="panel" role="status">
                  Loading workspace…
                </div>
              ) : error ? (
                <button className="btn btn-primary" onClick={reload}>
                  Try again
                </button>
              ) : (
                <Fragment key={route.key}>
                  {page === "editor" &&
                    (id && !current ? (
                      <ErrorBox error="Migration not found" />
                    ) : (
                      <Editor current={current} canEdit={historyData.can_edit} />
                    ))}
                  {page === "details" &&
                    (current ? (
                      <Details
                        current={current}
                        canEdit={historyData.can_edit}
                        nextOperation={historyData.migrations.find((m) => m.id === current.id)?.next_operation}
                        reload={reload}
                      />
                    ) : (
                      <ErrorBox error="Select a migration from history." />
                    ))}
                  {page === "schema-diff" && <SchemaDiff canEdit={historyData.can_edit} />}
                  {page === "change-request" && (
                    <ChangeRequestPage
                      databaseId={databaseId}
                      requestId={route.request}
                      canEdit={historyData.can_edit}
                      onDecision={approvals.reload}
                    />
                  )}
                </Fragment>
              )}
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
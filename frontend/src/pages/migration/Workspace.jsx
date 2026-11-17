import { Home } from "./Home.jsx";
import { permissionLabel } from "../../migration/permission.js";
import { HistoryView } from "./HistoryView.jsx";
import { QueryPlan } from "./QueryPlan.jsx";
import { SqlConsole } from "./SqlConsole.jsx";
import { ErrorBox } from "../../migration/components.jsx";

// Read-only reminder; NOT a selector. Selection happens only in Overview.
function DatabaseContext({ selected, onChangeRequest }) {
  return (
    <div className="database-context">
      <span>
        Database <b>{selected.name}</b> · {permissionLabel(selected.permission)}
      </span>
      <button type="button" className="text-link" onClick={onChangeRequest}>
        Change in Overview tab
      </button>
    </div>
  );
}

function NeedDatabase({ onChangeRequest }) {
  return (
    <div className="panel empty">
      <h3>Select a database first</h3>
      <p>Choose one of your accessible databases in the Overview tab.</p>
      <button type="button" className="btn btn-primary" onClick={onChangeRequest}>
        Go to Overview
      </button>
    </div>
  );
}

export function Workspace({
  databaseId, onDatabaseChange, tab, onTabChange, databases, selected, ready, error, history, reload,
}) {
  const usable = Boolean(databaseId && selected); // a valid, accessible selection
  const toOverview = () => onTabChange("overview");
  const panel = (id) => ({
    role: "tabpanel",
    id: `panel-${id}`,
    "aria-labelledby": `tab-${id}`,
    hidden: tab !== id,
  });
  return (
    <>
      <div {...panel("overview")}>
        <Home
          databaseId={databaseId}
          onDatabaseChange={onDatabaseChange}
          databases={databases}
          selected={selected}
          onOpenTab={onTabChange}
        />
      </div>

      <div {...panel("migration")}>
        {usable ? (
          <>
            <DatabaseContext selected={selected} onChangeRequest={toOverview} />
            {!ready ? (
              <div className="panel" role="status">Loading workspace…</div>
            ) : error ? (
              <>
                <ErrorBox error={error} />
                <button className="btn btn-primary" onClick={reload}>Try again</button>
              </>
            ) : (
              <HistoryView databaseId={databaseId} history={history} reload={reload} />
            )}
          </>
        ) : (
          <NeedDatabase onChangeRequest={toOverview} />
        )}
      </div>

      <div {...panel("console")}>
        {usable ? (
          <>
            <DatabaseContext selected={selected} onChangeRequest={toOverview} />
            <SqlConsole key={databaseId} databaseId={databaseId} canEdit={selected.permission === "ViewEdit"} />
          </>
        ) : (
          <NeedDatabase onChangeRequest={toOverview} />
        )}
      </div>

      <div {...panel("plan")}>
        {usable ? (
          <>
            <DatabaseContext selected={selected} onChangeRequest={toOverview} />
            <QueryPlan key={databaseId} databaseId={databaseId} />
          </>
        ) : (
          <NeedDatabase onChangeRequest={toOverview} />
        )}
      </div>
    </>
  );
}
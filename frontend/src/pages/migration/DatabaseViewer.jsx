import { useEffect, useState } from "react";
import { api, databaseBase } from "../../migration/api.js";
import { ErrorBox } from "../../migration/components.jsx";
import { DatabaseSelector } from "./DatabaseSelector.jsx";

const pageSize = 50;
const metadata = {
  columns: [
    ["column_name", "Name"], ["data_type", "Type"], ["is_nullable", "Nullable"], ["column_default", "Default"],
  ],
  constraints: [
    ["constraint_name", "Name"], ["constraint_type", "Type"], ["column_name", "Column"],
    ["foreign_column_name", "Referenced column"], ["definition", "Definition"],
  ],
  indexes: [["indexname", "Name"], ["indexdef", "Definition"]],
};

function ViewerResource({ path, children }) {
  const [result, setResult] = useState(null);
  const [retry, setRetry] = useState(0);
  useEffect(() => {
    let active = true;
    api(path).then(
      (data) => { if (active) setResult({ path, retry, data }); },
      (error) => { if (active) setResult({ path, retry, error: error.message }); },
    );
    return () => { active = false; };
  }, [path, retry]);

  if (result?.path !== path || result.retry !== retry) {
    return <p role="status">Loading database information…</p>;
  }
  if (result.error) {
    return (
      <>
        <ErrorBox error={result.error} />
        <button type="button" className="btn" onClick={() => setRetry((n) => n + 1)}>Try again</button>
      </>
    );
  }
  return children(result.data ?? []);
}

function ViewerTable({ columns, rows, label }) {
  if (rows.length === 0) return <p>No {label.toLowerCase()} to display.</p>;
  return (
    <div className="table-scroll" tabIndex={0} role="region" aria-label={label}>
      <table className="table">
        <thead><tr>{columns.map(([key, heading]) => <th scope="col" key={key}>{heading}</th>)}</tr></thead>
        <tbody>
          {rows.map((row, index) => (
            <tr key={index}>
              {columns.map(([key]) => <td key={key}>{row[key] == null ? <em className="sql-null">NULL</em> : row[key]}</td>)}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function TableDetails({ path }) {
  const [view, setView] = useState("columns");
  const [offset, setOffset] = useState(0);
  const isData = view === "rows";
  return (
    <>
      <div className="field">
        <label htmlFor="viewer-table-view">Table view</label>
        <select className="input" id="viewer-table-view" value={view} onChange={(e) => setView(e.target.value)}>
          <option value="columns">Columns</option>
          <option value="constraints">Constraints</option>
          <option value="indexes">Indexes</option>
          <option value="rows">Data</option>
        </select>
      </div>
      {isData ? (
        <ViewerResource path={`${path}/rows?limit=${pageSize}&offset=${offset}`}>
          {(data) => (
            <>
              <ViewerTable label="Rows" columns={data.columns.map((name) => [name, name])} rows={data.rows} />
              <div className="actions">
                <button type="button" className="btn" disabled={offset === 0} onClick={() => setOffset(offset - pageSize)}>Previous</button>
                <span aria-live="polite">Page {offset / pageSize + 1}</span>
                <button type="button" className="btn" disabled={!data.has_more} onClick={() => setOffset(offset + pageSize)}>Next</button>
              </div>
            </>
          )}
        </ViewerResource>
      ) : (
        <ViewerResource path={`${path}/${view}`}>
          {(rows) => <ViewerTable label={view} columns={metadata[view]} rows={rows} />}
        </ViewerResource>
      )}
    </>
  );
}

function Tables({ path, tables }) {
  const [selected, setSelected] = useState(tables[0]?.name ?? "");
  const table = tables.some(({ name }) => name === selected) ? selected : "";
  if (!tables.length) return <p>No accessible tables in this schema.</p>;
  return (
    <>
      <div className="field">
        <label htmlFor="viewer-table-select">Table</label>
        <select className="input" id="viewer-table-select" value={table} onChange={(e) => setSelected(e.target.value)}>
          <option value="" disabled>Select a table</option>
          {tables.map(({ name }) => <option key={name} value={name}>{name}</option>)}
        </select>
      </div>
      {table && <TableDetails key={table} path={`${path}/${encodeURIComponent(table)}`} />}
    </>
  );
}

function Schemas({ base, schemas }) {
  const [selected, setSelected] = useState(schemas.find(({ name }) => name === "public")?.name ?? schemas[0]?.name ?? "");
  const schema = schemas.some(({ name }) => name === selected) ? selected : "";
  if (!schemas.length) return <p>No accessible schemas.</p>;
  const tables = `${base}/schemas/${encodeURIComponent(schema)}/tables`;
  return (
    <>
      <div className="field">
        <label htmlFor="viewer-schema-select">Schema</label>
        <select className="input" id="viewer-schema-select" value={schema} onChange={(e) => setSelected(e.target.value)}>
          <option value="" disabled>Select a schema</option>
          {schemas.map(({ name }) => <option key={name} value={name}>{name}</option>)}
        </select>
      </div>
      {schema && <ViewerResource path={tables}>{(data) => <Tables key={schema} path={tables} tables={data} />}</ViewerResource>}
    </>
  );
}

export function DatabaseViewer({ databaseId, onDatabaseChange, databases, selected }) {
  const base = databaseBase(databaseId);
  return (
    <>
      <DatabaseSelector id="viewer-database-select" databases={databases.items}
        loading={databases.loading} error={databases.error} onRetry={databases.reload}
        value={databaseId} onChange={onDatabaseChange} />
      {selected && (
        <section className="panel stack" aria-label="Database objects">
          <ViewerResource key={databaseId} path={`${base}/schemas`}>
            {(schemas) => <Schemas base={base} schemas={schemas} />}
          </ViewerResource>
        </section>
      )}
    </>
  );
}

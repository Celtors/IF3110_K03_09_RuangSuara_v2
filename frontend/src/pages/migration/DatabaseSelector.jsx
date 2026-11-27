import { permissionLabel } from "../../migration/permission.js";
export function DatabaseSelector({ id = "database-select", databases, loading, error, onRetry, value, onChange }) {
  const listed = databases.some((d) => d.id === value);
  const inaccessible = Boolean(value) && !loading && !error && !listed;
  return (
    <section className="panel database-selector">
      <label htmlFor={id}>Database</label>
      <select
        className="input"
        id={id}
        value={value}
        disabled={loading || (!databases.length && !value)}
        onChange={(e) => onChange(e.target.value)}
      >
        <option value="">
          {loading
            ? "Loading databases…"
            : databases.length
              ? "Select a database"
              : "No accessible databases"}
        </option>
        {databases.map((d) => (
          <option key={d.id} value={d.id}>
            {d.name} · {permissionLabel(d.permission)}
          </option>
        ))}
        {value && !listed && (
          <option value={value}>{loading ? "Loading…" : `${value} (not accessible)`}</option>
        )}
      </select>
      {error && (
        <div className="alert alert-error notice error" role="alert">
          <span>{error}</span>
          <button type="button" className="btn" onClick={onRetry}>
            Try again
          </button>
        </div>
      )}
      {inaccessible && (
        <div className="alert notice warning" role="alert">
          The selected database is not accessible to you (it may have been removed or your
          permission changed). Choose another database.
        </div>
      )}
      {!loading && !error && !databases.length && (
        <p className="muted">Ask an administrator to grant your group access to a database.</p>
      )}
    </section>
  );
}
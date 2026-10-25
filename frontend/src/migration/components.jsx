export function Badge({ state }) {
  return <span className={`badge ${state}`}>{state}</span>;
}
export function ErrorBox({ error }) {
  return error ? (
    <div className="alert alert-error notice error" role="alert">
      {error}
    </div>
  ) : null;
}
export function SQL({ value }) {
  return (
    <pre className="sql">
      <code>{value}</code>
    </pre>
  );
}

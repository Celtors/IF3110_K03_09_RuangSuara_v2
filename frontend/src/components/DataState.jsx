// Shared loading / error / empty handling for list pages.
export default function DataState({ loading, error, empty, emptyText, onRetry, children }) {
  return (
    <>
      {error && (
        <div className="alert alert-error" role="alert">
          <span>{error.message}</span>
          <button type="button" className="btn btn-small" onClick={onRetry}>Try again</button>
        </div>
      )}
      {loading && !empty && <div className="loading-bar" aria-hidden="true" />}
      {!error && loading && empty && <p className="muted pad">Loading…</p>}
      {!error && !loading && empty && <p className="empty">{emptyText}</p>}
      {!empty && children}
    </>
  )
}

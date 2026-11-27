export default function NotFound() {
  return (
    <main className="home">
      <div className="home-card stack">
        <span className="brand-mark home-brand">RuangSuara</span>
        <h1>Page not found</h1>
        <p className="muted">There is nothing at this address.</p>
        <div className="actions home-actions">
          <a className="btn btn-primary" href="/home">Go to the home page</a>
        </div>
      </div>
    </main>
  )
}

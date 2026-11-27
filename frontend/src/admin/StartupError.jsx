export function StartupError({ message }) {
    return (
        <main className="content">
            <p className="alert alert-error" role="alert">{message}</p>
        </main>
    )
}
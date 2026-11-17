import { Badge } from "../../migration/components.jsx";

const NUMERIC = new Set(["int2", "int4", "int8", "float4", "float8", "numeric", "oid"]);

function formatCell(value) {
    if (typeof value === "boolean") return value ? "true" : "false";
    if (value !== null && typeof value === "object") return JSON.stringify(value);
    return String(value);
}

function Meta({ ms, children }) {
    return (
        <p className="sql-meta">
            {children}
            {typeof ms === "number" && ms > 0 && <span>{ms} ms</span>}
        </p>
    );
}

// result: successful response body. error: { message, code, details }.
export function SqlResultViewer({ result, error }) {
    if (error) {
        const d = error.details ?? {};
        return (
            <section className="panel sql-result" aria-label="Execution error">
                <div className="panel-heading">
                    <h2>Execution failed</h2>
                    <Badge state="failed" />
                </div>
                <div className="alert notice error" role="alert">{error.message}</div>
                <Meta ms={d.execution_time_ms}>
                    {error.code && <span className="mono">{error.code}</span>}
                    {d.postgres_code && <span className="mono">SQLSTATE {d.postgres_code}</span>}
                    {d.position > 0 && <span>at character {d.position}</span>}
                </Meta>
            </section>
        );
    }
    if (!result) return null;

    if (!result.has_result_set) {
        return (
            <section className="panel sql-result" aria-label="Execution summary">
                <div className="panel-heading">
                    <h2>Statement executed</h2>
                    <Badge state="succeeded" />
                </div>
                <Meta ms={result.execution_time_ms}>
                    {result.command && <span className="mono">{result.command}</span>}
                    {typeof result.rows_affected === "number" && (
                        <span>{result.rows_affected} {result.rows_affected === 1 ? "row" : "rows"} affected</span>
                    )}
                </Meta>
                <p className="sql-empty">This statement did not return a result set.</p>
            </section>
        );
    }

    const numeric = (result.column_types ?? []).map((t) => NUMERIC.has(t));
    return (
        <section className="panel sql-result" aria-label="Query result">
            <div className="panel-heading">
                <h2>Result</h2>
                <Badge state="succeeded" />
            </div>
            <Meta ms={result.execution_time_ms}>
                <span>{result.row_count} {result.row_count === 1 ? "row" : "rows"}</span>
                {typeof result.rows_affected === "number" && <span>{result.rows_affected} affected in total</span>}
            </Meta>
            {result.truncated && (
                <div className="alert notice warning" role="status">
                    Showing the first {result.max_rows ?? result.row_count} rows; more rows exist. Add a WHERE or LIMIT to narrow the query.
                </div>
            )}
            {result.values_truncated && (
                <div className="alert notice warning" role="status">Some very long values were shortened.</div>
            )}
            <div className="table-scroll" role="region" aria-label="SQL result table" tabIndex={0}>
                <table className="table sql-table">
                    <thead>
                        <tr>
                            {result.columns.map((c, i) => (
                                <th scope="col" key={i} className={numeric[i] ? "num" : undefined}>
                                    {c}
                                    {result.column_types?.[i] && <small className="mono">{result.column_types[i]}</small>}
                                </th>
                            ))}
                        </tr>
                    </thead>
                    <tbody>
                        {result.rows.map((row, r) => (
                            <tr key={r}>
                                {row.map((value, c) => (
                                    <td key={c} className={numeric[c] ? "num" : undefined}>
                                        {value === null || value === undefined ? <em className="sql-null">NULL</em> : formatCell(value)}
                                    </td>
                                ))}
                            </tr>
                        ))}
                    </tbody>
                </table>
            </div>
            {result.row_count === 0 && <p className="sql-empty">The query returned no rows.</p>}
        </section>
    );
}
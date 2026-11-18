import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { SqlResultViewer } from "./SqlResultViewer.jsx";

const base = { has_result_set: true, truncated: false, execution_time_ms: 12, max_rows: 200 };

describe("SqlResultViewer", () => {
  it("renders dynamic columns, rows, NULLs, count and duration", () => {
    render(
      <SqlResultViewer
        result={{
          ...base,
          columns: ["id", "name", "note"],
          column_types: ["int4", "text", "text"],
          rows: [[1, "Alice", null], [2, "Bob", "hi"]],
          row_count: 2,
        }}
      />,
    );
    expect(screen.getAllByRole("columnheader").map((h) => h.textContent)).toEqual(["idint4", "nametext", "notetext"]);
    expect(screen.getByText("Alice")).toBeInTheDocument();
    expect(screen.getByText("NULL")).toBeInTheDocument();
    expect(screen.getByText("2 rows")).toBeInTheDocument();
    expect(screen.getByText("12 ms")).toBeInTheDocument();
    expect(screen.getByRole("region", { name: "SQL result table" })).toBeInTheDocument(); // scroll container
  });
  it("supports any column count and booleans/objects", () => {
    const cols = Array.from({ length: 30 }, (_, i) => `c${i}`);
    render(<SqlResultViewer result={{ ...base, columns: cols, rows: [cols.map((_, i) => (i === 0 ? true : i === 1 ? { a: 1 } : i))], row_count: 1 }} />);
    expect(screen.getAllByRole("columnheader")).toHaveLength(30);
    expect(screen.getByText("true")).toBeInTheDocument();
    expect(screen.getByText('{"a":1}')).toBeInTheDocument();
  });
  it("explains an empty result set but keeps the headers", () => {
    render(<SqlResultViewer result={{ ...base, columns: ["id"], rows: [], row_count: 0 }} />);
    expect(screen.getByText("The query returned no rows.")).toBeInTheDocument();
    expect(screen.getByRole("columnheader", { name: "id" })).toBeInTheDocument();
  });
  it("shows a truncation indicator", () => {
    render(<SqlResultViewer result={{ ...base, columns: ["n"], rows: [[1]], row_count: 1, truncated: true, max_rows: 1 }} />);
    expect(screen.getByRole("status")).toHaveTextContent("first 1 rows");
  });
  it("shows rows affected for statements without a result set", () => {
    render(<SqlResultViewer result={{ columns: [], rows: [], row_count: 0, has_result_set: false, rows_affected: 5, command: "UPDATE", execution_time_ms: 3 }} />);
    expect(screen.getByText("5 rows affected")).toBeInTheDocument();
    expect(screen.getByText("This statement did not return a result set.")).toBeInTheDocument();
    expect(screen.queryByRole("table")).not.toBeInTheDocument();
  });
  it("shows DDL as success without a rows-affected count", () => {
    render(<SqlResultViewer result={{ columns: [], rows: [], row_count: 0, has_result_set: false, command: "CREATE TABLE", execution_time_ms: 4 }} />);
    expect(screen.getByText("CREATE TABLE")).toBeInTheDocument();
    expect(screen.queryByText(/affected/)).not.toBeInTheDocument();
  });
  it("shows failures separately with no success indicator", () => {
    render(<SqlResultViewer error={{ message: 'relation "x" does not exist', code: "query_failed", details: { postgres_code: "42P01", position: 15, execution_time_ms: 2 } }} />);
    expect(screen.getByRole("alert")).toHaveTextContent('relation "x" does not exist');
    expect(screen.getByText("SQLSTATE 42P01")).toBeInTheDocument();
    expect(screen.getByText("failed")).toBeInTheDocument();
    expect(screen.queryByText("succeeded")).not.toBeInTheDocument();
    expect(screen.queryByRole("table")).not.toBeInTheDocument();
  });
});
import { describe, it, expect, vi, beforeEach } from "vitest";
import userEvent from "@testing-library/user-event";
import { render, screen } from "@testing-library/react";

const { call } = vi.hoisted(() => ({ call: vi.fn() }));
vi.mock("../../migration/api.js", () => ({
  apiWithSessionRetry: call,
  databaseBase: (id) => `/api/databases/${id}`,
}));
import { SqlConsole } from "./SqlConsole.jsx";

const rowsResult = { columns: ["id", "name"], column_types: ["int4", "text"], rows: [[1, "Alice"]], row_count: 1, has_result_set: true, truncated: false, execution_time_ms: 5 };

beforeEach(() => {
  call.mockReset();
});

describe("SqlConsole", () => {
  it("posts the SQL to the shared database and renders the result", async () => {
    call.mockResolvedValue(rowsResult);
    const user = userEvent.setup();
    render(<SqlConsole databaseId="db-1" canEdit />);
    await user.type(screen.getByLabelText("SQL statement"), "SELECT 1");
    await user.click(screen.getByRole("button", { name: "Execute" }));
    expect(call).toHaveBeenCalledWith("/api/databases/db-1/query", "POST", { query: "SELECT 1" });
    expect(await screen.findByText("Alice")).toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent("Last run succeeded");
  });
  it("disables execution while running, prevents double submit and clears stale results", async () => {
    let finish;
    call.mockResolvedValueOnce(rowsResult).mockReturnValueOnce(new Promise((r) => (finish = r)));
    const user = userEvent.setup();
    render(<SqlConsole databaseId="db-1" canEdit />);
    await user.type(screen.getByLabelText("SQL statement"), "SELECT 1");
    await user.click(screen.getByRole("button", { name: "Execute" }));
    await screen.findByText("Alice");
    await user.click(screen.getByRole("button", { name: "Execute" }));
    expect(screen.queryByText("Alice")).not.toBeInTheDocument(); // stale result cleared
    const running = screen.getByRole("button", { name: "Running…" });
    expect(running).toBeDisabled();
    await user.click(running);
    expect(call).toHaveBeenCalledTimes(2);
    finish({ ...rowsResult, rows: [[2, "Bob"]] });
    expect(await screen.findByText("Bob")).toBeInTheDocument();
  });
  it("shows backend errors with details", async () => {
    call.mockRejectedValue(Object.assign(new Error("syntax error"), { code: "invalid_sql", details: { execution_time_ms: 1 } }));
    const user = userEvent.setup();
    render(<SqlConsole databaseId="db-1" canEdit />);
    await user.type(screen.getByLabelText("SQL statement"), "SELEC");
    await user.click(screen.getByRole("button", { name: "Execute" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("syntax error");
    expect(screen.getByRole("status")).toHaveTextContent("Last run failed");
    expect(screen.getByText("invalid_sql")).toBeVisible();
    expect(screen.getByText("1 ms")).toBeVisible();
  });
  it("Clear empties the editor and the result", async () => {
    call.mockResolvedValue(rowsResult);
    const user = userEvent.setup();
    render(<SqlConsole databaseId="db-1" canEdit />);
    await user.type(screen.getByLabelText("SQL statement"), "SELECT 1");
    await user.click(screen.getByRole("button", { name: "Execute" }));
    await screen.findByText("Alice");
    await user.click(screen.getByRole("button", { name: "Clear" }));
    expect(screen.getByLabelText("SQL statement")).toHaveValue("");
    expect(screen.queryByText("Alice")).not.toBeInTheDocument();
  });
  it("cannot execute without SQL or without a database", async () => {
    const { rerender } = render(<SqlConsole databaseId="db-1" canEdit />);
    expect(screen.getByRole("button", { name: "Execute" })).toBeDisabled(); // empty editor
    rerender(<SqlConsole databaseId="" canEdit />);
    await userEvent.setup().type(screen.getByLabelText("SQL statement"), "SELECT 1");
    expect(screen.getByRole("button", { name: "Execute" })).toBeDisabled();
    expect(call).not.toHaveBeenCalled();
  });
  it("describes the access level", () => {
    const { rerender } = render(<SqlConsole databaseId="db-1" canEdit={false} />);
    expect(screen.getByText(/View only: only read-only queries/)).toBeInTheDocument();
    rerender(<SqlConsole databaseId="db-1" canEdit />);
    expect(screen.getByText(/NOT recorded as migrations/)).toBeInTheDocument();
  });
  it("has no database selector of its own", () => {
    render(<SqlConsole databaseId="db-1" canEdit />);
    expect(screen.queryByRole("combobox")).not.toBeInTheDocument();
  });
});

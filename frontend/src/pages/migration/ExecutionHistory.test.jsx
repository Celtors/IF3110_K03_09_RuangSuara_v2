import { describe, it, expect, vi, beforeEach } from "vitest";
import userEvent from "@testing-library/user-event";
import { render, screen } from "@testing-library/react";

const { fetchHistory } = vi.hoisted(() => ({ fetchHistory: vi.fn() }));
vi.mock("../../migration/executionHistory.js", async (original) => ({
  ...(await original()),
  fetchExecutionHistory: fetchHistory,
}));
vi.mock("./SubmitRequestDialog.jsx", () => ({
  SubmitRequestDialog: ({ operation, migration }) => <div>dialog {operation} {migration.id}</div>,
}));
import { ExecutionHistory } from "./ExecutionHistory.jsx";
import { latestByMigration, nextOperations } from "../../migration/executionHistory.js";

const entry = (over) => ({
  execution_id: "e1", migration_id: "m1", migration_name: "Add users", migration_version: 1, success_order: 1,
  operation: "apply", status: "succeeded", executor_user_id: "alice", started_at: "2026-11-01T10:00:00Z", ...over,
});
const applied = { id: "m1", state: "applied", sql_down: "DROP TABLE users", irreversible: false };
const none = { items: [] };

beforeEach(() => fetchHistory.mockReset());

describe("nextOperations", () => {
  it("offers rollback only for reversible applied migrations, and roll forward after a rollback", () => {
    expect(nextOperations(applied)).toEqual([["rollback", "Request rollback"]]);
    expect(nextOperations({ ...applied, irreversible: true })).toEqual([]);
    expect(nextOperations({ ...applied, sql_down: " " })).toEqual([]);
    expect(nextOperations({ state: "rolled_back" })).toEqual([["forward", "Request roll forward"]]);
    expect(nextOperations({ state: "draft" })).toEqual([["apply", "Submit for approval"]]);
  });
});

describe("latestByMigration", () => {
  it("keeps the newest execution of each migration", () => {
    const a = entry({ execution_id: "a" });
    const b = entry({ execution_id: "b", started_at: "2026-11-02T10:00:00Z" });
    expect(latestByMigration([a, b]).get("m1")).toBe(b);
  });
});

describe("ExecutionHistory", () => {
  it("offers rollback on the newest row and opens the request dialog", async () => {
    fetchHistory.mockResolvedValue([entry({})]);
    const user = userEvent.setup();
    render(<ExecutionHistory databaseId="db-1" migrations={[applied]} canEdit requests={none} />);
    await user.click(await screen.findByRole("button", { name: "Request rollback" }));
    expect(screen.getByText("dialog rollback m1")).toBeInTheDocument();
  });
  it("hides actions for view-only users and while a request is open", async () => {
    fetchHistory.mockResolvedValue([entry({})]);
    const { rerender } = render(<ExecutionHistory databaseId="db-1" migrations={[applied]} canEdit={false} requests={none} />);
    await screen.findByText("Add users");
    expect(screen.queryByRole("button", { name: /rollback/i })).not.toBeInTheDocument();
    rerender(
      <ExecutionHistory
        databaseId="db-1" migrations={[applied]} canEdit
        requests={{ items: [{ id: "r1", migration_id: "m1", status: "pending_review" }] }}
      />,
    );
    expect(await screen.findByText(/Request open/)).toBeInTheDocument();
  });
  it("refetches with the filter when the toggle changes", async () => {
    fetchHistory.mockResolvedValue([]);
    const user = userEvent.setup();
    render(<ExecutionHistory databaseId="db-1" migrations={[]} canEdit={false} requests={none} />);
    await screen.findByText("No executions yet");
    await user.click(screen.getByLabelText("Hide rolled-back executions"));
    await screen.findByText("Nothing to show");
    expect(fetchHistory).toHaveBeenLastCalledWith("db-1", true);
  });
});

import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { nav, mocks } = vi.hoisted(() => ({
  nav: {
    database: "", id: "", page: "index", base: "", detailPath: "",
    titles: { index: ["Index", "i"], history: ["Migration history", "h"], "query-plan": ["Query plan", "q"] },
  },
  mocks: { api: vi.fn(), session: vi.fn() },
}));
vi.mock("./navigation.js", () => nav);
vi.mock("./api.js", async (importOriginal) => ({
  ...(await importOriginal()),
  api: mocks.api,
  session: mocks.session,
  apiWithSessionRetry: mocks.api,
}));
import { MigrationApp } from "./MigrationApp.jsx";
import { navigate } from "./router.js";

const DBS = [
  { id: "db-1", name: "Dev DB", permission: "ViewEdit" },
  { id: "db-2", name: "Prod DB", permission: "ViewOnly" },
];
const migration = { id: "m1", name: "Add students", description: "", state: "applied", version: 1, last_executor_user_id: "u1", last_executed_at: null };
const queryResult = (value) => ({ columns: ["v"], column_types: ["text"], rows: [[value]], row_count: 1, has_result_set: true, truncated: false, execution_time_ms: 1 });

function setup({ url = "?database=db-1", databases = DBS, listError } = {}) {
  navigate(`/migrations${url}`, { replace: true });
  nav.database = new URLSearchParams(url).get("database") ?? "";
  mocks.session.mockResolvedValue({ csrf_token: "t" });
  mocks.api.mockImplementation(async (path, method = "GET") => {
    if (path === "/api/accessible-databases") {
      if (listError) throw new Error(listError);
      return { databases };
    }
    if (path === "/api/change-requests/pending-approval") return { change_requests: [] };
    if (path.includes("/change-requests")) return { change_requests: [] };
    if (path.endsWith("/migrations")) return { migrations: [migration], can_edit: path.includes("db-1") };
    if (path.endsWith("/query")) return queryResult(`from ${path.split("/")[3]}`);
    if (path.endsWith("/query-plan")) return { plan: [], nodes: [{ depth: 0, node_type: `Plan ${path.split("/")[3]}`, total_cost: 1, estimated_rows: 1 }] };
    throw new Error(`unexpected ${method} ${path}`);
  });
  return render(<MigrationApp />);
}
const dropdown = () => screen.getByRole("combobox", { name: "Database" });
const tab = (name) => screen.getByRole("tab", { name });
const needDatabase = () => screen.getByRole("heading", { name: "Select a database first" }); // role queries skip hidden panels

beforeEach(() => {
  mocks.api.mockReset();
  mocks.session.mockReset();
});

describe("Overview tab", () => {
  it("is the default tab on index.html and loads accessible databases into the dropdown", async () => {
    setup();
    expect(screen.getByRole("tab", { name: "Overview", selected: true })).toBeInTheDocument();
    expect(await screen.findByRole("option", { name: "Dev DB · View + Edit" })).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "Prod DB · View only" })).toBeInTheDocument();
    await waitFor(() => expect(dropdown()).toHaveValue("db-1"));
  });
  it("shows the selected database's permission and keeps the feature cards", async () => {
    setup();
    expect(await screen.findByText("View + Edit", { selector: "b" })).toBeInTheDocument();
    for (const t of ["Review every change", "Run ad-hoc SQL", "Design the next schema", "Inspect before execution"])
      expect(screen.getByRole("heading", { name: t })).toBeInTheDocument();
  });
  it("cards switch tabs in place", async () => {
    setup();
    const user = userEvent.setup();
    await screen.findByRole("option", { name: "Dev DB · View + Edit" });
    await user.click(screen.getByRole("link", { name: /Run ad-hoc SQL/ }));
    expect(tab("SQL Console")).toHaveAttribute("aria-selected", "true");
    expect(window.location.search).toContain("tab=console");
  });
  it("selecting a database updates the shared id, reloads data and the URL", async () => {
    setup();
    const user = userEvent.setup();
    await screen.findByRole("option", { name: "Prod DB · View only" });
    await user.selectOptions(dropdown(), "db-2");
    await waitFor(() => expect(mocks.api).toHaveBeenCalledWith("/api/databases/db-2/migrations"));
    expect(window.location.search).toContain("database=db-2");
  });
  it("handles an empty list", async () => {
    setup({ url: "", databases: [] });
    expect(await screen.findByRole("option", { name: "No accessible databases" })).toBeInTheDocument();
    expect(dropdown()).toBeDisabled();
  });
  it("handles a listing error with retry", async () => {
    setup({ url: "", listError: "boom" });
    const alert = await screen.findByRole("alert");
    expect(within(alert).getByText("boom")).toBeInTheDocument();
    await userEvent.setup().click(within(alert).getByRole("button", { name: "Try again" }));
    await waitFor(() => expect(mocks.api.mock.calls.filter(([p]) => p === "/api/accessible-databases").length).toBeGreaterThan(1));
  });
  it("warns about an inaccessible database and blocks the other tabs", async () => {
    setup({ url: "?database=gone" });
    expect(await screen.findByText(/not accessible to you/)).toBeInTheDocument();
    const user = userEvent.setup();
    for (const name of ["Migration", "SQL Console", "Query Plan"]) {
      await user.click(tab(name));
      expect(needDatabase()).toBeInTheDocument();
    }
    expect(screen.queryByLabelText("SQL statement")).not.toBeInTheDocument();
  });
  it("blocks the other tabs when nothing is selected", async () => {
    setup({ url: "" });
    await screen.findByRole("option", { name: "Dev DB · View + Edit" });
    await userEvent.setup().click(tab("SQL Console"));
    expect(needDatabase()).toBeInTheDocument();
  });
});

describe("Shared database context", () => {
  it("the dropdown exists only in Overview", async () => {
    setup({ url: "?database=db-1" });
    const user = userEvent.setup();
    await screen.findByRole("option", { name: "Dev DB · View + Edit" });
    expect(screen.getAllByRole("combobox")).toHaveLength(1);
    for (const name of ["Migration", "SQL Console", "Query Plan"]) {
      await user.click(tab(name));
      expect(screen.queryByRole("combobox")).not.toBeInTheDocument();
    }
    await user.click(tab("Overview"));
    expect(dropdown()).toHaveValue("db-1");
  });
  it("Migration, SQL Console and Query Plan all use the Overview selection and keep it across tab switches", async () => {
    setup({ url: "?database=db-2" });
    const user = userEvent.setup();
    await screen.findByRole("option", { name: "Prod DB · View only" });

    await user.click(tab("Migration"));
    expect(await screen.findByText("Add students")).toBeInTheDocument();
    expect(mocks.api).toHaveBeenCalledWith("/api/databases/db-2/migrations");

    await user.click(tab("SQL Console"));
    await user.type(screen.getByLabelText("SQL statement"), "SELECT 1");
    await user.click(screen.getByRole("button", { name: "Execute" }));
    expect(await screen.findByText("from db-2")).toBeInTheDocument();
    expect(mocks.api).toHaveBeenCalledWith("/api/databases/db-2/query", "POST", { query: "SELECT 1" });

    await user.click(tab("Query Plan"));
    await user.click(screen.getByRole("button", { name: /Explain query/ }));
    expect(await screen.findByText("Plan db-2")).toBeInTheDocument();

    await user.click(tab("Overview"));
    expect(dropdown()).toHaveValue("db-2");
    await user.click(tab("SQL Console"));
    expect(screen.getByText("from db-2")).toBeInTheDocument();
  });
  it("changing the database in Overview updates every tab and clears the other database's results", async () => {
    setup({ url: "?database=db-2&tab=console" });
    const user = userEvent.setup();
    await screen.findByRole("option", { name: "Prod DB · View only" });
    expect(screen.getByText(/View only: only read-only queries/)).toBeInTheDocument();
    await user.type(screen.getByLabelText("SQL statement"), "SELECT 1");
    await user.click(screen.getByRole("button", { name: "Execute" }));
    await screen.findByText("from db-2");

    await user.click(tab("Overview"));
    await user.selectOptions(dropdown(), "db-1");
    await user.click(tab("SQL Console"));
    expect(screen.queryByText("from db-2")).not.toBeInTheDocument();
    expect(screen.getByText(/NOT recorded as migrations/)).toBeInTheDocument();
    await user.type(screen.getByLabelText("SQL statement"), "SELECT 2");
    await user.click(screen.getByRole("button", { name: "Execute" }));
    expect(await screen.findByText("from db-1")).toBeInTheDocument();
    expect(mocks.api).toHaveBeenCalledWith("/api/databases/db-1/query", "POST", { query: "SELECT 2" });
  });
  it("restores the tab from the URL", async () => {
    setup({ url: "?database=db-1&tab=plan" });
    expect(await screen.findByRole("tab", { name: "Query Plan", selected: true })).toBeInTheDocument();
  });
});

describe("Migration tab (existing functionality)", () => {
  it("keeps history and the edit action for ViewEdit databases", async () => {
    setup({ url: "?database=db-1&tab=migration" });
    expect(await screen.findByText("Add students")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "+ New migration" })).toBeInTheDocument();
  });
  it("hides edit actions for View only databases", async () => {
    setup({ url: "?database=db-2&tab=migration" });
    await screen.findByText("Add students");
    expect(screen.queryByRole("link", { name: "+ New migration" })).not.toBeInTheDocument();
  });
});
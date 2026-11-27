import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const mocks = vi.hoisted(() => ({
  api: vi.fn(),
  session: vi.fn(),
  viewerId: vi.fn(),
}));

vi.mock("./api.js", async (importOriginal) => ({
  ...(await importOriginal()),
  api: mocks.api,
  session: mocks.session,
  apiWithSessionRetry: mocks.api,
  viewerId: mocks.viewerId,
}));

import { MigrationApp } from "./MigrationApp.jsx";
import { navigate, interceptLinks } from "./router.js";
import { JOB_POLL_MS } from "./useJob.js";

const DBS = [
  { id: "db-1", name: "Dev DB", permission: "ViewEdit" },
  { id: "db-2", name: "Prod DB", permission: "ViewOnly" },
];

const MIGRATION = {
  id: "m1",
  database_id: "db-1",
  name: "Add students",
  description: "",
  state: "applied",
  version: 1,
  sql_script: "UPDATE public.students SET score = score + 1 WHERE id = ANY($1::bigint[])",
  sql_down: "UPDATE public.students SET score = score - 1 WHERE id = ANY($1::bigint[])",
  irreversible: false,
  last_executor_user_id: "u1",
  last_executed_at: null,
  attempts: [],
};

const REQUEST = {
  id: "job-1",
  database_id: "db-1",
  migration_id: "m1",
  migration_name: "Add students",
  operation: "apply",
  status: "running",
  requested_by: "u1",
  approvals: [],
  events: [],
  created_at: "2026-11-27T01:00:00Z",
};

const JOB = {
  job_id: "job-1",
  database_id: "db-1",
  migration_id: "m1",
  operation: "apply",
  mode: "batch",
  status: "partial",
  approval_status: "running",
  paused: true,
  committed_batches: 2,
  committed_rows: 200,
  scope_rows: 500,
  batch_attribute: "id",
  batch_size: 100,
  checkpoint: 200,
  last_verified_checkpoint: {
    batch_number: 2,
    cursor: 200,
  },
  available_actions: ["resume", "compensate"],
  compensation_sql_hash: "approved-down-hash",
  created_at: "2026-11-27T01:00:00Z",
};

const queryResult = (value) => ({
  columns: ["v"],
  column_types: ["text"],
  rows: [[value]],
  row_count: 1,
  has_result_set: true,
  truncated: false,
  execution_time_ms: 1,
});

const dropdown = () =>
  screen.getByRole("combobox", { name: "Database" });

const navLink = (name) =>
  within(screen.getByRole("navigation", { name: "Migration navigation" }))
    .getByRole("link", { name, exact: true });

const needDatabase = () =>
  screen.getByRole("heading", { name: "Select a database first" });

const jobPanel = () =>
  within(screen.getByRole("region", { name: "Job status" }));

const fieldValue = (name) =>
  jobPanel().getByText(name, { selector: "dt" }).nextElementSibling;

const jobPath = (database = "db-1", id = "job-1") =>
  `/api/databases/${database}/jobs/${id}`;

const requestPath = (database = "db-1", id = "job-1") =>
  `/api/databases/${database}/change-requests/${id}`;

const jobUrl = (database = "db-1", id = "job-1") =>
  `/migrations/change-request?database=${database}&request=${id}`;

const detailsUrl = (database = "db-1") =>
  `/migrations/details?database=${database}&id=m1`;

function setup({
  url = "?database=db-1",
  databases = DBS,
  listError,
  migration = MIGRATION,
  request = REQUEST,
  job = JOB,
  requests = [],
  pendingApprovals = [],
  viewer = "u1",
  jobError,
  submissionError,
  recoveryError,
  blockers = [],
} = {}) {
  const target = url.startsWith("/") ? url : `/migrations${url}`;
  navigate(target, { replace: true });

  mocks.session.mockResolvedValue({
    id: viewer,
    csrf_token: "t",
  });

  mocks.viewerId.mockReturnValue(viewer);

  let currentRequest = { ...request };
  let currentJob = { ...job };

  mocks.api.mockImplementation(async (path, method = "GET", body) => {
    const [pathname, search = ""] = path.split("?");
    const params = new URLSearchParams(search);
    const databaseId = pathname.split("/")[3];
    const base = `/api/databases/${databaseId}`;

    if (pathname === "/api/accessible-databases") {
      if (listError) throw new Error(listError);
      return { databases };
    }

    if (pathname === "/api/change-requests/pending-approval") {
      return { change_requests: pendingApprovals };
    }

    if (pathname === `${base}/migrations`) {
      return {
        migrations: [
          { ...migration, database_id: databaseId },
        ],
        can_edit: databases.some(
          (d) => d.id === databaseId && d.permission === "ViewEdit",
        ),
      };
    }

    if (pathname === `${base}/migrations/${migration.id}`) {
      return {
        ...migration,
        database_id: databaseId,
      };
    }

    if (pathname === `${base}/migration-history`) {
      return { attempts: [] };
    }

    if (pathname === `${base}/query`) {
      return queryResult(`from ${databaseId}`);
    }

    if (pathname === `${base}/query-plan`) {
      return {
        plan: [],
        nodes: [
          {
            depth: 0,
            node_type: `Plan ${databaseId}`,
            total_cost: 1,
            estimated_rows: 1,
          },
        ],
      };
    }

    if (pathname === `${base}/change-requests` && method === "POST") {
      if (submissionError) throw submissionError;

      currentRequest = {
        ...currentRequest,
        database_id: databaseId,
        operation: body.operation,
        migration_id: body.migration_id,
        status: "pending_review",
        batch: body.mode === "batch"
          ? {
              batch_attribute: body.batch_attribute,
              batch_size: body.batch_size,
            }
          : null,
      };

      currentJob = {
        ...currentJob,
        database_id: databaseId,
        operation: body.operation,
        migration_id: body.migration_id,
        mode: body.mode,
        status: "pending_review",
        approval_status: "pending_review",
        paused: false,
        committed_batches: 0,
        committed_rows: 0,
        checkpoint: null,
        last_verified_checkpoint: null,
        available_actions: [],
        batch_attribute: body.batch_attribute,
        batch_size: body.batch_size,
      };

      return {
        job_id: currentJob.job_id,
        change_request: currentRequest,
      };
    }

    if (pathname === `${base}/change-requests` && method === "GET") {
      const status = params.get("status");
      const migrationId = params.get("migration_id");

      if (status) {
        return {
          change_requests: blockers
            .filter((blocker) => blocker.database_id === databaseId)
            .filter((blocker) => blocker.status === status),
        };
      }

      return {
        change_requests: requests
          .filter((item) => item.database_id === databaseId)
          .filter((item) => !migrationId || item.migration_id === migrationId),
      };
    }

    if (pathname === `${base}/change-requests/${currentRequest.id}`) {
      return {
        change_request: {
          ...currentRequest,
          database_id: databaseId,
        },
      };
    }

    if (
      pathname === `${base}/change-requests/${currentRequest.id}/approve`
      && method === "POST"
    ) {
      currentRequest = {
        ...currentRequest,
        status: "queued",
        approvals: currentRequest.approvals.map((approval) => ({
          ...approval,
          decision: "approved",
        })),
      };

      currentJob = {
        ...currentJob,
        status: "queued",
        approval_status: "queued",
        paused: false,
        available_actions: [],
      };

      return { change_request: currentRequest };
    }

    if (pathname === `${base}/jobs/${currentJob.job_id}`) {
      if (jobError) throw jobError;

      return {
        ...currentJob,
        database_id: databaseId,
      };
    }

    if (
      pathname === `${base}/jobs/${currentJob.job_id}/resume`
      && method === "POST"
    ) {
      if (recoveryError) throw recoveryError;

      currentJob = {
        ...currentJob,
        status: "partial",
        paused: false,
        available_actions: [],
        error: "",
        error_code: "",
      };

      return currentJob;
    }

    if (
      pathname === `${base}/jobs/${currentJob.job_id}/compensate`
      && method === "POST"
    ) {
      if (recoveryError) throw recoveryError;

      currentJob = {
        ...currentJob,
        status: "reverted",
        compensation_status: "reverted",
        paused: false,
        reverted_batches: currentJob.committed_batches,
        reverted_rows: currentJob.committed_rows,
        remaining_committed_rows: 0,
        available_actions: [],
      };

      currentRequest = {
        ...currentRequest,
        status: "failed",
      };

      return currentJob;
    }

    const blocker = blockers.find(
      (item) => pathname === `${base}/jobs/${item.id}`
        && item.database_id === databaseId,
    );

    if (blocker) {
      return {
        job_id: blocker.id,
        database_id: databaseId,
        status: blocker.job_status ?? blocker.status,
        paused: blocker.paused ?? false,
      };
    }

    throw new Error(`Unexpected ${method} ${path}`);
  });

  return {
    ...render(<MigrationApp />),
    setJob: (next) => {
      currentJob = { ...currentJob, ...next };
    },
    setRequest: (next) => {
      currentRequest = { ...currentRequest, ...next };
    },
  };
}

beforeEach(() => {
  mocks.api.mockReset();
  mocks.session.mockReset();
  mocks.viewerId.mockReset();

  vi.spyOn(window, "scrollTo").mockImplementation(() => {});

  HTMLDialogElement.prototype.showModal ??= function () {
    this.open = true;
  };

  HTMLDialogElement.prototype.close ??= function () {
    this.open = false;
  };

  document.addEventListener("click", interceptLinks);
});

afterEach(() => {
  document.removeEventListener("click", interceptLinks);
  vi.restoreAllMocks();
  vi.useRealTimers();
});

describe("Overview navigation", () => {
  it("opens Overview by default and loads accessible databases", async () => {
    setup();

    expect(navLink("Overview")).toHaveAttribute("aria-current", "page");

    expect(
      await screen.findByRole("option", { name: "Dev DB · View + Edit" }),
    ).toBeInTheDocument();

    expect(
      screen.getByRole("option", { name: "Prod DB · View only" }),
    ).toBeInTheDocument();

    expect(dropdown()).toHaveValue("db-1");
  });

  it("shows the selected permission and existing feature cards", async () => {
    setup();

    expect(
      await screen.findByText("View + Edit", { selector: "b" }),
    ).toBeInTheDocument();

    for (const name of [
      "Review every change",
      "Run ad-hoc SQL",
      "Design the next schema",
      "Inspect before execution",
    ]) {
      expect(screen.getByRole("heading", { name })).toBeInTheDocument();
    }
  });

  it("switches workspace views through feature cards", async () => {
    setup();
    const user = userEvent.setup();

    await screen.findByRole("option", { name: "Dev DB · View + Edit" });

    await user.click(
      screen.getByRole("link", { name: /Run ad-hoc SQL/ }),
    );

    expect(navLink("SQL console")).toHaveAttribute("aria-current", "page");
    expect(window.location.search).toContain("tab=console");
    expect(screen.getByLabelText("SQL statement")).toBeInTheDocument();
  });

  it("updates the selected database, migration data and URL", async () => {
    setup();
    const user = userEvent.setup();

    await screen.findByRole("option", { name: "Prod DB · View only" });
    await user.selectOptions(dropdown(), "db-2");

    await waitFor(() => {
      expect(mocks.api).toHaveBeenCalledWith(
        "/api/databases/db-2/migrations",
      );
    });

    expect(window.location.search).toContain("database=db-2");
    expect(dropdown()).toHaveValue("db-2");
  });

  it("handles an empty database list", async () => {
    setup({ url: "", databases: [] });

    expect(
      await screen.findByRole("option", {
        name: "No accessible databases",
      }),
    ).toBeInTheDocument();

    expect(dropdown()).toBeDisabled();
  });

  it("handles database listing errors and allows retry", async () => {
    setup({ url: "", listError: "Unable to load databases" });

    const alert = await screen.findByRole("alert");

    expect(alert).toHaveTextContent("Unable to load databases");

    await userEvent.setup().click(
      within(alert).getByRole("button", { name: "Try again" }),
    );

    await waitFor(() => {
      expect(
        mocks.api.mock.calls.filter(
          ([path]) => path === "/api/accessible-databases",
        ).length,
      ).toBeGreaterThan(1);
    });
  });

  it("blocks database-dependent views for an inaccessible database", async () => {
    setup({ url: "?database=gone" });
    const user = userEvent.setup();

    expect(
      await screen.findByText(/not accessible to you/),
    ).toBeInTheDocument();

    for (const name of ["Migrations", "SQL console", "Query plans"]) {
      await user.click(navLink(name));
      expect(needDatabase()).toBeInTheDocument();
    }

    expect(
      screen.queryByRole("textbox", { name: "SQL statement" }),
    ).not.toBeInTheDocument();
  });

  it("requires a selected database before opening SQL console", async () => {
    setup({ url: "" });

    await screen.findByRole("option", { name: "Dev DB · View + Edit" });

    await userEvent.setup().click(navLink("SQL console"));

    expect(needDatabase()).toBeInTheDocument();
  });
});

describe("Shared database context", () => {
  it("shows the database selector only in Overview", async () => {
    setup();
    const user = userEvent.setup();

    await screen.findByRole("option", { name: "Dev DB · View + Edit" });

    expect(screen.getAllByRole("combobox")).toHaveLength(1);

    for (const name of [
      "Migrations",
      "Approvals",
      "SQL console",
      "Query plans",
    ]) {
      await user.click(navLink(name));
      expect(screen.queryByRole("combobox")).not.toBeInTheDocument();
    }

    await user.click(navLink("Overview"));

    expect(dropdown()).toHaveValue("db-1");
  });

  it("shares the database selection between migrations, SQL console and query plans", async () => {
    setup({ url: "?database=db-2" });
    const user = userEvent.setup();

    await screen.findByRole("option", { name: "Prod DB · View only" });

    await user.click(navLink("Migrations"));

    expect(await screen.findByText("Add students")).toBeInTheDocument();

    expect(mocks.api).toHaveBeenCalledWith(
      "/api/databases/db-2/migrations",
    );

    await user.click(navLink("SQL console"));
    await user.type(screen.getByLabelText("SQL statement"), "SELECT 1");
    await user.click(screen.getByRole("button", { name: "Execute" }));

    expect(await screen.findByText("from db-2")).toBeInTheDocument();

    expect(mocks.api).toHaveBeenCalledWith(
      "/api/databases/db-2/query",
      "POST",
      { query: "SELECT 1" },
    );

    await user.click(navLink("Query plans"));
    await user.click(
      screen.getByRole("button", { name: "Explain query" }),
    );

    expect(await screen.findByText("Plan db-2")).toBeInTheDocument();

    await user.click(navLink("Overview"));
    expect(dropdown()).toHaveValue("db-2");

    await user.click(navLink("SQL console"));
    expect(screen.getByText("from db-2")).toBeInTheDocument();
  });

  it("clears results belonging to the previous database when selection changes", async () => {
    setup({ url: "?database=db-2&tab=console" });
    const user = userEvent.setup();

    expect(
      await screen.findByText(/View only: only read-only queries/),
    ).toBeInTheDocument();

    await user.type(screen.getByLabelText("SQL statement"), "SELECT 1");
    await user.click(screen.getByRole("button", { name: "Execute" }));

    await screen.findByText("from db-2");

    await user.click(navLink("Overview"));
    await user.selectOptions(dropdown(), "db-1");
    await user.click(navLink("SQL console"));

    expect(screen.queryByText("from db-2")).not.toBeInTheDocument();

    expect(
      screen.getByText(/NOT recorded as migrations/),
    ).toBeInTheDocument();

    await user.type(screen.getByLabelText("SQL statement"), "SELECT 2");
    await user.click(screen.getByRole("button", { name: "Execute" }));

    expect(await screen.findByText("from db-1")).toBeInTheDocument();

    expect(mocks.api).toHaveBeenCalledWith(
      "/api/databases/db-1/query",
      "POST",
      { query: "SELECT 2" },
    );
  });

  it("restores the selected workspace view from the URL", async () => {
    setup({ url: "?database=db-1&tab=plan" });

    expect(navLink("Query plans")).toHaveAttribute("aria-current", "page");

    expect(
      await screen.findByRole("button", { name: "Explain query" }),
    ).toBeInTheDocument();
  });

  it("reuses the session across workspace navigation", async () => {
    setup();
    const user = userEvent.setup();

    await screen.findByRole("option", { name: "Dev DB · View + Edit" });

    await user.click(navLink("Migrations"));
    await screen.findByText("Add students");

    await user.click(navLink("SQL console"));
    await user.click(navLink("Query plans"));
    await user.click(navLink("Approvals"));
    await user.click(navLink("Overview"));

    expect(mocks.session).toHaveBeenCalledTimes(1);
  });
});

describe("Migration history and permissions", () => {
  it("keeps migration history and creation actions for ViewEdit databases", async () => {
    setup({ url: "?database=db-1&tab=migration" });

    expect(await screen.findByText("Add students")).toBeInTheDocument();

    expect(
      screen.getByRole("link", { name: "+ New migration" }),
    ).toBeInTheDocument();
  });

  it("hides migration creation actions for ViewOnly databases", async () => {
    setup({ url: "?database=db-2&tab=migration" });

    await screen.findByText("Add students");

    expect(
      screen.queryByRole("link", { name: "+ New migration" }),
    ).not.toBeInTheDocument();
  });

  it("opens the migration details through the SPA router", async () => {
    setup({ url: "?database=db-1&tab=migration" });

    await screen.findByText("Add students");

    await userEvent.setup().click(
      within(screen.getByRole("region", { name: "Migration history" }))
        .getByRole("link", { name: "View →" }),
    );

    expect(
      await screen.findByRole("heading", { name: "Migration details" }),
    ).toBeInTheDocument();

    expect(window.location.pathname).toBe("/migrations/details");
    expect(window.location.search).toContain("id=m1");

    expect(mocks.api).toHaveBeenCalledWith(
      "/api/databases/db-1/migrations/m1",
    );
  });

  it("does not offer execution actions to ViewOnly users", async () => {
    setup({
      url: detailsUrl("db-2"),
      migration: { ...MIGRATION, state: "draft" },
    });

    await screen.findByRole("heading", { name: "Add students" });

    expect(
      screen.queryByRole("button", { name: "Submit for approval" }),
    ).not.toBeInTheDocument();

    expect(
      screen.queryByRole("link", { name: "Edit draft" }),
    ).not.toBeInTheDocument();
  });

  it("disables execution when the migration already has an open request", async () => {
    setup({
      url: detailsUrl(),
      migration: { ...MIGRATION, state: "draft" },
      requests: [REQUEST],
    });

    expect(
      await screen.findByText(/A change request is already open/),
    ).toBeInTheDocument();

    expect(
      screen.getByRole("button", { name: "Submit for approval" }),
    ).toBeDisabled();

    expect(
      screen.getByRole("link", { name: "View request →" }),
    ).toHaveAttribute("href", jobUrl());
  });
});

describe("Asynchronous migration submission", () => {
  it.each([
    ["draft", "apply", "Submit for approval"],
    ["applied", "rollback", "Request rollback"],
    ["rolled_back", "forward", "Request roll forward"],
  ])("submits a %s migration using %s and opens its job", async (
    state,
    operation,
    label,
  ) => {
    setup({
      url: detailsUrl(),
      migration: { ...MIGRATION, state },
    });

    const user = userEvent.setup();

    await user.click(
      await screen.findByRole("button", { name: label }),
    );

    const dialog = within(screen.getByRole("dialog"));

    if (operation !== "apply") {
      expect(
        dialog.queryByLabelText("Execution mode"),
      ).not.toBeInTheDocument();
    }

    await user.click(
      dialog.getByRole("button", { name: label }),
    );

    await screen.findByRole("heading", {
      name: "Required approvals",
    });

    await waitFor(() => {
      const panel = jobPanel();

      expect(
        panel.getByRole("heading", { name: "Execution job" }),
      ).toBeInTheDocument();

      expect(panel.getByText("job-1")).toBeInTheDocument();

      expect(
        panel.getByText("Pending review", { selector: ".badge" }),
      ).toBeInTheDocument();

      expect(window.location.pathname)
        .toBe("/migrations/change-request");

      expect(window.location.search)
        .toContain("request=job-1");

      expect(mocks.api).toHaveBeenCalledWith(
        "/api/databases/db-1/change-requests",
        "POST",
        {
          migration_id: "m1",
          operation,
          mode: "normal",
        },
      );

      expect(mocks.api).toHaveBeenCalledWith(jobPath());
    });
  });

  it("submits batch configuration and opens the returned job", async () => {
    setup({
      url: detailsUrl(),
      migration: { ...MIGRATION, state: "draft" },
    });

    const user = userEvent.setup();

    await user.click(
      await screen.findByRole("button", { name: "Submit for approval" }),
    );

    const dialog = within(screen.getByRole("dialog"));

    expect(dialog.queryByLabelText("Batch attribute")).not.toBeInTheDocument();
    expect(dialog.queryByLabelText("Batch size")).not.toBeInTheDocument();

    await user.selectOptions(dialog.getByLabelText("Execution mode"), "batch");

    await user.clear(dialog.getByLabelText("Batch attribute"));
    await user.type(dialog.getByLabelText("Batch attribute"), "id");

    await user.clear(dialog.getByLabelText("Batch size"));
    await user.type(dialog.getByLabelText("Batch size"), "250");

    await user.click(
      dialog.getByRole("button", { name: "Submit for approval" }),
    );

    await screen.findByRole("heading", { name: "Execution job" });

    expect(mocks.api).toHaveBeenCalledWith(
      "/api/databases/db-1/change-requests",
      "POST",
      {
        migration_id: "m1",
        operation: "apply",
        mode: "batch",
        batch_attribute: "id",
        batch_size: 250,
      },
    );

    expect(jobPanel().getByText("job-1")).toBeInTheDocument();
    expect(fieldValue("Mode")).toHaveTextContent("Batch");
    expect(fieldValue("Batch attribute")).toHaveTextContent("id");
    expect(fieldValue("Batch size")).toHaveTextContent("250");
  });

  it("displays invalid batch configuration returned by the backend", async () => {
    setup({
      url: detailsUrl(),
      migration: { ...MIGRATION, state: "draft" },
      submissionError: Object.assign(
        new Error("Batch requires an identity primary key"),
        { code: "invalid_input" },
      ),
    });

    const user = userEvent.setup();

    await user.click(
      await screen.findByRole("button", { name: "Submit for approval" }),
    );

    const dialog = within(screen.getByRole("dialog"));

    await user.selectOptions(dialog.getByLabelText("Execution mode"), "batch");

    await user.click(
      dialog.getByRole("button", { name: "Submit for approval" }),
    );

    expect(await dialog.findByRole("alert")).toHaveTextContent(
      "Batch requires an identity primary key",
    );

    expect(dialog.getByLabelText("Batch attribute"))
      .toHaveAttribute("aria-invalid", "true");

    expect(window.location.pathname).toBe("/migrations/details");
  });

  it("prevents duplicate execution submissions while a request is pending", async () => {
    setup({
      url: detailsUrl(),
      migration: { ...MIGRATION, state: "draft" },
    });

    const original = mocks.api.getMockImplementation();
    let resolveSubmission;

    mocks.api.mockImplementation((path, method, body) => {
      if (
        path === "/api/databases/db-1/change-requests"
        && method === "POST"
      ) {
        return new Promise((resolve) => {
          resolveSubmission = async () => {
            resolve(await original(path, method, body));
          };
        });
      }

      return original(path, method, body);
    });

    const user = userEvent.setup();

    await user.click(
      await screen.findByRole("button", { name: "Submit for approval" }),
    );

    const dialog = within(screen.getByRole("dialog"));

    await user.click(
      dialog.getByRole("button", { name: "Submit for approval" }),
    );

    const busy = dialog.getByRole("button", { name: "Submitting…" });

    expect(busy).toBeDisabled();

    await user.click(busy);

    const submissions = mocks.api.mock.calls.filter(
      ([path, method]) =>
        path === "/api/databases/db-1/change-requests"
        && method === "POST",
    );

    expect(submissions).toHaveLength(1);

    await act(async () => {
      await resolveSubmission();
    });

    expect(
      await screen.findByRole("heading", { name: "Execution job" }),
    ).toBeInTheDocument();
  });

  it("displays submission permission errors without navigating away", async () => {
    setup({
      url: detailsUrl(),
      migration: { ...MIGRATION, state: "draft" },
      submissionError: Object.assign(
        new Error("Permission denied"),
        { code: "forbidden" },
      ),
    });

    const user = userEvent.setup();

    await user.click(
      await screen.findByRole("button", { name: "Submit for approval" }),
    );

    const dialog = within(screen.getByRole("dialog"));

    await user.click(
      dialog.getByRole("button", { name: "Submit for approval" }),
    );

    expect(await dialog.findByRole("alert")).toHaveTextContent(
      "Permission denied",
    );

    expect(window.location.pathname).toBe("/migrations/details");
    expect(dialog.getByRole("button", { name: "Submit for approval" }))
      .toBeEnabled();
  });
});

describe("Execution job navigation and monitoring", () => {
  it("opens an existing job from migration history", async () => {
    setup({
      url: "?database=db-1&tab=migration",
      requests: [REQUEST],
    });

    const user = userEvent.setup();

    const link = await screen.findByRole("link", { name: "View job →" });

    expect(link).toHaveAttribute("href", jobUrl());

    await user.click(link);

    expect(
      await screen.findByRole("heading", { name: "Execution job" }),
    ).toBeInTheDocument();

    expect(jobPanel().getByText("job-1")).toBeInTheDocument();

    expect(mocks.api).toHaveBeenCalledWith(jobPath());
  });

  it.each([
    ["queued", "In queue", false],
    ["running", "Running", false],
    ["completed", "Completed", false],
    ["failed", "Failed", false],
    ["partial", "Partial", true],
    ["unresolved", "Unresolved (awaiting recovery)", true],
  ])("renders the %s job state and verified progress", async (
    status,
    label,
    paused,
  ) => {
    setup({
      url: jobUrl(),
      job: {
        ...JOB,
        status,
        paused,
        available_actions: [],
      },
    });

    await screen.findByRole("heading", {
      name: "Required approvals",
    });

    await waitFor(() => {
      expect(
        jobPanel().getByText(label, { selector: ".badge" }),
      ).toBeInTheDocument();

      expect(jobPanel().getByText("job-1")).toBeInTheDocument();
      expect(fieldValue("Target database")).toHaveTextContent("db-1");
      expect(fieldValue("Migration")).toHaveTextContent("m1");
      expect(fieldValue("Committed batches")).toHaveTextContent("2");
      expect(fieldValue("Committed rows")).toHaveTextContent("200");
      expect(fieldValue("Last verified cursor")).toHaveTextContent("200");
    });
  });

  it("displays execution errors and explains unresolved jobs", async () => {
    setup({
      url: jobUrl(),
      job: {
        ...JOB,
        status: "unresolved",
        paused: true,
        available_actions: [],
        error: "Connection lost during commit",
        error_code: "indeterminate",
      },
    });

    await screen.findByRole("heading", {
      name: "Required approvals",
    });

    await waitFor(() => {
      expect(jobPanel().getByRole("alert")).toHaveTextContent(
        "indeterminate: Connection lost during commit",
      );

      expect(
        jobPanel().getByText(
          /Later migrations on this database are held/,
        ),
      ).toBeInTheDocument();

      expect(
        jobPanel().queryByRole("button", { name: "Resume" }),
      ).not.toBeInTheDocument();
    });
  });

  it("handles unavailable job status without crashing the application", async () => {
    setup({
      url: jobUrl(),
      jobError: Object.assign(
        new Error("Job not found"),
        { code: "not_found" },
      ),
    });

    await screen.findByRole("heading", {
      name: "Required approvals",
    });

    await waitFor(() => {
      const panel = jobPanel();

      expect(panel.getByRole("alert"))
        .toHaveTextContent("Job not found");

      expect(
        panel.getByText(/Job status is not available/),
      ).toBeInTheDocument();

      expect(
        panel.getByRole("button", { name: "Refresh status" }),
      ).toBeEnabled();

      expect(
        panel.queryByText("Loading job status…"),
      ).not.toBeInTheDocument();
    });
  });

  it("refreshes verified progress on demand", async () => {
    const { setJob } = setup({
      url: jobUrl(),
    });

    const user = userEvent.setup();

    await screen.findByRole("heading", { name: "Execution job" });

    await waitFor(() => {
      expect(fieldValue("Committed rows")).toHaveTextContent("200");
    });

    setJob({
      committed_batches: 3,
      committed_rows: 300,
      checkpoint: 300,
      last_verified_checkpoint: {
        batch_number: 3,
        cursor: 300,
      },
    });

    await user.click(
      jobPanel().getByRole("button", { name: "Refresh status" }),
    );

    await waitFor(() => {
      expect(fieldValue("Committed rows")).toHaveTextContent("300");
      expect(fieldValue("Committed batches")).toHaveTextContent("3");
      expect(fieldValue("Last verified cursor")).toHaveTextContent("300");
    });
  });

  it("keeps active partial execution distinct from a paused partial job", async () => {
    setup({
      url: jobUrl(),
      job: {
        ...JOB,
        status: "partial",
        paused: false,
        available_actions: ["resume", "compensate"],
      },
    });
    await screen.findByRole("heading", {
      name: "Required approvals",
    });
    await waitFor(() => {
      const panel = jobPanel();

      expect(
        panel.getByText("Partial", { selector: ".badge" }),
      ).toBeInTheDocument();

      expect(
        panel.getByText(/Execution is still processing batches/),
      ).toBeInTheDocument();

      expect(
        panel.getByText("Updating automatically"),
      ).toBeInTheDocument();

      expect(
        panel.queryByRole("button", { name: "Resume" }),
      ).not.toBeInTheDocument();

      expect(
        panel.queryByRole("button", { name: /Compensate/ }),
      ).not.toBeInTheDocument();
    });
  });
});

describe("Execution polling lifecycle", () => {
  const flush = () => act(async () => {});

  const advance = (ms = JOB_POLL_MS) =>
    act(async () => {
      await vi.advanceTimersByTimeAsync(ms);
    });

  const jobReads = () =>
    mocks.api.mock.calls.filter(
      ([path, method]) =>
        path === jobPath() && (!method || method === "GET"),
    ).length;

  it("polls an active job until it reaches a completed state", async () => {
    vi.useFakeTimers();

    const { setJob } = setup({
      url: jobUrl(),
      job: {
        ...JOB,
        status: "running",
        paused: false,
        available_actions: [],
      },
    });

    await flush();

    expect(jobReads()).toBe(1);

    setJob({
      status: "partial",
      paused: false,
      committed_batches: 3,
      committed_rows: 300,
    });

    await advance();

    expect(jobReads()).toBe(2);
    expect(fieldValue("Committed rows")).toHaveTextContent("300");

    setJob({
      status: "completed",
      approval_status: "succeeded",
      committed_batches: 5,
      committed_rows: 500,
      paused: false,
    });

    await advance();

    expect(jobPanel().getByText("Completed", {
      selector: ".badge",
    })).toBeInTheDocument();

    const completedReads = jobReads();

    await advance(JOB_POLL_MS * 3);

    expect(jobReads()).toBe(completedReads);
  });

  it.each([
    { status: "completed", paused: false },
    { status: "failed", paused: false },
    { status: "partial", paused: true },
    { status: "unresolved", paused: true },
    { status: "reverted", paused: false },
    {
      status: "partial",
      paused: true,
      compensation_status: "partially_reverted",
    },
  ])("stops polling waiting or terminal jobs: %j", async (state) => {
    vi.useFakeTimers();

    setup({
      url: jobUrl(),
      job: {
        ...JOB,
        ...state,
        available_actions: [],
      },
    });

    await flush();

    expect(jobReads()).toBe(1);

    await advance(JOB_POLL_MS * 3);

    expect(jobReads()).toBe(1);
  });

  it("cleans up polling when navigating away from the job page", async () => {
    vi.useFakeTimers();

    setup({
      url: jobUrl(),
      job: {
        ...JOB,
        status: "running",
        paused: false,
        available_actions: [],
      },
    });

    await flush();

    expect(jobReads()).toBe(1);

    await act(async () => {
      navigate("/migrations?database=db-1&tab=overview");
    });

    expect(
      screen.queryByRole("heading", { name: "Execution job" }),
    ).not.toBeInTheDocument();

    await advance(JOB_POLL_MS * 3);

    expect(jobReads()).toBe(1);
  });

  it("cleans up polling when MigrationApp unmounts", async () => {
    vi.useFakeTimers();

    const { unmount } = setup({
      url: jobUrl(),
      job: {
        ...JOB,
        status: "running",
        paused: false,
        available_actions: [],
      },
    });

    await flush();

    expect(jobReads()).toBe(1);

    unmount();

    await advance(JOB_POLL_MS * 3);

    expect(jobReads()).toBe(1);
  });
});

describe("Approval workflow", () => {
  it("opens the approvals inbox without requiring a database selection", async () => {
    setup({
      url: "?tab=approvals",
      pendingApprovals: [],
    });

    expect(navLink("Approvals")).toHaveAttribute("aria-current", "page");

    expect(
      await screen.findByText("Nothing is waiting for you"),
    ).toBeInTheDocument();
  });

  it("opens a pending approval and submits the leader's decision", async () => {
    const pendingRequest = {
      ...REQUEST,
      status: "pending_review",
      requested_by: "u2",
      approvals: [
        {
          id: "approval-1",
          group_id: "group-1",
          group_name: "Engineering",
          required_leader_id: "u1",
          decision: "pending",
        },
      ],
    };

    setup({
      url: "?tab=approvals",
      pendingApprovals: [pendingRequest],
      request: pendingRequest,
      job: {
        ...JOB,
        status: "pending_review",
        approval_status: "pending_review",
        paused: false,
        available_actions: [],
      },
    });

    const user = userEvent.setup();

    await user.click(
      await screen.findByRole("link", { name: "Review →" }),
    );

    expect(
      await screen.findByText("Your decision is needed"),
    ).toBeInTheDocument();

    await user.click(
      screen.getByRole("button", { name: "Approve…" }),
    );

    const dialog = within(screen.getByRole("dialog"));

    await user.type(
      dialog.getByLabelText("Comment (optional)"),
      "Reviewed SQL",
    );

    await user.click(
      dialog.getByRole("button", { name: "Approve" }),
    );

    await waitFor(() => {
      expect(mocks.api).toHaveBeenCalledWith(
        `${requestPath()}/approve`,
        "POST",
        { comment: "Reviewed SQL" },
      );
    });

    expect(
      await jobPanel().findByText("In queue", { selector: ".badge" }),
    ).toBeInTheDocument();
  });

  it("does not show approval decisions to an unrelated user", async () => {
    setup({
      url: jobUrl(),
      viewer: "unrelated-user",
      request: {
        ...REQUEST,
        status: "pending_review",
        approvals: [
          {
            id: "approval-1",
            group_id: "group-1",
            group_name: "Engineering",
            required_leader_id: "leader-1",
            decision: "pending",
          },
        ],
      },
      job: {
        ...JOB,
        status: "pending_review",
        approval_status: "pending_review",
        paused: false,
        available_actions: [],
      },
    });

    await screen.findByRole("heading", { name: "Required approvals" });

    expect(
      screen.queryByRole("button", { name: "Approve…" }),
    ).not.toBeInTheDocument();

    expect(
      screen.queryByRole("button", { name: "Reject…" }),
    ).not.toBeInTheDocument();
  });
});

describe("Migration recovery and compensation", () => {
  it("shows recovery actions to the authorized requester", async () => {
    setup({ url: jobUrl() });

    expect(
      await screen.findByRole("button", { name: "Resume" }),
    ).toBeInTheDocument();

    expect(
      screen.getByRole("button", {
        name: "Compensate (revert committed batches)",
      }),
    ).toBeInTheDocument();
  });

  it.each([
    { database: "db-2", viewer: "u1" },
    { database: "db-1", viewer: "another-user" },
  ])("hides recovery actions without permission: %j", async ({
    database,
    viewer,
  }) => {
    setup({
      url: jobUrl(database),
      viewer,
    });

    await screen.findByRole("heading", { name: "Required approvals" });

    expect(
      jobPanel().queryByRole("button", { name: "Resume" }),
    ).not.toBeInTheDocument();

    expect(
      jobPanel().queryByRole("button", { name: /Compensate/ }),
    ).not.toBeInTheDocument();
  });

  it("does not expose recovery actions absent from available_actions", async () => {
    setup({
      url: jobUrl(),
      job: {
        ...JOB,
        available_actions: [],
      },
    });

    await screen.findByRole("heading", { name: "Required approvals" });

    expect(
      jobPanel().queryByRole("button", { name: "Resume" }),
    ).not.toBeInTheDocument();

    expect(
      jobPanel().queryByRole("button", { name: /Compensate/ }),
    ).not.toBeInTheDocument();
  });

  it("resumes a migration from its last verified checkpoint", async () => {
    setup({ url: jobUrl() });
    const user = userEvent.setup();

    await user.click(
      await screen.findByRole("button", { name: "Resume" }),
    );

    const dialog = within(screen.getByRole("dialog"));

    expect(
      dialog.getByText(/processing only uncommitted batches/),
    ).toBeInTheDocument();

    await user.click(dialog.getByRole("button", { name: "Resume" }));

    await waitFor(() => {
      expect(mocks.api).toHaveBeenCalledWith(
        `${jobPath()}/resume`,
        "POST",
      );
    });

    expect(
      await jobPanel().findByText(/Execution is still processing batches/),
    ).toBeInTheDocument();

    expect(fieldValue("Committed batches")).toHaveTextContent("2");
    expect(fieldValue("Committed rows")).toHaveTextContent("200");
  });

  it("requires confirmation before compensating committed batches", async () => {
    setup({ url: jobUrl() });
    const user = userEvent.setup();

    await user.click(
      await screen.findByRole("button", {
        name: "Compensate (revert committed batches)",
      }),
    );

    const dialog = within(screen.getByRole("dialog"));

    expect(
      dialog.getByText(/reverse order, one transaction per batch/),
    ).toBeInTheDocument();

    expect(mocks.api).not.toHaveBeenCalledWith(
      `${jobPath()}/compensate`,
      "POST",
    );

    await user.click(
      dialog.getByRole("button", { name: "Compensate" }),
    );

    await waitFor(() => {
      expect(mocks.api).toHaveBeenCalledWith(
        `${jobPath()}/compensate`,
        "POST",
      );
    });

    await waitFor(() => {
      expect(fieldValue("Reverted batches")).toHaveTextContent("2");
      expect(fieldValue("Reverted rows")).toHaveTextContent("200");
      expect(fieldValue("Remaining committed rows")).toHaveTextContent("0");
    });

    expect(
      jobPanel().getAllByText("Reverted", { selector: ".badge" }),
    ).toHaveLength(2);
  });

  it("displays unsafe compensation rejection and corrective migration guidance", async () => {
    setup({
      url: jobUrl(),
      recoveryError: Object.assign(
        new Error("Approved down SQL is unavailable"),
        { code: "conflict" },
      ),
    });

    const user = userEvent.setup();

    await user.click(
      await screen.findByRole("button", {
        name: "Compensate (revert committed batches)",
      }),
    );

    const dialog = within(screen.getByRole("dialog"));

    await user.click(
      dialog.getByRole("button", { name: "Compensate" }),
    );

    const alert = await dialog.findByRole("alert");

    expect(alert).toHaveTextContent("Approved down SQL is unavailable");

    expect(alert).toHaveTextContent(
      "A new corrective migration is required",
    );
  });

  it("offers continuation for interrupted compensation", async () => {
    setup({
      url: jobUrl(),
      job: {
        ...JOB,
        status: "partial",
        paused: true,
        compensation_status: "partially_reverted",
        reverted_batches: 1,
        reverted_rows: 100,
        remaining_committed_rows: 100,
        available_actions: ["resume"],
      },
    });

    const user = userEvent.setup();

    expect(
      await screen.findByText("Partially reverted", { selector: ".badge" }),
    ).toBeInTheDocument();

    expect(fieldValue("Reverted batches")).toHaveTextContent("1");
    expect(fieldValue("Reverted rows")).toHaveTextContent("100");
    expect(fieldValue("Remaining committed rows")).toHaveTextContent("100");

    await user.click(
      screen.getByRole("button", { name: "Continue compensation" }),
    );

    const dialog = within(screen.getByRole("dialog"));

    await user.click(
      dialog.getByRole("button", { name: "Continue compensation" }),
    );

    await waitFor(() => {
      expect(mocks.api).toHaveBeenCalledWith(
        `${jobPath()}/resume`,
        "POST",
      );
    });
  });
});

describe("Held migrations and database isolation", () => {
  const blocker = {
    id: "blocking-job",
    database_id: "db-1",
    migration_id: "m0",
    migration_name: "Earlier migration",
    status: "running",
    job_status: "partial",
    paused: true,
  };

  it("displays a possible blocking job when submission is rejected", async () => {
    setup({
      url: detailsUrl(),
      migration: { ...MIGRATION, state: "draft" },
      submissionError: Object.assign(
        new Error("Database is held by another migration"),
        { code: "conflict" },
      ),
      blockers: [blocker],
    });

    const user = userEvent.setup();

    await user.click(
      await screen.findByRole("button", { name: "Submit for approval" }),
    );

    const dialog = within(screen.getByRole("dialog"));

    await user.click(
      dialog.getByRole("button", { name: "Submit for approval" }),
    );

    expect(await dialog.findByRole("alert")).toHaveTextContent(
      "Database is held by another migration",
    );

    const link = await dialog.findByRole("link", {
      name: /blocking-job/,
    });

    expect(link).toHaveAttribute(
      "href",
      jobUrl("db-1", "blocking-job"),
    );

    expect(
      dialog.getByText(/Possible blocking jobs on this database/),
    ).toBeInTheDocument();
  });

  it("displays possible blockers for an accepted but queued job", async () => {
    setup({
      url: jobUrl(),
      request: {
        ...REQUEST,
        status: "queued",
      },
      job: {
        ...JOB,
        status: "queued",
        paused: false,
        available_actions: [],
      },
      blockers: [blocker],
    });

    await screen.findByRole("heading", { name: "Execution job" });

    await waitFor(() => {
      const link = jobPanel().getByRole("link", {
        name: /blocking-job/,
      });

      expect(link).toHaveAttribute(
        "href",
        jobUrl("db-1", "blocking-job"),
      );
    });

    await waitFor(() => {
      expect(
        jobPanel().getByText("Partial", { selector: ".badge" }),
      ).toBeInTheDocument();

      expect(jobPanel().getByText("(paused)")).toBeInTheDocument();
    });

  });

  it("does not display blockers belonging to an unrelated database", async () => {
    setup({
      url: jobUrl("db-2"),
      request: {
        ...REQUEST,
        status: "queued",
      },
      job: {
        ...JOB,
        status: "queued",
        paused: false,
        available_actions: [],
      },
      blockers: [blocker],
    });

    await screen.findByRole("heading", { name: "Execution job" });

    await waitFor(() => {
      expect(mocks.api).toHaveBeenCalledWith(
        "/api/databases/db-2/change-requests?status=running&limit=200",
      );

      expect(mocks.api).toHaveBeenCalledWith(
        "/api/databases/db-2/change-requests?status=unknown&limit=200",
      );
    });

    expect(
      jobPanel().queryByRole("link", { name: /blocking-job/ }),
    ).not.toBeInTheDocument();

    expect(mocks.api).not.toHaveBeenCalledWith(
      "/api/databases/db-1/jobs/blocking-job",
    );
  });
});
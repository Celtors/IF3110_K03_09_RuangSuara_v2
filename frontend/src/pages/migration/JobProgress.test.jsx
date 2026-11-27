import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { JobProgress } from "./JobProgress.jsx";

const { api } = vi.hoisted(() => ({ api: vi.fn() }));
vi.mock("../../migration/api.js", async (original) => ({ ...await original(), api, apiWithSessionRetry: api }));
const job = (over = {}) => ({ job_id: "job-123", database_id: "db1", migration_id: "m1", mode: "batch", operation: "apply", status: "partial", paused: true, committed_batches: 2, committed_rows: 200, scope_rows: 500, batch_size: 100, batch_attribute: "id", last_verified_checkpoint: { batch_number: 2, cursor: 200 }, available_actions: ["resume", "compensate"], compensation_sql_hash: "hash", ...over });
const renderJob = (over = {}, props = {}) => {
  const reload = vi.fn();
  render(<JobProgress databaseId="db1" jobId="job-123" resource={{ job: job(over), error: "", loading: false, reload }} canEdit isRequester {...props} />);
  return reload;
};
beforeEach(() => {
  api.mockReset().mockResolvedValue({ change_requests: [] });
  HTMLDialogElement.prototype.showModal ??= function () { this.open = true; };
  HTMLDialogElement.prototype.close ??= function () { this.open = false; };
});

describe("job progress", () => {
  it.each([["queued", "In queue"], ["running", "Running"], ["completed", "Completed"], ["failed", "Failed"], ["partial", "Partial"], ["unresolved", "Unresolved (awaiting recovery)"]])("renders %s and verified counts", (status, label) => {
    renderJob({ status, paused: status === "partial", available_actions: [] });
    expect(screen.getByText(label, { selector: ".badge" })).toBeInTheDocument();
    expect(screen.getByText("job-123")).toBeInTheDocument();
    expect(screen.getByText("Committed batches").nextElementSibling).toHaveTextContent("2");
    expect(screen.getByText("Committed rows", { selector: "dt" }).nextElementSibling).toHaveTextContent("200");
    expect(screen.getByText("Last verified cursor").nextElementSibling).toHaveTextContent("200");
  });
  it("shows error and explains held unresolved execution", () => {
    renderJob({ status: "unresolved", error: "Connection lost", error_code: "indeterminate", available_actions: [] });
    expect(screen.getByRole("alert")).toHaveTextContent("indeterminate: Connection lost");
    expect(screen.getByText(/Later migrations on this database are held/)).toBeInTheDocument();
  });
  it.each([{ canEdit: false }, { isRequester: false }])("hides actions without permission (%j)", (props) => {
    renderJob({}, props);
    expect(screen.queryByRole("button", { name: "Resume" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Compensate/ })).not.toBeInTheDocument();
  });
  it("does not infer actions for an active partial job or absent available actions", () => {
    renderJob({ paused: false, available_actions: [] });
    expect(screen.queryByRole("button", { name: "Resume" })).not.toBeInTheDocument();
    expect(screen.getByText(/still processing batches/)).toBeInTheDocument();
  });
  it("resumes only the verified job and refreshes after acceptance", async () => {
    const reload = renderJob({ available_actions: ["resume"] });
    expect(screen.queryByRole("button", { name: /Compensate/ })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Resume" }));
    fireEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Resume" }));
    await waitFor(() => expect(reload).toHaveBeenCalled());
    expect(api).toHaveBeenCalledWith("/api/databases/db1/jobs/job-123/resume", "POST");
  });
  it("confirms reverse-order compensation and displays unsafe rejection", async () => {
    api.mockRejectedValueOnce(new Error("Approved down SQL is unavailable; the job remains reserved"));
    renderJob();
    fireEvent.click(screen.getByRole("button", { name: /Compensate \(revert/ }));
    const dialog = screen.getByRole("dialog");
    expect(within(dialog).getByText(/reverse order, one transaction per batch/)).toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole("button", { name: "Compensate" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent("the job remains reserved");
    expect(api).toHaveBeenCalledWith("/api/databases/db1/jobs/job-123/compensate", "POST");
  });
  it("renders partially reverted and allows continuation via resume", async () => {
    renderJob({ compensation_status: "partially_reverted", reverted_batches: 1, reverted_rows: 100, remaining_committed_rows: 100 });
    expect(screen.getByText("Partially reverted")).toBeInTheDocument();
    expect(screen.getByText("Reverted batches").nextElementSibling).toHaveTextContent("1");
    fireEvent.click(screen.getByRole("button", { name: "Continue compensation" }));
    fireEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Continue compensation" }));
    await waitFor(() => expect(api).toHaveBeenCalledWith("/api/databases/db1/jobs/job-123/resume", "POST"));
  });
  it("shows blockers for accepted but queued jobs", async () => {
    api.mockResolvedValueOnce({ change_requests: [{ id: "earlier", status: "running", migration_name: "Earlier job" }] }).mockResolvedValueOnce({ change_requests: [] }).mockResolvedValueOnce({ status: "partial", paused: true });
    renderJob({ status: "queued", paused: false });
    expect(await screen.findByRole("link", { name: /earlier/ })).toHaveAttribute("href", "/migrations/change-request?database=db1&request=earlier");
    expect(screen.getByText("Partial", { selector: ".badge" })).toBeInTheDocument();
    expect(screen.getByText("(paused)")).toBeInTheDocument();
  });
});

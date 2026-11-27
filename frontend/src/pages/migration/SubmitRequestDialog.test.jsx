import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { SubmitRequestDialog } from "./SubmitRequestDialog.jsx";

const { api, navigate } = vi.hoisted(() => ({ api: vi.fn(), navigate: vi.fn() }));
vi.mock("../../migration/api.js", async (original) => ({ ...await original(), api, apiWithSessionRetry: api }));
vi.mock("../../migration/router.js", () => ({ navigate }));

const migration = { id: "m1", name: "Items", sql_script: "UPDATE public.items SET score = score + 1 WHERE id = ANY($1::bigint[])", sql_down: "DOWN" };
const submit = () => screen.getByRole("button", { name: /Submit for approval|Request rollback|Request roll forward/ });
const setup = (operation = "apply") => render(<SubmitRequestDialog databaseId="db1" migration={migration} operation={operation} onClose={vi.fn()} />);
const batch = () => fireEvent.change(screen.getByLabelText("Execution mode"), { target: { value: "batch" } });

beforeEach(() => {
  HTMLDialogElement.prototype.showModal ??= function () { this.open = true; };
  HTMLDialogElement.prototype.close ??= function () { this.open = false; };
  navigate.mockReset();
  api.mockReset().mockResolvedValue({ job_id: "job-123", change_request: { id: "request-123" } });
});

describe("asynchronous submission", () => {
  it("submits a linked correction as a normal apply", async () => {
    render(<SubmitRequestDialog databaseId="db1" migration={migration} operation="apply"
      correctsBatchId="partial-job" onClose={vi.fn()} />);
    expect(screen.queryByLabelText("Execution mode")).not.toBeInTheDocument();
    fireEvent.click(submit());
    await waitFor(() => expect(api).toHaveBeenCalledWith("/api/databases/db1/change-requests", "POST", {
      migration_id: "m1", operation: "apply", mode: "normal", corrects_batch_id: "partial-job",
    }));
  });
  it.each(["apply", "rollback", "forward"])("submits %s and navigates to the returned job", async (operation) => {
    setup(operation);
    if (operation !== "apply") expect(screen.queryByLabelText("Execution mode")).not.toBeInTheDocument();
    fireEvent.click(submit());
    await waitFor(() => expect(navigate).toHaveBeenCalledWith("/migrations/change-request?database=db1&request=job-123"));
    expect(api).toHaveBeenCalledWith("/api/databases/db1/change-requests", "POST", { migration_id: "m1", operation, mode: "normal" });
  });
  it("shows batch inputs only in batch mode and submits the configured batch", async () => {
    setup();
    expect(screen.queryByLabelText("Batch attribute")).not.toBeInTheDocument();
    batch();
    fireEvent.change(screen.getByLabelText("Batch attribute"), { target: { value: "item_id" } });
    fireEvent.change(screen.getByLabelText("Batch size"), { target: { value: "250" } });
    fireEvent.click(submit());
    await waitFor(() => expect(navigate).toHaveBeenCalled());
    expect(api).toHaveBeenCalledWith("/api/databases/db1/change-requests", "POST", { migration_id: "m1", operation: "apply", mode: "batch", batch_attribute: "item_id", batch_size: 250 });
  });
  it("validates batch inputs and shows server validation next to the fields", async () => {
    api.mockRejectedValue(Object.assign(new Error("Batch requires an identity primary key"), { code: "invalid_input" }));
    setup(); batch();
    fireEvent.change(screen.getByLabelText("Batch size"), { target: { value: "0" } });
    fireEvent.click(submit());
    expect(screen.getByText(/Enter a whole number/)).toBeInTheDocument();
    expect(api).not.toHaveBeenCalled();
    fireEvent.change(screen.getByLabelText("Batch size"), { target: { value: "50" } });
    fireEvent.click(submit());
    expect(await screen.findByText("Batch requires an identity primary key")).toBeInTheDocument();
    expect(screen.getByLabelText("Batch attribute")).toHaveAttribute("aria-invalid", "true");
  });
  it("prevents duplicate in-flight submissions", async () => {
    let resolve;
    api.mockReturnValue(new Promise((r) => { resolve = r; }));
    setup(); fireEvent.click(submit());
    const busy = screen.getByRole("button", { name: "Submitting…" });
    expect(busy).toBeDisabled();
    fireEvent.click(busy);
    expect(api).toHaveBeenCalledTimes(1);
    resolve({ job_id: "job-123", change_request: {} });
    await waitFor(() => expect(navigate).toHaveBeenCalled());
  });
  it("shows permission errors without navigating", async () => {
    api.mockRejectedValue(Object.assign(new Error("Permission denied"), { code: "forbidden" }));
    setup(); fireEvent.click(submit());
    expect(await screen.findByRole("alert")).toHaveTextContent("Permission denied");
    expect(navigate).not.toHaveBeenCalled();
  });
  it("shows same-database blocker candidates on a conflict", async () => {
    api.mockRejectedValueOnce(Object.assign(new Error("Database held"), { code: "conflict" }))
      .mockResolvedValueOnce({ change_requests: [{ id: "blocked-job", status: "running", migration_name: "Earlier migration" }] })
      .mockResolvedValueOnce({ change_requests: [] });
    setup(); fireEvent.click(submit());
    const link = await screen.findByRole("link", { name: /blocked-job/ });
    expect(link).toHaveAttribute("href", "/migrations/change-request?database=db1&request=blocked-job");
    expect(screen.getByRole("alert")).toHaveTextContent("Database held");
  });
});

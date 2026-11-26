import { describe, it, expect } from "vitest";
import { approvalProgress, isOpen, shortId, sha256Hex, sqlFor, trackFor, viewerRole } from "./changeRequests.js";

const approval = (leader, decision = "pending") => ({ required_leader_id: leader, decision, group_name: leader });
const request = (over = {}) => ({
  id: "r1", status: "pending_review", operation: "apply", requested_by: "carol",
  approvals: [approval("alice"), approval("bob")], ...over,
});

describe("viewerRole", () => {
  it("lets a leader with a pending approval decide", () => {
    expect(viewerRole(request(), "alice").canDecide).toBe(true);
  });
  it("does not let a leader decide twice or after the request left review", () => {
    const decided = request({ approvals: [approval("alice", "approved"), approval("bob")] });
    expect(viewerRole(decided, "alice").canDecide).toBe(false);
    expect(viewerRole(request({ status: "queued" }), "alice").canDecide).toBe(false);
  });
  it("lets only the requester cancel, and only before it runs", () => {
    expect(viewerRole(request(), "carol").canCancel).toBe(true);
    expect(viewerRole(request(), "alice").canCancel).toBe(false);
    expect(viewerRole(request({ status: "running" }), "carol").canCancel).toBe(false);
    expect(viewerRole(request(), "").canCancel).toBe(false);
  });
});

describe("trackFor", () => {
  const states = (r) => trackFor(r).map((s) => s.state);
  it("marks Draft done and Pending review current while in review", () => {
    expect(states(request())).toEqual(["done", "current", "todo", "todo", "todo", "todo"]);
  });
  it("completes every stage on success", () => {
    expect(states(request({ status: "succeeded" }))).toEqual(["done", "done", "done", "done", "done", "done"]);
    expect(trackFor(request({ status: "succeeded" })).at(-1).label).toBe("Success");
  });
  it("marks failure and rejection as bad at the right stage", () => {
    expect(trackFor(request({ status: "failed" })).at(-1)).toEqual({ label: "Failed", state: "bad" });
    expect(trackFor(request({ status: "rejected" }))[1]).toEqual({ label: "Rejected", state: "bad" });
  });
  it("places a cancellation where the request had got to", () => {
    expect(trackFor(request({ status: "cancelled" }))[1].state).toBe("bad");
    expect(trackFor(request({ status: "cancelled", queued_at: "t" }))[3].state).toBe("bad");
  });
});

describe("helpers", () => {
  it("counts approvals", () => {
    expect(approvalProgress(request({ approvals: [approval("a", "approved"), approval("b")] }))).toEqual({ approved: 1, total: 2 });
  });
  it("treats unfinished statuses as open", () => {
    expect(isOpen("pending_review")).toBe(true);
    expect(isOpen("succeeded")).toBe(false);
  });
  it("picks sql_down for rollback and sql_script otherwise", () => {
    const m = { sql_script: "UP", sql_down: "DOWN" };
    expect(sqlFor(m, request({ operation: "rollback" }))).toBe("DOWN");
    expect(sqlFor(m, request({ operation: "forward" }))).toBe("UP");
    expect(sqlFor(null, request())).toBe("");
  });
  it("shortens long ids only", () => {
    expect(shortId("11111111-1111-4111-8111-000000000002")).toBe("11111111…");
    expect(shortId("abc")).toBe("abc");
    expect(shortId("")).toBe("—");
  });
  it("hashes like the server (SHA-256 hex)", async () => {
    expect(await sha256Hex("abc")).toBe("ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad");
  });
});

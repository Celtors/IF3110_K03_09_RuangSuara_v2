import { api, apiWithSessionRetry, databaseBase, viewerId } from "./api.js";

// Change requests: the approval workflow in front of every migration execution.
//
//   Draft -> Pending review -> Approved -> Pending (Queue) -> Running -> Success / Failed
//
// "Draft" is the migration's own state. A change request exists per execution
// (apply, rollback or roll forward) and is approved by the leader of every group
// that has access to the database.

export const OPERATION_LABEL = {
  apply: "Apply",
  rollback: "Roll back",
  forward: "Roll forward",
};

export const STATUS_LABEL = {
  pending_review: "Pending review",
  approved: "Approved",
  queued: "In queue",
  running: "Running",
  succeeded: "Success",
  failed: "Failed",
  unknown: "Unresolved",
  rejected: "Rejected",
  cancelled: "Cancelled",
};

// Requests that still block a new request for the same migration and operation.
const OPEN = ["pending_review", "approved", "queued", "running", "unknown"];
// Requests whose status can still change by itself, so screens keep refreshing.
const LIVE = ["pending_review", "approved", "queued", "running", "unknown"];
export const isOpen = (status) => OPEN.includes(status);
export const isLive = (status) => LIVE.includes(status);

const requestsBase = (database) => `${databaseBase(database)}/change-requests`;
const requestPath = (database, id) => `${requestsBase(database)}/${encodeURIComponent(id)}`;

export async function submitChangeRequest(database, migrationId, operation) {
  const data = await apiWithSessionRetry(requestsBase(database), "POST", {
    migration_id: migrationId,
    operation,
  });
  return data.change_request;
}

export async function listChangeRequests(database, { migrationId = "", status = "", limit = 0 } = {}) {
  const q = new URLSearchParams();
  if (migrationId) q.set("migration_id", migrationId);
  if (status) q.set("status", status);
  if (limit) q.set("limit", String(limit));
  const suffix = q.size ? `?${q}` : "";
  const data = await api(`${requestsBase(database)}${suffix}`);
  return data.change_requests ?? [];
}

export async function getChangeRequest(database, id) {
  const data = await api(requestPath(database, id));
  return data.change_request;
}

const decide = (action) => async (database, id, comment = "") => {
  const data = await apiWithSessionRetry(`${requestPath(database, id)}/${action}`, "POST", { comment });
  return data.change_request;
};
export const approveChangeRequest = decide("approve");
export const rejectChangeRequest = decide("reject");
export const cancelChangeRequest = decide("cancel");

// Requests, across all databases, waiting for the signed-in user's decision.
export async function pendingApprovals() {
  const data = await api("/api/change-requests/pending-approval");
  return data.change_requests ?? [];
}

// What the signed-in user may do with a request. The server enforces every rule again;
// this only decides which buttons to show.
export function viewerRole(request, userId = viewerId()) {
  const mine = (request.approvals ?? []).filter((a) => a.required_leader_id === userId);
  const isRequester = Boolean(userId) && request.requested_by === userId;
  return {
    isRequester,
    mine,
    canDecide: request.status === "pending_review" && mine.some((a) => a.decision === "pending"),
    canCancel: isRequester && ["pending_review", "approved", "queued"].includes(request.status),
  };
}

export function approvalProgress(request) {
  const approvals = request.approvals ?? [];
  return { approved: approvals.filter((a) => a.decision === "approved").length, total: approvals.length };
}

// The API returns usernames for group leaders in approval records.
export function namesFrom(requests) {
  const names = {};
  for (const request of requests ?? []) {
    for (const approval of request.approvals ?? []) {
      if (approval.required_leader_id && approval.leader_username) {
        names[approval.required_leader_id] = approval.leader_username;
      }
    }
  }
  return names;
}

// The six stages of the workflow with the state of each, for the progress track.
export function trackFor(request) {
  const labels = ["Draft", "Pending review", "Approved", "In queue", "Running", "Success / Failed"];
  let at = 0;
  let kind = "current";
  let label;
  switch (request.status) {
    case "pending_review": at = 1; break;
    case "approved": at = 2; break;
    case "queued": at = 3; break;
    case "running": at = 4; break;
    case "succeeded": at = 5; kind = "done"; label = "Success"; break;
    case "failed": at = 5; kind = "bad"; label = "Failed"; break;
    case "unknown": at = 5; label = "Unresolved"; break;
    case "rejected": at = 1; kind = "bad"; label = "Rejected"; break;
    case "cancelled":
      at = request.queued_at ? 3 : request.approved_at ? 2 : 1;
      kind = "bad";
      label = "Cancelled";
      break;
    default: break;
  }
  return labels.map((text, i) => ({
    label: i === at && label ? label : text,
    state: i < at ? "done" : i === at ? kind : "todo",
  }));
}

// The SQL a request runs, taken from the migration definition.
export function sqlFor(migration, request) {
  if (!migration || !request) return "";
  return (request.operation === "rollback" ? migration.sql_down : migration.sql_script) ?? "";
}

// Hex SHA-256 of text, matching the server's sql_hash. Null when the browser has no
// Web Crypto (non-secure context), in which case no integrity claim is made.
export async function sha256Hex(text) {
  const subtle = globalThis.crypto?.subtle;
  if (!subtle) return null;
  const digest = await subtle.digest("SHA-256", new TextEncoder().encode(text));
  return Array.from(new Uint8Array(digest), (b) => b.toString(16).padStart(2, "0")).join("");
}

export const shortId = (id) => (id && id.length > 12 ? `${id.slice(0, 8)}…` : id || "—");

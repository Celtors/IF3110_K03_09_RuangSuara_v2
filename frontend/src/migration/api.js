import { sessionFetch } from "../api/client.js";
import { fetchMe } from "../auth/session.js";

let me = null;
export async function session() {
  const value = await fetchMe();
  if (!value) throw new APIError("A validated session is required", "unauthenticated");
  me = value;
  return value;
}
// Id of the signed-in user, known once session() has resolved. Used to show which
// approvals and actions belong to them; the server still enforces every rule.
export const viewerId = () => me?.id ?? "";
export class APIError extends Error {
  constructor(message, code, details) {
    super(message);
    this.code = code;
    this.details = details; // e.g. { execution_time_ms, postgres_code, position }
  }
}
export async function api(path, method = "GET", body) {
  const response = await sessionFetch(path, method, body);
  const data = await response.json().catch(() => ({}));
  if (!response.ok)
    throw new APIError(
      data.error?.message ?? `Request failed (${response.status})`,
      response.status === 401 ? "unauthenticated" : data.error?.code ?? "request_failed",
      data.data ?? undefined,
    );
  return data;
}
export const databaseBase = (id) => `/api/databases/${encodeURIComponent(id)}`;
export const apiWithSessionRetry = api;
// /migrations for the workspace home, /migrations/<page> for the rest.
const migrationPath = (page) => (page === "index" ? "/migrations" : `/migrations/${page}`);

export function href(page, database, id) {
  const q = new URLSearchParams({ database });
  if (id) q.set("id", id);
  return `${migrationPath(page)}?${q}`;
}

export function correctiveDraftHref(database, batchId, draftId) {
  return `${href("editor", database, draftId)}&corrects=${encodeURIComponent(batchId)}`;
}

// URL of a change request page. It uses ?request= because ?id= is a migration id.
export function requestHref(database, request) {
  return `${migrationPath("change-request")}?${new URLSearchParams({ database, request })}`;
}

// URL of a workspace tab: /migrations hosts the whole workspace; ?tab= selects it.
export function workspaceHref(tab, database) {
  const q = new URLSearchParams({ tab });
  if (database) q.set("database", database);
  return `${migrationPath("index")}?${q}`;
}

export function date(value) {
  return value ? new Date(value).toLocaleString() : "—";
}
export const draftKey = (database) => `dmp-generated-sql:${database}`;
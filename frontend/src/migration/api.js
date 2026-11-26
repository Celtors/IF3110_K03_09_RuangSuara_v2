import { fetchMe } from "../auth/session.js";

let token = "";
let me = null;
export async function session() {
  token = "";
  const value = await fetchMe();
  if (!value) throw new APIError("A validated session is required", "unauthenticated");
  token = value.csrf_token ?? "";
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
  const response = await fetch(path, {
    method,
    credentials: "same-origin",
    headers: {
      "Content-Type": "application/json",
      ...(method !== "GET" ? { "X-CSRF-Token": token } : {}),
    },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const data = await response.json().catch(() => ({}));
  if (!response.ok)
    throw new APIError(
      data.error?.message ?? `Request failed (${response.status})`,
      data.error?.code ?? "request_failed",
      data.data ?? undefined,
    );
  return data;
}
export const databaseBase = (id) => `/api/databases/${encodeURIComponent(id)}`;
export async function apiWithSessionRetry(path, method, body) {
  for (let attempt = 0; ; attempt++) {
    try {
      return await api(path, method, body);
    } catch (e) {
      if (e.code !== "csrf_failed" || attempt >= 2) throw e;
      await session();
    }
  }
}
export function href(page, database, id) {
  const q = new URLSearchParams({ database });
  if (id) q.set("id", id);
  return `/migrations/${page}.html?${q}`;
}

// URL of a change request page. It uses ?request= because ?id= is a migration id.
export function requestHref(database, request) {
  return `/migrations/change-request.html?${new URLSearchParams({ database, request })}`;
}

// URL of a workspace tab: index.html hosts the whole workspace; ?tab= selects it.
export function workspaceHref(tab, database) {
  const q = new URLSearchParams({ tab });
  if (database) q.set("database", database);
  return `/migrations/index.html?${q}`;
}

export function date(value) {
  return value ? new Date(value).toLocaleString() : "—";
}
export const draftKey = (database) => `dmp-generated-sql:${database}`;
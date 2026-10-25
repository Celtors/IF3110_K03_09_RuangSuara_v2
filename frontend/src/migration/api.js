let token = "";
export async function session() {
  const response = await fetch("/api/session", { credentials: "same-origin" });
  if (response.ok) {
    const value = await response.json();
    token = value.csrf_token ?? "";
  }
}
export class APIError extends Error {
  constructor(message, code) {
    super(message);
    this.code = code;
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
    );
  return data;
}
export function href(page, database, id) {
  const q = new URLSearchParams({ database });
  if (id) q.set("id", id);
  return `/migrations/${page}.html?${q}`;
}
export function date(value) {
  return value ? new Date(value).toLocaleString() : "—";
}
export const draftKey = (database) => `dmp-generated-sql:${database}`;

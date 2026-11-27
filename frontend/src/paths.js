// Which URLs belong to the single-page app. Pure and dependency-free so the router, the Vite
// dev server and the tests can all use it.
//
//   /                          -> the admin console
//   /login   /home
//   /admin/users | groups | databases | policies
//   /migrations                -> the workspace; /migrations/history | editor | details |
//                                 schema-diff | query-plan | change-request
//
// Older builds used .html names (/login.html, /admin/users.html, /migrations/details.html).
// Those still resolve, and the app replaces them with the clean form in the address bar.

export const MIGRATION_PAGES = ["index", "history", "editor", "details", "schema-diff", "query-plan", "change-request"];
export const ADMIN_PAGES = ["users", "groups", "databases", "policies"];

// The clean form of a path: no .html, no trailing slash, and the workspace home is /migrations.
export function cleanPath(pathname) {
  let p = pathname.replace(/\.html$/, "");
  if (p === "/index") p = "/";
  if (p.length > 1) p = p.replace(/\/+$/, "");
  if (p === "/migrations/index") p = "/migrations";
  return p;
}

// -> { name: "root" | "login" | "home" | "admin" | "migrations", page? } or null for a path
// that is not part of the app (the server answers those itself: API calls, assets, 404s).
export function matchPath(pathname) {
  const p = cleanPath(pathname);
  if (p === "/") return { name: "root" };
  if (p === "/login") return { name: "login" };
  if (p === "/home") return { name: "home" };
  let m = p.match(/^\/admin\/([^/]+)$/);
  if (m) return ADMIN_PAGES.includes(m[1]) ? { name: "admin", page: m[1] } : null;
  if (p === "/migrations") return { name: "migrations", page: "index" };
  m = p.match(/^\/migrations\/([^/]+)$/);
  if (m) return MIGRATION_PAGES.includes(m[1]) ? { name: "migrations", page: m[1] } : null;
  return null;
}

export const isAppPath = (pathname) => matchPath(pathname) !== null;

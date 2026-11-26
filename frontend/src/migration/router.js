import { useSyncExternalStore } from "react";

// A small client-side router for the migration app, so moving between its pages never
// reloads the document. URLs stay exactly as they were (/migrations/<page>.html?database=..),
// which keeps bookmarks, links from other apps and the browser's back button working; the
// server serves the same shell for all of them and this module reads the view from the URL.
//
// react-router-dom is not used on purpose: the app has no nested routes, only a page name
// and a few query parameters, and the module is small enough to test on its own.

export const PAGES = ["index", "history", "editor", "details", "schema-diff", "query-plan", "change-request"];

const listeners = new Set();

function parse(loc) {
  const params = new URLSearchParams(loc.search);
  const last = loc.pathname.split("/").pop()?.replace(/\.html$/, "") || "index";
  const route = {
    page: PAGES.includes(last) ? last : "index",
    database: params.get("database") ?? "",
    id: params.get("id") ?? "",
    request: params.get("request") ?? "",
    tab: params.get("tab") ?? "",
  };
  // Identity of the view: when it changes the page content starts fresh, like a page load did.
  route.key = [route.page, route.database, route.id, route.request].join("|");
  return Object.freeze(route);
}

let snapshot = parse(location);
export const getRoute = () => snapshot;
export function subscribe(listener) {
  listeners.add(listener);
  return () => listeners.delete(listener);
}
function commit() {
  snapshot = parse(location);
  for (const listener of [...listeners]) listener(snapshot);
}

// Go to another page of the app. The listeners that keep module state in sync (navigation.js)
// run before React re-renders, because they subscribe first.
export function navigate(to, { replace = false } = {}) {
  window.history[replace ? "replaceState" : "pushState"](null, "", to);
  commit();
}

// Change the address bar without changing the view (the workspace keeps database and tab in
// the URL so a refresh restores them, but it has already updated its own state).
export function replaceUrl(to) {
  window.history.replaceState(null, "", to);
}

export const useRoute = () => useSyncExternalStore(subscribe, getRoute, getRoute);

if (typeof window !== "undefined") window.addEventListener("popstate", commit);

// Page URLs of this app, from a path like /migrations/details.html (or /migrations/).
function isAppPath(pathname) {
  const m = pathname.match(/^\/migrations\/(?:([^/]+)\.html)?$/);
  return Boolean(m) && (!m[1] || PAGES.includes(m[1]));
}

// Document-level click handler: any plain click on a link to another page of this app becomes
// a client-side navigation. Modified clicks, new-tab links, downloads and links to other apps
// (login, admin, home) keep the browser's normal behaviour.
export function interceptLinks(event) {
  if (event.defaultPrevented || event.button !== 0) return;
  if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
  const link = event.target instanceof Element ? event.target.closest("a[href]") : null;
  if (!link || (link.target && link.target !== "_self") || link.hasAttribute("download")) return;
  const url = new URL(link.href, location.href);
  if (url.origin !== location.origin || !isAppPath(url.pathname)) return;
  event.preventDefault();
  navigate(url.pathname + url.search + url.hash);
}

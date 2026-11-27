import { useSyncExternalStore } from "react";
import { isAppPath } from "./paths.js";

// The one client-side router of the app. It keeps the current location in a small store,
// moves between screens with the History API (so nothing reloads), and turns plain clicks on
// links to other screens of the app into those moves. Screens read the location with
// useLocation(); the route table lives in App.jsx.
//
// react-router-dom is not used on purpose: the app has a flat list of screens and a few query
// parameters, and this module is small enough to test on its own.

const listeners = new Set();

const read = () => Object.freeze({ pathname: location.pathname, search: location.search, hash: location.hash });
let snapshot = read();

export const getLocation = () => snapshot;
export function subscribe(listener) {
  listeners.add(listener);
  return () => listeners.delete(listener);
}
function commit() {
  snapshot = read();
  for (const listener of [...listeners]) listener(snapshot);
}

// Go to another URL of the app. Listeners that keep module state in sync (migration/navigation.js)
// run before React re-renders, because the store notifies them synchronously and React renders after.
export function navigate(to, { replace = false } = {}) {
  window.history[replace ? "replaceState" : "pushState"](null, "", to);
  commit();
}

// Change the address bar without changing the screen (the migration workspace keeps its database
// and tab in the URL so a refresh restores them, but it has already updated its own state).
export function replaceUrl(to) {
  window.history.replaceState(null, "", to);
}

export const useLocation = () => useSyncExternalStore(subscribe, getLocation, getLocation);

if (typeof window !== "undefined") window.addEventListener("popstate", commit);

// Document-level click handler: a plain click on a link to another screen of this app becomes a
// client-side navigation. Modified clicks, new-tab links, downloads, other origins and paths the
// server answers itself (API, assets) keep the browser's normal behaviour.
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

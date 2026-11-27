import { useSyncExternalStore } from "react";
import { getLocation, subscribe as subscribeLocation } from "../router.js";
import { MIGRATION_PAGES, matchPath } from "../paths.js";

// The migration app's view of the app-wide location: which of its pages is showing and the
// parameters it reads (?database= ?id= ?request= ?tab=). Navigation itself is the shared
// router's; it is re-exported so the migration code has one import.
export { navigate, replaceUrl, interceptLinks } from "../router.js";
export const PAGES = MIGRATION_PAGES;

function parse(loc) {
  const params = new URLSearchParams(loc.search);
  const route = {
    page: matchPath(loc.pathname)?.page ?? "index",
    database: params.get("database") ?? "",
    id: params.get("id") ?? "",
    request: params.get("request") ?? "",
    tab: params.get("tab") ?? "",
  };
  // Identity of the view: when it changes the page content starts fresh, like a page load did.
  route.key = [route.page, route.database, route.id, route.request].join("|");
  return Object.freeze(route);
}

// One parsed route per location, so the object is stable between renders until the location commits.
let parsedFor = null;
let parsed = null;
export function getRoute() {
  const loc = getLocation();
  if (loc !== parsedFor) {
    parsedFor = loc;
    parsed = parse(loc);
  }
  return parsed;
}
export const subscribe = (listener) => subscribeLocation(() => listener(getRoute()));
export const useRoute = () => useSyncExternalStore(subscribeLocation, getRoute, getRoute);

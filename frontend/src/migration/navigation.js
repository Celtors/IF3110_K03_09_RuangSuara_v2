import { getRoute, subscribe } from "./router.js";

// The values of the current page URL. They are live bindings: the router updates them on every
// client-side navigation, before React renders the new page, so importers always read the
// current page. (A change request page identifies its request with ?request=; ?id= stays a
// migration id.)
export let database;
export let id;
export let requestId;
export let page;
export let base;
export let detailPath;

function apply(route) {
  database = route.database;
  id = route.id;
  requestId = route.request;
  page = route.page;
  base = `/api/databases/${encodeURIComponent(database)}`;
  detailPath = `${base}/migrations/${encodeURIComponent(id)}`;
}
apply(getRoute());
subscribe(apply);
export const titles = {
  index: [
    "A clear history of every change.",
    "Create, review, and track database migrations in one workspace.",
  ],
  history: [
    "Migration history",
    "Applied versions and drafts for your selected database.",
  ],
  editor: [
    "Migration editor",
    "Review your SQL, save a draft, then confirm its execution.",
  ],
  details: [
    "Migration details",
    "The definition and every immutable execution snapshot.",
  ],
  "schema-diff": [
    "Compare schemas",
    "Describe the desired schema and review the proposed changes.",
  ],
  "query-plan": [
    "Understand the query plan",
    "Inspect PostgreSQL estimates before running a statement.",
  ],
  "change-request": [
    "Change request",
    "Review the SQL, collect approvals, and follow the change through the queue.",
  ],
};

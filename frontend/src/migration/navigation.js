// Each HTML navigation reloads these values. There is no client-side router.
const params = new URLSearchParams(location.search);
export const database = params.get("database") ?? "";
export const id = params.get("id") ?? "";
export const page =
  location.pathname.split("/").pop()?.replace(".html", "") || "index";
export const base = `/api/databases/${encodeURIComponent(database)}`;
export const detailPath = `${base}/migrations/${encodeURIComponent(id)}`;
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
};

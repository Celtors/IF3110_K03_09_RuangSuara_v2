import { api, databaseBase } from "./api.js";

// Translate the server's ordering hint into the same action on both migration views.
export function nextOperations(m) {
  const labels = {
    apply: m.state === "failed" ? "Resubmit for approval" : "Submit for approval",
    rollback: "Request rollback",
    forward: "Request roll forward",
  };
  const operation = m.next_operation;
  return operation ? [[operation, labels[operation]]] : [];
}

export function isUnattemptedDraft(m) {
  return m.state === "draft" && !m.attempts?.length;
}

// Every execution (apply, rollback, roll forward) on a database, oldest first. With
// hideRolledBack the server drops each successful execution together with the rollback that
// undid it, so only changes that still stand remain.
export async function fetchExecutionHistory(databaseId, hideRolledBack = false) {
  const q = hideRolledBack ? "?hide_rollback=true" : "";
  const data = await api(`${databaseBase(databaseId)}/migration-history${q}`);
  return data.attempts ?? [];
}

// The newest execution of each migration, by migration id. Row actions belong on that one
// row only, because the action applies to the migration's current state, not to a past run.
export function latestByMigration(entries) {
  const latest = new Map();
  for (const e of entries) {
    const seen = latest.get(e.migration_id);
    if (!seen || new Date(e.started_at) >= new Date(seen.started_at)) latest.set(e.migration_id, e);
  }
  return latest;
}

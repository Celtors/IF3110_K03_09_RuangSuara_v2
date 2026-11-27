import { api, databaseBase } from "./api.js";

// The operations a migration can be asked to run next, given its state, as [operation, label]
// pairs. Each one needs approval before it runs. Shared by the details page and the
// execution history so both offer exactly the same buttons.
export function nextOperations(m) {
  if (m.state === "draft") return [["apply", "Submit for approval"]];
  if (m.state === "failed") return [["apply", "Resubmit for approval"]];
  if (m.state === "applied" && !m.irreversible && m.sql_down?.trim()) return [["rollback", "Request rollback"]];
  if (m.state === "rolled_back") return [["forward", "Request roll forward"]];
  return [];
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

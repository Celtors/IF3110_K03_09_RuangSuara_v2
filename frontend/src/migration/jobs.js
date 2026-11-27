import { api, apiWithSessionRetry, databaseBase } from "./api.js";
import { STATUS, listChangeRequests } from "./changeRequests.js";

const jobPath = (database, id) => `${databaseBase(database)}/jobs/${encodeURIComponent(id)}`;
export const getJob = (database, id) => api(jobPath(database, id));
export const recoverJob = (database, id, action) => apiWithSessionRetry(`${jobPath(database, id)}/${action}`, "POST");

// A batch's approval request stays running even when paused. Only the job endpoint
// can distinguish active partial progress from a paused job or compensation.
export const jobStatus = (job) => ({ unknown: "unresolved", succeeded: "completed" }[job?.status] ?? job?.status);
export function isJobActive(job) {
  const status = jobStatus(job);
  if (!job || job.paused || status === "unresolved") return false;
  if (status === "partial") return job.paused === false || job.compensation_status === "running";
  return Boolean(STATUS[status]?.live);
}
export function recoveryActions(job, canEdit, isRequester) {
  if (!canEdit || !isRequester || !job?.paused || !STATUS[jobStatus(job)]?.recoverable || isJobActive(job)) return [];
  // No optimistic fallback: this API supplies permission-filtered actions.
  return ["resume", "compensate"].filter((action) => job.available_actions?.includes(action));
}

// The backend does not identify the blocker in job responses. Query only the
// selected database and present candidates, not a fabricated authoritative blocker.
export async function possibleBlockers(database, exclude = "") {
  const lists = await Promise.all(["running", "unknown"].map((status) => listChangeRequests(database, { status, limit: 200 })));
  const candidates = lists.flat().filter((request) => request.id !== exclude && STATUS[request.status]?.blocks);
  // The request can remain "running" while its batch job is actually paused.
  // Enrich candidates with the authoritative job status when available.
  const verified = await Promise.all(candidates.map(async (request) => {
    try {
      const job = await getJob(database, request.id);
      const status = jobStatus(job);
      if (!STATUS[status]) return request;
      if (STATUS[status].blocks || status === "queued" || (status === "failed" && job.paused)) {
        return { ...request, status, paused: job.paused };
      }
      return null;
    } catch { return request; }
  }));
  return verified.filter(Boolean);
}

export function batchErrors(attribute, size) {
  return {
    attribute: /^[a-z_][a-z0-9_]{0,62}$/.test(attribute.trim()) ? "" : "Enter a lowercase PostgreSQL column name (up to 63 characters).",
    size: /^\d+$/.test(size) && Number(size) >= 1 && Number(size) <= 10000 ? "" : "Enter a whole number from 1 to 10,000.",
  };
}

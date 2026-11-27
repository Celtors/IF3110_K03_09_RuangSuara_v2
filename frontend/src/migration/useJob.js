import { useCallback, useEffect, useState } from "react";
import { getJob, isJobActive } from "./jobs.js";

export const JOB_POLL_MS = 4000;

export function useJob(database, id) {
  const scope = `${database}|${id}`;
  const [tick, setTick] = useState(0);
  const [state, setState] = useState({ scope: "", job: null, error: "" });
  useEffect(() => {
    if (!database || !id) return;
    let active = true;
    let timer;
    let failures = 0;
    let lastJob;
    async function fetchJob() {
      try {
        const job = await getJob(database, id);
        if (!active) return;
        failures = 0;
        lastJob = job;
        setState({ scope, job, error: "" });
        if (isJobActive(job)) timer = setTimeout(fetchJob, JOB_POLL_MS);
      } catch (e) {
        if (!active) return;
        failures++;
        setState((old) => ({ scope, job: old.scope === scope ? old.job : null, error: e.message }));
        // Retry transient read failures only, with bounded backoff. Manual refresh
        // remains available after exhaustion or an authorization/not-found error.
        if ((!lastJob || isJobActive(lastJob)) && failures < 3 && !["forbidden", "unauthenticated", "not_found"].includes(e.code)) {
          timer = setTimeout(fetchJob, JOB_POLL_MS * 2 ** failures);
        }
      }
    }
    fetchJob();
    return () => { active = false; clearTimeout(timer); };
  }, [database, id, scope, tick]);
  const reload = useCallback(() => setTick((n) => n + 1), []);
  const current = state.scope === scope ? state : { job: null, error: "" };
  return { ...current, loading: Boolean(database && id) && state.scope !== scope, reload };
}

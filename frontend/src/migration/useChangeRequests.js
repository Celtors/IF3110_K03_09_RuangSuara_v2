import { useCallback, useEffect, useRef, useState } from "react";
import { api, databaseBase } from "./api.js";
import {
  getChangeRequest,
  isLive,
  listChangeRequests,
  pendingApprovals,
  sha256Hex,
} from "./changeRequests.js";

const POLL_MS = 4000;

// Requests of one database (optionally of one migration). Refreshes itself while any
// request can still change, and keeps showing the previous rows during a refresh.
export function useChangeRequestList(databaseId, migrationId = "", onSettled) {
  const scope = `${databaseId}|${migrationId}`;
  const [tick, setTick] = useState(0);
  const [state, setState] = useState({ scope: "", items: [], error: "" });
  useEffect(() => {
    if (!databaseId) return;
    let active = true;
    (async () => {
      try {
        const items = await listChangeRequests(databaseId, { migrationId });
        if (active) setState({ scope, items, error: "" });
      } catch (e) {
        if (active) setState((previous) => ({
          scope,
          items: previous.scope === scope ? previous.items : [],
          error: e.message,
        }));
      }
    })();
    return () => {
      active = false;
    };
  }, [databaseId, migrationId, scope, tick]);
  const items = state.scope === scope ? state.items : [];
  const previous = useRef(state);
  useEffect(() => {
    if (state.scope !== scope || state.error) return;
    const prior = previous.current;
    previous.current = state;
    if (prior.scope !== scope || !onSettled) return;
    const liveIds = new Set(prior.items.filter((r) => isLive(r.status)).map((r) => r.id));
    if (state.items.some((r) => liveIds.has(r.id) && !isLive(r.status))) onSettled();
  }, [scope, state, onSettled]);
  const live = items.some((r) => isLive(r.status));
  useEffect(() => {
    if (!live) return;
    const timer = setInterval(() => setTick((n) => n + 1), POLL_MS);
    return () => clearInterval(timer);
  }, [live]);
  const reload = useCallback(() => setTick((n) => n + 1), []);
  return {
    items,
    error: state.scope === scope ? state.error : "",
    loading: Boolean(databaseId) && state.scope !== scope,
    live,
    reload,
  };
}

// One request with approvals and audit events; refreshes itself while it can still change.
export function useChangeRequest(databaseId, requestId) {
  const scope = `${databaseId}|${requestId}`;
  const [tick, setTick] = useState(0);
  const [state, setState] = useState({ scope: "", request: undefined, error: "" });
  useEffect(() => {
    if (!databaseId || !requestId) return;
    let active = true;
    (async () => {
      try {
        const request = await getChangeRequest(databaseId, requestId);
        if (active) setState({ scope, request, error: "" });
      } catch (e) {
        if (active) setState((s) => ({ scope, request: s.scope === scope ? s.request : undefined, error: e.message }));
      }
    })();
    return () => {
      active = false;
    };
  }, [databaseId, requestId, scope, tick]);
  const current = state.scope === scope ? state : { request: undefined, error: "" };
  const live = Boolean(current.request) && isLive(current.request.status);
  useEffect(() => {
    if (!live) return;
    const timer = setInterval(() => setTick((n) => n + 1), POLL_MS);
    return () => clearInterval(timer);
  }, [live]);
  const reload = useCallback(() => setTick((n) => n + 1), []);
  // After an approve/reject/cancel the server returns the updated request; show it at once.
  const replace = useCallback((request) => setState({ scope, request, error: "" }), [scope]);
  return {
    request: current.request,
    error: current.error,
    loading: Boolean(databaseId && requestId) && state.scope !== scope,
    reload,
    replace,
  };
}

// Requests across all databases that wait for the signed-in user's decision.
export function usePendingApprovals(ensureSession) {
  const [tick, setTick] = useState(0);
  const [state, setState] = useState({ tick: -1, items: [], error: "" });
  useEffect(() => {
    let active = true;
    (async () => {
      try {
        await ensureSession();
        const items = await pendingApprovals();
        if (active) setState({ tick, items, error: "" });
      } catch (e) {
        if (active) setState({ tick, items: [], error: e.message });
      }
    })();
    return () => {
      active = false;
    };
  }, [ensureSession, tick]);
  const reload = useCallback(() => setTick((n) => n + 1), []);
  return { items: state.items, error: state.error, loading: state.tick !== tick, reload };
}

// The migration definition a request refers to, so reviewers can read the SQL they approve.
export function useMigrationDefinition(databaseId, migrationId) {
  const scope = `${databaseId}|${migrationId}`;
  const [state, setState] = useState({ scope: "", migration: undefined, error: "" });
  useEffect(() => {
    if (!databaseId || !migrationId) return;
    let active = true;
    (async () => {
      try {
        const migration = await api(`${databaseBase(databaseId)}/migrations/${encodeURIComponent(migrationId)}`);
        if (active) setState({ scope, migration, error: "" });
      } catch (e) {
        if (active) setState({ scope, migration: undefined, error: e.message });
      }
    })();
    return () => {
      active = false;
    };
  }, [databaseId, migrationId, scope]);
  const current = state.scope === scope ? state : { migration: undefined, error: "" };
  return { ...current, loading: Boolean(databaseId && migrationId) && state.scope !== scope };
}

// Whether `sql` still hashes to what was submitted: true, false, or null when unknown
// (not yet computed, or no Web Crypto in this browser context).
export function useSqlIntegrity(sql, expectedHash) {
  const key = `${expectedHash}|${sql}`;
  const [state, setState] = useState({ key: "", value: null });
  useEffect(() => {
    if (!expectedHash || !sql) return;
    let active = true;
    sha256Hex(sql)
      .then((hash) => active && setState({ key, value: hash === null ? null : hash === expectedHash }))
      .catch(() => active && setState({ key, value: null }));
    return () => {
      active = false;
    };
  }, [sql, expectedHash, key]);
  return state.key === key ? state.value : null;
}

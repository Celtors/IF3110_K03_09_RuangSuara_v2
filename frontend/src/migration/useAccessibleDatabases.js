import { useEffect, useRef, useState } from "react";
import { api } from "./api.js";

export function useAccessibleDatabases(enabled, ensureSession) {
  const [tick, setTick] = useState(0);
  const [state, setState] = useState({ tick: -1, items: [], error: "" });
  // The list is fetched once per tick, not on every visit: the app is a single-page app and
  // this hook stays mounted while the user moves between pages.
  const loadedTick = useRef(-1);
  useEffect(() => {
    if (!enabled || loadedTick.current === tick) return;
    let active = true;
    (async () => {
      try {
        await ensureSession();
        const data = await api("/api/accessible-databases");
        if (active) {
          loadedTick.current = tick;
          setState({ tick, items: data.databases ?? [], error: "" });
        }
      } catch (e) {
        if (active) setState({ tick, items: [], error: e.message });
      }
    })();
    return () => {
      active = false;
    };
  }, [enabled, ensureSession, tick]);
  return {
    items: state.items,
    error: state.error,
    loading: enabled && state.tick !== tick,
    reload: () => setTick((t) => t + 1),
  };
}
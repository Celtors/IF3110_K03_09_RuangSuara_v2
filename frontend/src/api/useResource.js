import { useEffect, useState } from 'react'

// Loads data with a stable `fetcher`. Keeps the previous data on screen while reloading.
export function useResource(fetcher) {
  const [tick, setTick] = useState(0)
  const [state, setState] = useState({ tick: -1, data: null, error: null })

  useEffect(() => {
    let alive = true
    fetcher()
      .then((data) => alive && setState({ tick, data, error: null }))
      .catch((error) => alive && setState((s) => ({ tick, data: s.data, error })))
    return () => {
      alive = false
    }
  }, [fetcher, tick])

  return {
    data: state.data,
    error: state.error,
    loading: state.tick !== tick,
    reload: () => setTick((t) => t + 1),
  }
}

import { useCallback, useEffect, useRef, useState } from 'react'

// Calls fetcher() now and then every intervalMs. Polling every couple of
// seconds is simple and plenty for a monitoring dashboard; a push channel
// (WebSocket/SSE) would be the next step for lower latency.
export function usePolling(fetcher, intervalMs, deps = []) {
  const [data, setData] = useState(null)
  const [error, setError] = useState(null)
  const fetcherRef = useRef(fetcher)
  fetcherRef.current = fetcher

  const refresh = useCallback(async () => {
    try {
      setData(await fetcherRef.current())
      setError(null)
    } catch (e) {
      setError(e.message)
    }
  }, [])

  useEffect(() => {
    let cancelled = false
    const tick = async () => { if (!cancelled) await refresh() }
    tick()
    const id = setInterval(tick, intervalMs)
    return () => { cancelled = true; clearInterval(id) }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [intervalMs, refresh, ...deps])

  return { data, error, refresh }
}

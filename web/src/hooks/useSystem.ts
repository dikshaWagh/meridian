import { useCallback, useEffect, useState } from 'react'
import type { Catalog, Mode, SystemState } from '../types'
import { fetchCatalog, fetchState, requestMode } from '../api'
import { errorMessage } from '../api'

export function useSystem() {
  const [state, setState] = useState<SystemState | null>(null)
  const [catalog, setCatalog] = useState<Catalog | null>(null)
  const [connected, setConnected] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const refreshCatalog = useCallback(async () => {
    try {
      setCatalog(await fetchCatalog())
    } catch {
      // The connection pill already communicates loss of the API.
    }
  }, [])

  useEffect(() => {
    let alive = true
    fetchState()
      .then((s) => {
        if (alive) setState(s)
      })
      .catch((err) => {
        if (alive) setError(errorMessage(err))
      })
    void refreshCatalog()
    return () => {
      alive = false
    }
  }, [refreshCatalog])

  useEffect(() => {
    const source = new EventSource('/v1/system/events')
    source.addEventListener('open', () => setConnected(true))
    source.addEventListener('state', (event) => {
      try {
        const payload = JSON.parse((event as MessageEvent).data) as { state: SystemState }
        if (payload?.state) setState(payload.state)
      } catch {
        // Ignore malformed frames.
      }
    })
    source.addEventListener('error', () => setConnected(false))
    return () => source.close()
  }, [])

  useEffect(() => {
    const timer = window.setInterval(() => void refreshCatalog(), 4000)
    return () => window.clearInterval(timer)
  }, [refreshCatalog])

  const phase = state?.phase
  const mode = state?.mode
  useEffect(() => {
    if (phase === 'active') void refreshCatalog()
  }, [phase, mode, refreshCatalog])

  const switchTo = useCallback(async (mode: Exclude<Mode, 'idle'>) => {
    const next = await requestMode(mode)
    setState(next)
  }, [])

  return {
    state,
    catalog,
    connected,
    error,
    clearError: () => setError(null),
    refreshCatalog,
    switchTo,
  }
}

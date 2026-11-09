import { useCallback, useEffect, useState } from 'react'
import { ApiError, NetworkError, fetchModels, fetchStatus } from '../lib/api'
import { STATUS_POLL_MS } from '../lib/constants'
import type { WorkerStatus } from '../lib/types'

export interface LiveStatusState {
  workers: WorkerStatus[]
  models: string[]
  loading: boolean
  error: string | null
  offline: boolean
  lastUpdated: number | null
  refresh: () => void
}

const OFFLINE_MESSAGE =
  'Meridian is unreachable. Start the backend (for example: meridian-server serve --mock) or switch to Simulation.'

/**
 * Polls GET /status and GET /v1/models, pausing while the tab is hidden.
 * This is the only live data source for the Dashboard and Playground.
 */
export function useMeridianStatus(enabled: boolean): LiveStatusState {
  const [workers, setWorkers] = useState<WorkerStatus[]>([])
  const [models, setModels] = useState<string[]>([])
  const [loading, setLoading] = useState(enabled)
  const [error, setError] = useState<string | null>(null)
  const [offline, setOffline] = useState(false)
  const [lastUpdated, setLastUpdated] = useState<number | null>(null)
  const [refreshKey, setRefreshKey] = useState(0)

  const refresh = useCallback(() => setRefreshKey((key) => key + 1), [])

  useEffect(() => {
    if (!enabled) return

    let alive = true
    let controller: AbortController | null = null

    const load = async (showLoading: boolean) => {
      if (document.hidden) return
      controller?.abort()
      const current = new AbortController()
      controller = current

      if (showLoading) setLoading(true)

      try {
        const nextWorkers = await fetchStatus(current.signal)
        if (!alive || current.signal.aborted) return
        setWorkers(nextWorkers)
        setLastUpdated(Date.now())
        setError(null)
        setOffline(false)
        setLoading(false)
      } catch (cause) {
        if (!alive || current.signal.aborted) return
        if (cause instanceof DOMException && cause.name === 'AbortError') return
        setLoading(false)
        if (cause instanceof NetworkError || (cause instanceof ApiError && cause.status >= 500)) {
          setOffline(true)
          setError(OFFLINE_MESSAGE)
        } else {
          setError(cause instanceof Error ? cause.message : 'Could not load worker status.')
        }
      }

      try {
        const nextModels = await fetchModels(current.signal)
        if (alive && !current.signal.aborted) setModels(nextModels)
      } catch {
        // Model discovery is supplementary; a failure must not mark the backend offline.
      }
    }

    void load(true)
    const timer = window.setInterval(() => {
      void load(false)
    }, STATUS_POLL_MS)

    const onVisibility = () => {
      if (!document.hidden) void load(false)
    }
    document.addEventListener('visibilitychange', onVisibility)

    return () => {
      alive = false
      window.clearInterval(timer)
      controller?.abort()
      document.removeEventListener('visibilitychange', onVisibility)
    }
  }, [enabled, refreshKey])

  return { workers, models, loading, error, offline, lastUpdated, refresh }
}

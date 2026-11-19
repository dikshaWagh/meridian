import { useCallback, useEffect, useState } from 'react'
import type { Catalog } from '../types'
import { fetchCatalog } from '../api'
import { errorMessage } from '../api'

export function useSystem() {
  const [catalog, setCatalog] = useState<Catalog | null>(null)
  const [connected, setConnected] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const refreshCatalog = useCallback(async () => {
    try {
      setCatalog(await fetchCatalog())
      setConnected(true)
      setError(null)
    } catch (err) {
      setConnected(false)
      setError(errorMessage(err))
    }
  }, [])

  useEffect(() => {
    void refreshCatalog()
    const timer = window.setInterval(() => void refreshCatalog(), 4000)
    return () => window.clearInterval(timer)
  }, [refreshCatalog])

  return {
    catalog,
    connected,
    error,
    clearError: () => setError(null),
    refreshCatalog,
  }
}

import { useEffect, useState } from 'react'
import { errorMessage } from '../api'

export interface Polled<T> {
  data: T | null
  error: string | null
  updatedAt: number | null
}

// usePoll calls fetcher now and then every intervalMs. Pass a stable fetcher
// (a module-level function) so the timer is not reset on each render.
export function usePoll<T>(fetcher: () => Promise<T>, intervalMs: number): Polled<T> {
  const [state, setState] = useState<Polled<T>>({ data: null, error: null, updatedAt: null })

  useEffect(() => {
    let alive = true
    const tick = async () => {
      try {
        const data = await fetcher()
        if (alive) setState({ data, error: null, updatedAt: Date.now() })
      } catch (err) {
        if (alive) setState((prev) => ({ ...prev, error: errorMessage(err) }))
      }
    }
    void tick()
    const timer = setInterval(tick, intervalMs)
    return () => {
      alive = false
      clearInterval(timer)
    }
  }, [fetcher, intervalMs])

  return state
}

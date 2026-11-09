import { useEffect, useState } from 'react'
import type { WorkerStatus } from '../lib/types'

export interface ObservedPoint {
  t: number
  inFlight: number
  served: number
}

/** Client-side samples of the status feed. No extra requests are made. */
export function useObservedHistory(workers: WorkerStatus[], enabled: boolean): ObservedPoint[] {
  const [points, setPoints] = useState<ObservedPoint[]>([])

  useEffect(() => {
    setPoints([])
  }, [enabled])

  useEffect(() => {
    if (!enabled || workers.length === 0) return
    const point: ObservedPoint = {
      t: Date.now(),
      inFlight: workers.reduce((sum, worker) => sum + worker.inFlight, 0),
      served: workers.reduce((sum, worker) => sum + worker.served, 0),
    }
    setPoints((previous) => [...previous, point].slice(-30))
  }, [workers, enabled])

  return points
}

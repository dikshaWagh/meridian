import { useCallback, useEffect, useRef, useSyncExternalStore } from 'react'
import { LlmSimulation } from '../simulation/llmSimulation'
import type { SimSendResult } from '../simulation/llmSimulation'
import type { WorkerStatus } from '../lib/types'

export interface SimulationState {
  workers: WorkerStatus[]
  models: string[]
  sendRequest: (model: string, estTokens: number) => SimSendResult
  kill: (id: string) => void
  revive: (id: string) => void
  toggleHealthFailing: (id: string) => void
  reset: () => void
  requestCounter: () => number
}

/** Browser-only mirror of the verified LLM balancer. Makes no network requests. */
export function useSimulation(enabled: boolean): SimulationState {
  const store = useRef<LlmSimulation | null>(null)
  if (!store.current) store.current = new LlmSimulation()
  const simulation = store.current

  const snapshot = useSyncExternalStore(
    simulation.subscribe,
    simulation.getSnapshot,
    simulation.getSnapshot,
  )

  useEffect(() => {
    if (!enabled) return
    const timer = window.setInterval(() => simulation.tick(), 1000)
    return () => window.clearInterval(timer)
  }, [enabled, simulation])

  const sendRequest = useCallback(
    (model: string, estTokens: number) => simulation.send(model, estTokens),
    [simulation],
  )
  const kill = useCallback((id: string) => simulation.kill(id), [simulation])
  const revive = useCallback((id: string) => simulation.revive(id), [simulation])
  const toggleHealthFailing = useCallback(
    (id: string) => simulation.toggleHealthFailing(id),
    [simulation],
  )
  const reset = useCallback(() => simulation.reset(), [simulation])
  const requestCounter = useCallback(() => simulation.requestCounter(), [simulation])

  return {
    workers: snapshot.workers,
    models: snapshot.models,
    sendRequest,
    kill,
    revive,
    toggleHealthFailing,
    reset,
    requestCounter,
  }
}

import { FAILURE_THRESHOLD, HEALTH_PROBE_MS, STARTING_GRACE_MS } from '../lib/constants'
import type { WorkerState, WorkerStatus } from '../lib/types'

interface SimWorker extends WorkerStatus {
  startedAt: number
  healthFailing: boolean
  exited: boolean
}

interface ActiveRequest {
  workerId: string
  tokens: number
  endsAt: number
}

export interface SimulationSnapshot {
  workers: WorkerStatus[]
  models: string[]
}

export type SimSendResult =
  | {
      ok: true
      status: 200
      workerId: string
      model: string
      healthyCandidates: number
      roundRobinIndex: number
    }
  | { ok: false; status: 404 | 503; message: string }

interface Spec {
  id: string
  model: string
  port: number
}

/** Mirrors balancer.DefaultWorkers. */
const SPECS: Spec[] = [
  { id: 'w1', model: 'qwen2.5-3b', port: 8081 },
  { id: 'w2', model: 'qwen2.5-3b', port: 8082 },
  { id: 'w3', model: 'llama-3.2-3b', port: 8083 },
  { id: 'w4', model: 'llama-3.2-3b', port: 8084 },
]

const REQUEST_DURATION_MS = 1400

/**
 * Deterministic, in-browser mirror of the verified LLM balancer behaviour:
 * per-model round robin, 2 s probes, 3 consecutive failures → unhealthy,
 * 180 s starting grace, recovery on the next successful probe.
 * No network access.
 */
export class LlmSimulation {
  private workers: SimWorker[]
  private active: ActiveRequest[] = []
  private rr = new Map<string, number>()
  private listeners = new Set<() => void>()
  private snapshot: SimulationSnapshot
  private lastProbeAt = Date.now()
  private nextRequestId = 1

  constructor() {
    this.workers = this.createWorkers()
    this.snapshot = this.buildSnapshot()
  }

  subscribe = (listener: () => void): (() => void) => {
    this.listeners.add(listener)
    return () => {
      this.listeners.delete(listener)
    }
  }

  getSnapshot = (): SimulationSnapshot => this.snapshot

  private createWorkers(): SimWorker[] {
    const now = Date.now()
    return SPECS.map((spec) => ({
      id: spec.id,
      model: spec.model,
      port: spec.port,
      pid: 4100 + spec.port,
      state: 'healthy',
      healthy: true,
      inFlight: 0,
      served: 0,
      estTokensInFlight: 0,
      consecutiveFailures: 0,
      lastCheck: new Date(now).toISOString(),
      lastError: '',
      startedAt: now - STARTING_GRACE_MS,
      healthFailing: false,
      exited: false,
    }))
  }

  private buildSnapshot(): SimulationSnapshot {
    const workers: WorkerStatus[] = this.workers.map((worker) => ({
      id: worker.id,
      model: worker.model,
      port: worker.port,
      pid: worker.pid,
      state: worker.state,
      healthy: worker.state === 'healthy',
      inFlight: worker.inFlight,
      served: worker.served,
      estTokensInFlight: worker.estTokensInFlight,
      consecutiveFailures: worker.consecutiveFailures,
      lastCheck: worker.lastCheck,
      lastError: worker.lastError,
      healthFailing: worker.healthFailing,
    }))
    const models = [...new Set(workers.filter((w) => w.healthy).map((w) => w.model))].sort()
    return { workers, models }
  }

  private emit(): void {
    this.snapshot = this.buildSnapshot()
    this.listeners.forEach((listener) => listener())
  }

  /** Called on a 1 s UI cadence; health probes run at the verified 2 s interval. */
  tick(now = Date.now()): void {
    let changed = false

    const remaining: ActiveRequest[] = []
    for (const request of this.active) {
      if (now >= request.endsAt) {
        const worker = this.workers.find((candidate) => candidate.id === request.workerId)
        if (worker) {
          worker.inFlight = Math.max(0, worker.inFlight - 1)
          worker.estTokensInFlight = Math.max(0, worker.estTokensInFlight - request.tokens)
        }
        changed = true
      } else {
        remaining.push(request)
      }
    }
    this.active = remaining

    if (now - this.lastProbeAt >= HEALTH_PROBE_MS) {
      this.lastProbeAt = now
      for (const worker of this.workers) this.applyProbe(worker, now, worker.healthFailing)
      changed = true
    }

    if (changed) this.emit()
  }

  private applyProbe(worker: SimWorker, now: number, failing: boolean): void {
    worker.lastCheck = new Date(now).toISOString()

    if (worker.exited) {
      worker.state = 'unhealthy'
      worker.healthy = false
      worker.lastError = 'process exited'
      return
    }

    if (!failing) {
      worker.consecutiveFailures = 0
      worker.lastError = ''
      if (worker.state !== 'healthy') {
        worker.state = 'healthy'
        worker.healthy = true
      }
      return
    }

    if (worker.state === 'starting') {
      if (now - worker.startedAt >= STARTING_GRACE_MS) {
        worker.state = 'unhealthy'
        worker.healthy = false
        worker.lastError = 'readiness grace period expired'
      }
      return
    }

    if (worker.state === 'healthy') {
      worker.consecutiveFailures += 1
      if (worker.consecutiveFailures >= FAILURE_THRESHOLD) {
        worker.state = 'unhealthy'
        worker.healthy = false
        worker.lastError = 'consecutive health check failures'
      }
    }
  }

  send(model: string, estTokens: number): SimSendResult {
    const known = this.workers.filter((worker) => worker.model === model)
    if (known.length === 0) {
      return { ok: false, status: 404, message: `unknown model ${JSON.stringify(model)}` }
    }

    const healthy = known.filter((worker) => worker.state === 'healthy')
    if (healthy.length === 0) {
      const starting = known.some((worker) => worker.state === 'starting')
      return {
        ok: false,
        status: 503,
        message: starting
          ? `workers for ${JSON.stringify(model)} are still starting`
          : `no healthy worker for model ${JSON.stringify(model)}`,
      }
    }

    const cursor = this.rr.get(model) ?? 0
    const index = cursor % healthy.length
    const worker = healthy[index]
    if (!worker) {
      return { ok: false, status: 503, message: `no healthy worker for model ${JSON.stringify(model)}` }
    }

    this.rr.set(model, cursor + 1)
    worker.served += 1
    worker.inFlight += 1
    worker.estTokensInFlight += estTokens
    this.active.push({
      workerId: worker.id,
      tokens: estTokens,
      endsAt: Date.now() + REQUEST_DURATION_MS,
    })
    this.nextRequestId += 1
    this.emit()

    return {
      ok: true,
      status: 200,
      workerId: worker.id,
      model: worker.model,
      healthyCandidates: healthy.length,
      roundRobinIndex: index,
    }
  }

  kill(id: string): void {
    const worker = this.workers.find((candidate) => candidate.id === id)
    if (!worker) return
    worker.exited = true
    worker.healthFailing = false
    worker.state = 'unhealthy'
    worker.healthy = false
    worker.lastError = 'process exited'
    worker.lastCheck = new Date().toISOString()
    this.emit()
  }

  revive(id: string): void {
    const worker = this.workers.find((candidate) => candidate.id === id)
    if (!worker) return
    worker.exited = false
    worker.healthFailing = false
    worker.state = 'starting'
    worker.healthy = false
    worker.consecutiveFailures = 0
    worker.lastError = ''
    worker.startedAt = Date.now()
    worker.lastCheck = new Date().toISOString()
    this.emit()
  }

  /** Toggle a failing /health endpoint. Upcoming probes accumulate failures. */
  toggleHealthFailing(id: string): void {
    const worker = this.workers.find((candidate) => candidate.id === id)
    if (!worker || worker.exited) return
    worker.healthFailing = !worker.healthFailing
    this.emit()
  }

  reset(): void {
    this.workers = this.createWorkers()
    this.active = []
    this.rr.clear()
    this.nextRequestId = 1
    this.lastProbeAt = Date.now()
    this.emit()
  }

  requestCounter(): number {
    return this.nextRequestId
  }
}

export type { WorkerState }

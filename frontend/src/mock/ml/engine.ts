import { ML_WEIGHTS } from './weights'
import type { MLWeights } from './weights'

export type MLWorkerState = 'starting' | 'healthy' | 'unhealthy'

export interface MLWorker {
  id: string
  model: string
  state: MLWorkerState
  cpuOnly: boolean
  inFlight: number
  served: number
  latencyMs: number
  cpuPct: number
  gpuPct: number | null
  lastError: string
}

export interface MLRawMetrics {
  latencyMs: number
  inFlight: number
  cpuPct: number
  gpuPct: number | null
}

export interface MLNormalized {
  latency: number
  queue: number
  cpu: number
  gpu: number
}

export interface MLCandidate {
  workerId: string
  model: string
  state: MLWorkerState
  raw: MLRawMetrics
  normalized: MLNormalized | null
  score: number | null
  selected: boolean
}

export interface MLDecision {
  id: number
  at: number
  model: string
  status: 200 | 404 | 503
  message: string
  candidates: MLCandidate[]
  chosen: string | null
}

export interface MLSnapshot {
  workers: MLWorker[]
  decisions: MLDecision[]
  weights: MLWeights
  models: string[]
  lastChosen: string | null
}

interface ActiveRequest {
  workerId: string
  endsAt: number
}

interface InternalWorker extends MLWorker {
  readyAt: number
}

interface Spec {
  id: string
  model: string
  cpuOnly: boolean
  latencyMs: number
  cpuPct: number
  gpuPct: number | null
}

const SPECS: Spec[] = [
  { id: 'resnet-1', model: 'resnet50', cpuOnly: false, latencyMs: 42, cpuPct: 18, gpuPct: 24 },
  { id: 'resnet-2', model: 'resnet50', cpuOnly: false, latencyMs: 51, cpuPct: 27, gpuPct: 38 },
  // CPU-only worker: GPU data is n/a, so gpu_score is 0 and the UI says so.
  { id: 'resnet-3', model: 'resnet50', cpuOnly: true, latencyMs: 68, cpuPct: 33, gpuPct: null },
  { id: 'yolo-1', model: 'yolov8', cpuOnly: false, latencyMs: 29, cpuPct: 22, gpuPct: 31 },
  { id: 'yolo-2', model: 'yolov8', cpuOnly: false, latencyMs: 36, cpuPct: 41, gpuPct: 55 },
]

const MAX_DECISIONS = 12
const REQUEST_DURATION_MS = 1600
const REVIVE_MS = 2000

function mulberry32(seed: number): () => number {
  let state = seed >>> 0
  return () => {
    state = (state + 0x6d2b79f5) >>> 0
    let t = state
    t = Math.imul(t ^ (t >>> 15), t | 1)
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61)
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296
  }
}

function clamp(value: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, value))
}

function round(value: number, digits = 3): number {
  const factor = 10 ** digits
  return Math.round(value * factor) / factor
}

function toRaw(worker: MLWorker): MLRawMetrics {
  return {
    latencyMs: worker.latencyMs,
    inFlight: worker.inFlight,
    cpuPct: worker.cpuPct,
    gpuPct: worker.gpuPct,
  }
}

/**
 * Deterministic ML routing mock. Model first, then health, then weighted score
 * (lowest wins). Unhealthy workers are never selected. Zero network calls.
 */
export class MockMLEngine {
  private workers: InternalWorker[]
  private active: ActiveRequest[] = []
  private decisions: MLDecision[] = []
  private listeners = new Set<() => void>()
  private snapshot: MLSnapshot
  private random = mulberry32(0x5eed)
  private nextDecisionId = 1
  private lastChosen: string | null = null

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

  getSnapshot = (): MLSnapshot => this.snapshot

  private createWorkers(): InternalWorker[] {
    return SPECS.map((spec) => ({
      id: spec.id,
      model: spec.model,
      state: 'healthy',
      cpuOnly: spec.cpuOnly,
      inFlight: 0,
      served: 0,
      latencyMs: spec.latencyMs,
      cpuPct: spec.cpuPct,
      gpuPct: spec.gpuPct,
      lastError: '',
      readyAt: 0,
    }))
  }

  private buildSnapshot(): MLSnapshot {
    const workers = this.workers.map((worker) => ({
      id: worker.id,
      model: worker.model,
      state: worker.state,
      cpuOnly: worker.cpuOnly,
      inFlight: worker.inFlight,
      served: worker.served,
      latencyMs: worker.latencyMs,
      cpuPct: worker.cpuPct,
      gpuPct: worker.gpuPct,
      lastError: worker.lastError,
    }))
    const models = [...new Set(workers.map((worker) => worker.model))].sort()
    return {
      workers,
      decisions: this.decisions,
      weights: ML_WEIGHTS,
      models,
      lastChosen: this.lastChosen,
    }
  }

  private emit(): void {
    this.snapshot = this.buildSnapshot()
    this.listeners.forEach((listener) => listener())
  }

  /** 1 s UI cadence: finish in-flight work, revive starting workers, drift metrics. */
  tick(now = Date.now()): void {
    let changed = false

    const remaining: ActiveRequest[] = []
    for (const request of this.active) {
      if (now >= request.endsAt) {
        const worker = this.workers.find((candidate) => candidate.id === request.workerId)
        if (worker) worker.inFlight = Math.max(0, worker.inFlight - 1)
        changed = true
      } else {
        remaining.push(request)
      }
    }
    this.active = remaining

    for (const worker of this.workers) {
      if (worker.state === 'starting' && now >= worker.readyAt) {
        worker.state = 'healthy'
        worker.lastError = ''
        changed = true
      }
      if (worker.state !== 'unhealthy') {
        const previous = `${worker.latencyMs}:${worker.cpuPct}:${worker.gpuPct}`
        worker.latencyMs = Math.round(clamp(worker.latencyMs + (this.random() - 0.5) * 6, 12, 600))
        worker.cpuPct = Math.round(clamp(worker.cpuPct + (this.random() - 0.5) * 4, 4, 99))
        if (worker.gpuPct !== null) {
          worker.gpuPct = Math.round(clamp(worker.gpuPct + (this.random() - 0.5) * 4, 0, 99))
        }
        if (previous !== `${worker.latencyMs}:${worker.cpuPct}:${worker.gpuPct}`) changed = true
      }
    }

    if (changed) this.emit()
  }

  private scoreWorkers(
    healthy: InternalWorker[],
    modelWorkers: InternalWorker[],
  ): { candidates: MLCandidate[]; chosen: InternalWorker | null } {
    const maxLatency = Math.max(...healthy.map((worker) => worker.latencyMs), 0)
    const maxInFlight = Math.max(...healthy.map((worker) => worker.inFlight), 0)

    const scored = healthy.map((worker) => {
      const normalized: MLNormalized = {
        latency: maxLatency > 0 ? worker.latencyMs / maxLatency : 0,
        queue: maxInFlight > 0 ? worker.inFlight / maxInFlight : 0,
        cpu: worker.cpuPct / 100,
        gpu: worker.gpuPct === null ? 0 : worker.gpuPct / 100,
      }
      const score =
        ML_WEIGHTS.latency * normalized.latency +
        ML_WEIGHTS.queue * normalized.queue +
        ML_WEIGHTS.gpu * normalized.gpu +
        ML_WEIGHTS.cpu * normalized.cpu
      return { worker, normalized, score: round(score, 4) }
    })

    const ordered = [...scored].sort(
      (a, b) => a.score - b.score || a.worker.id.localeCompare(b.worker.id),
    )
    const chosen = ordered[0]?.worker ?? null

    const candidates: MLCandidate[] = modelWorkers.map((worker) => {
      const match = scored.find((entry) => entry.worker.id === worker.id)
      return {
        workerId: worker.id,
        model: worker.model,
        state: worker.state,
        raw: toRaw(worker),
        normalized: match ? match.normalized : null,
        score: match ? match.score : null,
        selected: chosen?.id === worker.id,
      }
    })

    return { candidates, chosen }
  }

  send(model: string): MLDecision {
    const modelWorkers = this.workers.filter((worker) => worker.model === model)
    const healthy = modelWorkers.filter((worker) => worker.state === 'healthy')

    if (modelWorkers.length === 0) {
      const decision: MLDecision = {
        id: this.nextDecisionId++,
        at: Date.now(),
        model,
        status: 404,
        message: `unknown model ${JSON.stringify(model)}`,
        candidates: [],
        chosen: null,
      }
      this.record(decision)
      return decision
    }

    if (healthy.length === 0) {
      const decision: MLDecision = {
        id: this.nextDecisionId++,
        at: Date.now(),
        model,
        status: 503,
        message: `no healthy worker for model ${JSON.stringify(model)}`,
        candidates: modelWorkers.map((worker) => ({
          workerId: worker.id,
          model: worker.model,
          state: worker.state,
          raw: toRaw(worker),
          normalized: null,
          score: null,
          selected: false,
        })),
        chosen: null,
      }
      this.record(decision)
      return decision
    }

    const { candidates, chosen } = this.scoreWorkers(healthy, modelWorkers)
    if (chosen) {
      chosen.served += 1
      chosen.inFlight += 1
      this.active.push({ workerId: chosen.id, endsAt: Date.now() + REQUEST_DURATION_MS })
      this.lastChosen = chosen.id
    }

    const decision: MLDecision = {
      id: this.nextDecisionId++,
      at: Date.now(),
      model,
      status: 200,
      message: chosen ? `selected ${chosen.id} (lowest score)` : 'no healthy worker',
      candidates,
      chosen: chosen?.id ?? null,
    }
    this.record(decision)
    return decision
  }

  private record(decision: MLDecision): void {
    this.decisions = [decision, ...this.decisions].slice(0, MAX_DECISIONS)
    this.emit()
  }

  /** Raise a healthy worker's load. High load never changes health. */
  load(id: string): void {
    const worker = this.workers.find((candidate) => candidate.id === id)
    if (!worker || worker.state === 'unhealthy') return
    worker.cpuPct = Math.round(clamp(worker.cpuPct + 22, 0, 99))
    worker.latencyMs = Math.round(clamp(worker.latencyMs + 85, 12, 600))
    worker.inFlight += 3
    if (worker.gpuPct !== null) worker.gpuPct = Math.round(clamp(worker.gpuPct + 18, 0, 99))
    const now = Date.now()
    for (const delay of [2200, 3600, 5000]) {
      this.active.push({ workerId: worker.id, endsAt: now + delay })
    }
    this.emit()
  }

  kill(id: string): void {
    const worker = this.workers.find((candidate) => candidate.id === id)
    if (!worker) return
    worker.state = 'unhealthy'
    worker.inFlight = 0
    worker.lastError = 'worker stopped'
    this.emit()
  }

  revive(id: string): void {
    const worker = this.workers.find((candidate) => candidate.id === id)
    if (!worker) return
    worker.state = 'starting'
    worker.lastError = ''
    worker.readyAt = Date.now() + REVIVE_MS
    this.emit()
  }

  reset(): void {
    this.workers = this.createWorkers()
    this.active = []
    this.decisions = []
    this.random = mulberry32(0x5eed)
    this.nextDecisionId = 1
    this.lastChosen = null
    this.emit()
  }
}

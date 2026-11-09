export type WorkerState = 'starting' | 'healthy' | 'unhealthy'

export type Tone = 'neutral' | 'ok' | 'warn' | 'bad' | 'accent' | 'ml'

/** Frontend worker shape. Mirrors GET /status; both live and simulated hooks use it. */
export interface WorkerStatus {
  id: string
  model: string
  port: number
  pid: number
  state: WorkerState
  healthy: boolean
  inFlight: number
  served: number
  estTokensInFlight: number
  consecutiveFailures: number
  lastCheck: string
  lastError: string
  /** Simulation-only: the /health endpoint is currently failing. Absent for live data. */
  healthFailing?: boolean
}

/** Raw shape of GET /status, matching internal/api/types.go JSON tags exactly. */
export interface RawWorkerStatus {
  id: string
  model: string
  port: number
  pid: number
  state: string
  healthy: boolean
  in_flight: number
  served: number
  est_tokens_in_flight: number
  consecutive_failures: number
  last_check: string
  last_error: string
}

export interface RawStatusResponse {
  workers: RawWorkerStatus[] | null
}

export interface RawModelList {
  object: string
  data: Array<{ id: string; object: string }> | null
}

export interface ChatMessage {
  role: 'system' | 'user' | 'assistant'
  content: string
}

export interface ChatRequestBody {
  model: string
  messages: ChatMessage[]
  max_tokens?: number
}

export interface ChatUsage {
  prompt_tokens: number
  completion_tokens: number
  total_tokens: number
}

export interface ChatChoice {
  index: number
  message: ChatMessage
  finish_reason: string
}

export interface ChatResponse {
  id: string
  object: string
  model: string
  choices: ChatChoice[]
  usage: ChatUsage
}

export interface ChatMeta {
  worker: string | null
  model: string | null
  estInput: number | null
  estOutput: number | null
  estTotal: number | null
}

export interface ErrorBody {
  error?: {
    message?: string
    type?: string
  }
}

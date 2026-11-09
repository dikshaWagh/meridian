import type {
  ChatMeta,
  ChatRequestBody,
  ChatResponse,
  ErrorBody,
  RawModelList,
  RawStatusResponse,
  RawWorkerStatus,
  WorkerState,
  WorkerStatus,
} from './types'

const API_BASE = (import.meta.env.VITE_API_BASE as string | undefined)?.replace(/\/$/, '') ?? ''

export class ApiError extends Error {
  status: number
  type: string

  constructor(status: number, type: string, message: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.type = type
  }
}

export class NetworkError extends Error {
  constructor(message = 'Could not reach Meridian.') {
    super(message)
    this.name = 'NetworkError'
  }
}

function parseState(value: string): WorkerState {
  return value === 'healthy' || value === 'starting' ? value : 'unhealthy'
}

function mapWorker(raw: RawWorkerStatus): WorkerStatus {
  return {
    id: raw.id,
    model: raw.model,
    port: raw.port,
    pid: raw.pid,
    state: parseState(raw.state),
    healthy: raw.healthy,
    inFlight: raw.in_flight,
    served: raw.served,
    estTokensInFlight: raw.est_tokens_in_flight,
    consecutiveFailures: raw.consecutive_failures,
    lastCheck: raw.last_check,
    lastError: raw.last_error,
  }
}

async function getResponse(path: string, signal?: AbortSignal): Promise<Response> {
  try {
    return await fetch(`${API_BASE}${path}`, {
      signal,
      headers: { Accept: 'application/json' },
    })
  } catch (error) {
    if (error instanceof DOMException && error.name === 'AbortError') throw error
    throw new NetworkError()
  }
}

async function readError(response: Response): Promise<Error> {
  const contentType = response.headers.get('content-type') ?? ''
  let type = 'http_error'
  let message = `Request failed with HTTP ${response.status}`
  try {
    const body = (await response.json()) as ErrorBody
    if (body.error?.type) type = body.error.type
    if (body.error?.message) message = body.error.message
  } catch {
    // The Vite proxy returns a non-JSON page when the backend is unreachable.
    if (response.status >= 500 && !contentType.includes('application/json')) {
      return new NetworkError(
        'Could not reach the Meridian balancer. The dev proxy may be unable to connect to the backend.',
      )
    }
  }
  return new ApiError(response.status, type, message)
}

export async function fetchStatus(signal?: AbortSignal): Promise<WorkerStatus[]> {
  const response = await getResponse('/status', signal)
  if (!response.ok) throw await readError(response)
  const body = (await response.json()) as RawStatusResponse
  return (body.workers ?? []).map(mapWorker)
}

export async function fetchModels(signal?: AbortSignal): Promise<string[]> {
  const response = await getResponse('/v1/models', signal)
  if (!response.ok) throw await readError(response)
  const body = (await response.json()) as RawModelList
  return (body.data ?? []).map((model) => model.id)
}

function readIntHeader(headers: Headers, name: string): number | null {
  const value = headers.get(name)
  if (value === null) return null
  const parsed = Number.parseInt(value, 10)
  return Number.isFinite(parsed) ? parsed : null
}

export interface ChatResult {
  data: ChatResponse
  meta: ChatMeta
  status: number
}

export async function sendChat(body: ChatRequestBody, signal?: AbortSignal): Promise<ChatResult> {
  let response: Response
  try {
    response = await fetch(`${API_BASE}/v1/chat/completions`, {
      method: 'POST',
      signal,
      headers: {
        'Content-Type': 'application/json',
        Accept: 'application/json',
      },
      body: JSON.stringify(body),
    })
  } catch (error) {
    if (error instanceof DOMException && error.name === 'AbortError') throw error
    throw new NetworkError('Could not reach the Meridian balancer.')
  }

  if (!response.ok) throw await readError(response)

  const meta: ChatMeta = {
    worker: response.headers.get('X-Meridian-Worker'),
    model: response.headers.get('X-Meridian-Model'),
    estInput: readIntHeader(response.headers, 'X-Meridian-Est-Input-Tokens'),
    estOutput: readIntHeader(response.headers, 'X-Meridian-Est-Output-Tokens'),
    estTotal: readIntHeader(response.headers, 'X-Meridian-Est-Total-Tokens'),
  }

  let data: ChatResponse
  try {
    data = (await response.json()) as ChatResponse
  } catch {
    throw new ApiError(response.status, 'invalid_response', 'The response body was not valid JSON.')
  }

  return { data, meta, status: response.status }
}

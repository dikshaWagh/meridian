import type { Catalog, ChatTurn, MLStatus, OCRResponse, SystemStatus } from './types'

export class ApiError extends Error {
  readonly status: number
  readonly type: string

  constructor(status: number, type: string, message: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.type = type
  }
}

export function errorMessage(err: unknown): string {
  if (err instanceof DOMException && err.name === 'AbortError') return 'Stopped'
  if (err instanceof Error) return err.message
  return String(err)
}

async function toApiError(res: Response): Promise<ApiError> {
  let type = 'http_error'
  let message = `${res.status} ${res.statusText}`.trim()
  try {
    const body = (await res.json()) as { error?: { message?: string; type?: string } }
    if (body.error) {
      type = body.error.type ?? type
      message = body.error.message ?? message
    }
  } catch {
    // Non-JSON error body: keep the status line.
  }
  return new ApiError(res.status, type, message)
}

async function getJSON<T>(url: string): Promise<T> {
  const res = await fetch(url, { cache: 'no-store' })
  if (!res.ok) throw await toApiError(res)
  return (await res.json()) as T
}

export const fetchCatalog = () => getJSON<Catalog>('/v1/catalog')
export const fetchClusterStatus = () => getJSON<SystemStatus>('/status')
export const fetchMLStatus = () => getJSON<MLStatus>('/ml/status')

export interface ChatRequest {
  model: string
  messages: ChatTurn[]
  stream: boolean
  policy?: string
  signal?: AbortSignal
  onDelta: (text: string) => void
}

export interface ChatResult {
  worker: string | null
  failover: string | null
}

// sendChat posts the full conversation on every turn. The gateway does not
// need a conversation id for this, so every request is self-contained.
export async function sendChat(req: ChatRequest): Promise<ChatResult> {
  const headers: Record<string, string> = { 'Content-Type': 'application/json' }
  if (req.policy) headers['X-Meridian-Routing-Policy'] = req.policy

  const res = await fetch('/v1/chat/completions', {
    method: 'POST',
    headers,
    body: JSON.stringify({ model: req.model, messages: req.messages, stream: req.stream }),
    signal: req.signal,
  })
  if (!res.ok) throw await toApiError(res)

  const result: ChatResult = {
    worker: res.headers.get('X-Meridian-Worker'),
    failover: res.headers.get('X-Meridian-Failover'),
  }

  const contentType = res.headers.get('content-type') ?? ''
  if (!contentType.includes('text/event-stream') || !res.body) {
    const body = (await res.json()) as { choices?: { message?: { content?: string } }[] }
    req.onDelta(body.choices?.[0]?.message?.content ?? '')
    return result
  }

  await readSSE(res.body, req.onDelta)
  return result
}

async function readSSE(body: ReadableStream<Uint8Array>, onDelta: (text: string) => void) {
  const reader = body.getReader()
  const decoder = new TextDecoder()
  let buffer = ''

  const handleFrame = (frame: string) => {
    for (const line of frame.split('\n')) {
      const trimmed = line.trim()
      if (!trimmed.startsWith('data:')) continue
      const data = trimmed.slice(5).trim()
      if (!data || data === '[DONE]') continue
      try {
        const chunk = JSON.parse(data) as { choices?: { delta?: { content?: string } }[] }
        const delta = chunk.choices?.[0]?.delta?.content
        if (delta) onDelta(delta)
      } catch {
        // Ignore a malformed frame rather than failing the whole reply.
      }
    }
  }

  for (;;) {
    const { done, value } = await reader.read()
    if (done) break
    buffer += decoder.decode(value, { stream: true })
    let boundary = buffer.indexOf('\n\n')
    while (boundary >= 0) {
      handleFrame(buffer.slice(0, boundary))
      buffer = buffer.slice(boundary + 2)
      boundary = buffer.indexOf('\n\n')
    }
  }
  if (buffer.trim()) handleFrame(buffer)
}

export async function runOCR(file: File, model: string): Promise<OCRResponse> {
  const form = new FormData()
  form.append('file', file)
  if (model) form.append('model', model)
  const res = await fetch('/v1/ocr', { method: 'POST', body: form })
  if (!res.ok) throw await toApiError(res)
  return (await res.json()) as OCRResponse
}

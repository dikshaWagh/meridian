import type { AdminCluster, Catalog, ChatTurn, RouteEvent } from './types'

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

function bearer(token: string): Record<string, string> {
  return { Authorization: `Bearer ${token}` }
}

export const fetchCatalog = () => getJSON<Catalog>('/v1/catalog')

// checkAdmin returns true when the token is accepted. It throws for network
// errors and for an admin console that is disabled on this leader.
export async function checkAdmin(token: string): Promise<boolean> {
  const res = await fetch('/v1/admin/session', { headers: bearer(token), cache: 'no-store' })
  if (res.status === 401) return false
  if (!res.ok) throw await toApiError(res)
  return true
}

export async function fetchAdminCluster(token: string): Promise<AdminCluster> {
  const res = await fetch('/v1/admin/cluster', { headers: bearer(token), cache: 'no-store' })
  if (!res.ok) throw await toApiError(res)
  return (await res.json()) as AdminCluster
}

export async function setSystemPolicy(token: string, name: string): Promise<void> {
  const res = await fetch('/v1/policies/default', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json', ...bearer(token) },
    body: JSON.stringify({ name }),
  })
  if (!res.ok) throw await toApiError(res)
}

// streamAdminEvents follows the route event stream until the signal aborts.
export async function streamAdminEvents(
  token: string,
  onEvent: (e: RouteEvent) => void,
  signal: AbortSignal,
): Promise<void> {
  const res = await fetch('/v1/admin/events', { headers: bearer(token), signal, cache: 'no-store' })
  if (!res.ok || !res.body) throw await toApiError(res)
  const reader = res.body.getReader()
  const decoder = new TextDecoder()
  let buffer = ''
  for (;;) {
    const { done, value } = await reader.read()
    if (done) break
    buffer += decoder.decode(value, { stream: true })
    let boundary = buffer.indexOf('\n\n')
    while (boundary >= 0) {
      for (const line of buffer.slice(0, boundary).split('\n')) {
        if (!line.startsWith('data:')) continue
        try {
          onEvent(JSON.parse(line.slice(5).trim()) as RouteEvent)
        } catch {
          // Skip a malformed event; the stream stays up.
        }
      }
      buffer = buffer.slice(boundary + 2)
      boundary = buffer.indexOf('\n\n')
    }
  }
}

export interface ChatRequest {
  model: string
  messages: ChatTurn[]
  stream: boolean
  signal?: AbortSignal
  onDelta: (text: string) => void
}

export interface ChatResult {
  worker: string | null
  failover: string | null
}

// sendChat posts the full conversation on every turn. Clients never pick a
// routing policy; the system policy applies to every request.
export async function sendChat(req: ChatRequest): Promise<ChatResult> {
  const res = await fetch('/v1/chat/completions', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
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

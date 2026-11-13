import type {
  Catalog,
  ImageRequest,
  ImageResult,
  OCRResult,
  SystemState,
  Mode,
} from './types'

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

function errorMessage(err: unknown): string {
  if (err instanceof Error) return err.message
  return String(err)
}

async function parseError(res: Response): Promise<ApiError> {
  let type = 'http_error'
  let message = `${res.status} ${res.statusText}`.trim()
  try {
    const body = (await res.json()) as { error?: { message?: string; type?: string } }
    if (body?.error) {
      type = body.error.type ?? type
      message = body.error.message ?? message
    }
  } catch {
    // Keep the status line as the message.
  }
  return new ApiError(res.status, type, message)
}

async function asJson<T>(res: Response): Promise<T> {
  if (!res.ok) throw await parseError(res)
  return (await res.json()) as T
}

export function fetchState(): Promise<SystemState> {
  return fetch('/v1/system/state', { cache: 'no-store' }).then((r) => asJson<SystemState>(r))
}

export function fetchCatalog(): Promise<Catalog> {
  return fetch('/v1/catalog', { cache: 'no-store' }).then((r) => asJson<Catalog>(r))
}

export function requestMode(mode: Exclude<Mode, 'idle'>): Promise<SystemState> {
  return fetch('/v1/system/mode', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ mode }),
  }).then((r) => asJson<SystemState>(r))
}

export function setDefaultPolicy(name: string): Promise<unknown> {
  return fetch('/v1/policies/default', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name }),
  }).then((r) => asJson<unknown>(r))
}

// subscribeSystem streams arbiter state transitions. EventSource reconnects on
// its own; the callback reports connection health for the status pill.
export function subscribeSystem(
  onState: (state: SystemState) => void,
  onConnection?: (connected: boolean) => void,
): () => void {
  const source = new EventSource('/v1/system/events')
  source.addEventListener('open', () => onConnection?.(true))
  source.addEventListener('state', (event) => {
    try {
      const payload = JSON.parse((event as MessageEvent).data) as { state: SystemState }
      if (payload?.state) onState(payload.state)
    } catch {
      // Ignore malformed frames and keep the stream alive.
    }
  })
  source.addEventListener('error', () => onConnection?.(false))
  return () => source.close()
}

interface StreamChatOptions {
  model: string
  messages: { role: string; content: string }[]
  policy?: string
  signal?: AbortSignal
  onDelta: (delta: string) => void
}

// streamChat speaks the OpenAI-compatible SSE dialect. llama-server streams
// natively, so the UI has one code path for both JSON and SSE replies.
export async function streamChat(options: StreamChatOptions): Promise<string> {
  const headers: Record<string, string> = { 'Content-Type': 'application/json' }
  if (options.policy) headers['X-Meridian-Routing-Policy'] = options.policy

  const res = await fetch('/v1/chat/completions', {
    method: 'POST',
    headers,
    body: JSON.stringify({
      model: options.model,
      messages: options.messages,
      stream: true,
    }),
    signal: options.signal,
  })
  if (!res.ok) throw await parseError(res)

  const contentType = res.headers.get('content-type') ?? ''
  if (!contentType.includes('text/event-stream') || !res.body) {
    const body = await res.json()
    const content: string = body?.choices?.[0]?.message?.content ?? ''
    if (content) options.onDelta(content)
    return content
  }

  const reader = res.body.getReader()
  const decoder = new TextDecoder()
  let buffer = ''
  let content = ''

  const consume = (frame: string) => {
    for (const line of frame.split('\n')) {
      const trimmed = line.trim()
      if (!trimmed.startsWith('data:')) continue
      const data = trimmed.slice(5).trim()
      if (!data || data === '[DONE]') continue
      try {
        const chunk = JSON.parse(data)
        const delta: string = chunk?.choices?.[0]?.delta?.content ?? ''
        if (delta) {
          content += delta
          options.onDelta(delta)
        }
      } catch {
        // A partial JSON frame should not kill the stream.
      }
    }
  }

  while (true) {
    const { done, value } = await reader.read()
    if (done) break
    buffer += decoder.decode(value, { stream: true })
    let boundary = buffer.indexOf('\n\n')
    while (boundary >= 0) {
      consume(buffer.slice(0, boundary))
      buffer = buffer.slice(boundary + 2)
      boundary = buffer.indexOf('\n\n')
    }
  }
  if (buffer.trim()) consume(buffer)
  return content
}

export async function runOCR(file: File): Promise<OCRResult> {
  const form = new FormData()
  form.append('file', file)
  const res = await fetch('/v1/ocr', { method: 'POST', body: form })
  return asJson<OCRResult>(res)
}

export async function generateImage(request: ImageRequest): Promise<ImageResult> {
  const res = await fetch('/v1/images/generations', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(request),
  })
  return asJson<ImageResult>(res)
}

export { errorMessage }

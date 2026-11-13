export type Mode = 'idle' | 'llm' | 'ml'
export type Phase = 'idle' | 'draining' | 'stopping' | 'starting' | 'warming' | 'active' | 'failed'
export type Role = 'user' | 'assistant' | 'system'

export interface SystemState {
  mode: Mode
  target: Mode
  phase: Phase
  switching: boolean
  progress: number
  message: string
  last_error?: string
  available: Mode[]
  in_flight: Record<string, number>
  attempt: number
  exclusive: boolean
  observed_at: string
}

export interface CatalogModel {
  id: string
  object: string
  kind: 'llm' | 'ocr' | 'image-generation'
  state: string
  healthy: boolean
  workers?: number
  healthy_workers?: number
  context_window?: number
  description?: string
}

export interface RoutingPolicy {
  name: string
  description: string
}

export interface Catalog {
  llm: CatalogModel[]
  ml: CatalogModel[]
  routing_policies: RoutingPolicy[]
  default_routing_policy: string
  serving_mode: string
}

export interface ChatMessage {
  id: string
  role: Role
  content: string
  createdAt: number
  model?: string
  pending?: boolean
  error?: string
}

export interface Conversation {
  id: string
  title: string
  model: string
  messages: ChatMessage[]
  updatedAt: number
}

export interface OCRLine {
  text: string
  confidence?: number
  bbox?: { x: number; y: number; width: number; height: number }
}

export interface OCRResult {
  request_id: string
  model: string
  text: string
  lines: OCRLine[]
  processing_ms: number
}

export interface ImageResult {
  id: string
  model: string
  url: string
  width: number
  height: number
  steps: number
  seed: number
  created: number
  processing_ms: number
  prompt?: string
}

export interface ImageRequest {
  prompt: string
  negative_prompt?: string
  width: number
  height: number
  steps: number
  seed?: number
}

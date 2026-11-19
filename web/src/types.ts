export type Role = 'user' | 'assistant' | 'system'

export interface CatalogModel {
  id: string
  object: string
  kind: 'llm' | 'ocr'
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

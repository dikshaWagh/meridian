// Shapes returned by the Meridian gateway. Keep these in sync with
// internal/api/types.go (LLM) and internal/ml/types.go (OCR).

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

export interface WorkerStatus {
  id: string
  model: string
  port: number
  pid: number
  state: string
  healthy: boolean
  in_flight: number
  served: number
  est_tokens_in_flight: number
  tok_per_sec: number
  consecutive_failures: number
  last_check: string
  last_error: string
  sessions: number
}

export interface SystemStatus {
  workers: WorkerStatus[]
  sessionCount: number
}

export interface OCRLine {
  text: string
  confidence?: number
  bbox?: { x: number; y: number; width: number; height: number }
}

export interface OCRResponse {
  request_id: string
  model: string
  text: string
  lines: OCRLine[]
  processing_ms: number
}

export interface MLModel {
  id: string
  object: string
  kind: string
  state: string
  healthy: boolean
  worker_id?: string
}

export interface MLWorker {
  id: string
  kind: string
  model_id: string
  engine: string
  device: string
  pid: number
  port: number
  state: string
  healthy: boolean
  in_flight: number
  concurrency: number
  served: number
  failed: number
  restarts: number
  last_error?: string
  last_inference_ms: number
}

export interface MLResources {
  policy: string
  ram_known: boolean
  ram_available_mb: number
  ram_total_mb: number
  ram_reserved_mb: number
  ram_headroom_mb: number
  probe_error?: string
}

export interface MLStatus {
  enabled: boolean
  models: MLModel[]
  workers: MLWorker[]
  resources: MLResources
}

export type Role = 'user' | 'assistant'

export interface ChatTurn {
  role: Role
  content: string
}

export interface ChatMessage extends ChatTurn {
  id: string
  pending?: boolean
  stopped?: boolean
  error?: string
  worker?: string | null
  failover?: string | null
}

export interface Conversation {
  id: string
  title: string
  updatedAt: number
  messages: ChatMessage[]
}

// Admin-only shapes. They include IP addresses and process IDs, so they are
// fetched only with the admin token and never shown to regular users.
export interface AdminReplica {
  id: string
  model: string
  instance: number
  pid: number
  state: string
  last_state?: string
  healthy: boolean
  in_flight: number
  served: number
  tok_per_sec: number
  endpoint: string
  last_error: string
}

export interface AdminNode {
  node_id: string
  address: string
  http_port: number
  local: boolean
  state: string
  cpu_cores: number
  cpu_percent: number
  ram_used_mb: number
  ram_total_mb: number
  replicas: AdminReplica[]
}

export interface AdminCluster {
  leader_id: string
  policy: string
  policies: RoutingPolicy[]
  nodes: AdminNode[]
}

// RouteEvent is one step of a request's path, streamed to the admin console.
export interface RouteEvent {
  kind: string
  at: string
  request_id: string
  model: string
  policy: string
  node_id: string
  replica_id: string
  phase: 'routing' | 'done' | 'retry' | 'failed'
  attempt: number
}

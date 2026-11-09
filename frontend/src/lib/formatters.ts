import type { Tone, WorkerState } from './types'

const numberFmt = new Intl.NumberFormat('en-US')

export function formatNumber(value: number): string {
  return numberFmt.format(value)
}

export function formatDuration(ms: number): string {
  if (!Number.isFinite(ms)) return '—'
  if (ms < 1) return '<1 ms'
  if (ms < 1000) return `${Math.round(ms)} ms`
  return `${(ms / 1000).toFixed(2)} s`
}

export function formatRelativeTime(iso: string, now = Date.now()): string {
  if (!iso) return 'never'
  const then = Date.parse(iso)
  if (Number.isNaN(then)) return 'never'
  const seconds = Math.max(0, Math.round((now - then) / 1000))
  if (seconds < 2) return 'just now'
  if (seconds < 60) return `${seconds}s ago`
  const minutes = Math.round(seconds / 60)
  if (minutes < 60) return `${minutes}m ago`
  const hours = Math.round(minutes / 60)
  return `${hours}h ago`
}

export function formatIsoTime(iso: string): string {
  if (!iso) return '—'
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) return '—'
  return date.toLocaleTimeString([], { hour12: false })
}

export function stateLabel(state: WorkerState): string {
  switch (state) {
    case 'healthy':
      return 'Healthy'
    case 'starting':
      return 'Starting'
    default:
      return 'Unhealthy'
  }
}

export function stateTone(state: WorkerState): Tone {
  switch (state) {
    case 'healthy':
      return 'ok'
    case 'starting':
      return 'warn'
    default:
      return 'bad'
  }
}

export function truncate(text: string, max = 64): string {
  if (text.length <= max) return text
  return `${text.slice(0, max - 1)}…`
}

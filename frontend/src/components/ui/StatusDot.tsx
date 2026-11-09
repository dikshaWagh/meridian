import { stateLabel } from '../../lib/formatters'
import type { WorkerState } from '../../lib/types'

interface StatusDotProps {
  state: WorkerState
  showLabel?: boolean
  className?: string
}

const dotClass: Record<WorkerState, string> = {
  healthy: 'dot-ok',
  starting: 'dot-warn',
  unhealthy: 'dot-bad',
}

export function StatusDot({ state, showLabel = true, className = '' }: StatusDotProps) {
  return (
    <span className={`inline-flex items-center gap-2 ${className}`}>
      <span className={`status-dot ${dotClass[state]}`} aria-hidden="true" />
      {showLabel ? <span className="text-xs text-ink">{stateLabel(state)}</span> : null}
    </span>
  )
}

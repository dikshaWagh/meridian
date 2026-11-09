import { useMode } from '../../lib/useMode'
import { Badge } from '../ui/Badge'

export function ModeBadge() {
  const { mode } = useMode()
  return mode === 'live' ? (
    <Badge tone="accent" dot>
      Live data
    </Badge>
  ) : (
    <Badge tone="warn" dot>
      Simulated
    </Badge>
  )
}

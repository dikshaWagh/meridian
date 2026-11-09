interface ChamferFrameProps {
  /** Ink is the default panel line; accent is used for selected/focused frames. */
  tone?: 'ink' | 'accent'
  className?: string
}

const corners = [
  { position: 'left-0 top-0', path: 'M 9.5 0.5 L 0.5 9.5' },
  { position: 'right-0 top-0', path: 'M 0.5 0.5 L 9.5 9.5' },
  { position: 'bottom-0 right-0', path: 'M 9.5 0.5 L 0.5 9.5' },
  { position: 'bottom-0 left-0', path: 'M 0.5 0.5 L 9.5 9.5' },
]

/**
 * Draws the two diagonal segments at each chamfered corner of a panel. The
 * panel's own 1px border supplies the straight edges; this only completes the
 * 10px cuts created by the panel clip-path. Decorative only.
 */
export function ChamferFrame({ tone = 'ink', className = '' }: ChamferFrameProps) {
  return (
    <span
      aria-hidden="true"
      className={`pointer-events-none absolute inset-0 z-[1] ${tone === 'accent' ? 'text-accent' : 'text-line'} ${className}`}
    >
      {corners.map((corner) => (
        <svg
          key={corner.position}
          viewBox="0 0 10 10"
          className={`absolute h-[10px] w-[10px] ${corner.position}`}
          fill="none"
        >
          <path d={corner.path} stroke="currentColor" strokeWidth="1" vectorEffect="non-scaling-stroke" />
        </svg>
      ))}
    </span>
  )
}

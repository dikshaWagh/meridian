interface TickStripProps {
  ticks?: number
  className?: string
}

/** A short strip of drafting tick marks. Decorative, never focusable. */
export function TickStrip({ ticks = 13, className = '' }: TickStripProps) {
  const width = ticks * 4
  return (
    <svg
      aria-hidden="true"
      viewBox={`0 0 ${width} 5`}
      width={width}
      height={5}
      className={`pointer-events-none text-dim opacity-70 ${className}`}
      fill="none"
    >
      {Array.from({ length: ticks }, (_, index) => (
        <path
          key={index}
          d={`M ${index * 4 + 0.5} 0 L ${index * 4 + 0.5} ${index % 4 === 0 ? 5 : 3}`}
          stroke="currentColor"
          strokeWidth="1"
          strokeOpacity="0.55"
        />
      ))}
    </svg>
  )
}

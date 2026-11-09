/**
 * Dashed corner marks in slate. Used on mock surfaces so they read as
 * provisional rather than live telemetry.
 */
export function DashedCorners({ className = '' }: { className?: string }) {
  return (
    <span
      aria-hidden="true"
      className={`pointer-events-none absolute inset-[4px] text-ml ${className}`}
    >
      <span className="absolute left-0 top-0 h-2.5 w-2.5 border-l border-t border-dashed border-current" />
      <span className="absolute right-0 top-0 h-2.5 w-2.5 border-r border-t border-dashed border-current" />
      <span className="absolute bottom-0 right-0 h-2.5 w-2.5 border-b border-r border-dashed border-current" />
      <span className="absolute bottom-0 left-0 h-2.5 w-2.5 border-b border-l border-dashed border-current" />
    </span>
  )
}

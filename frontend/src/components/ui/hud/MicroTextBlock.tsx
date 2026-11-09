interface MicroTextBlockProps {
  /** Relative line lengths in percent. */
  lines?: number[]
  className?: string
}

/**
 * Non-semantic placeholder text lines, used only as a drafting detail. The
 * markup is hidden from assistive technology and carries no information.
 */
export function MicroTextBlock({
  lines = [86, 62, 74, 44, 58],
  className = '',
}: MicroTextBlockProps) {
  return (
    <span aria-hidden="true" className={`pointer-events-none flex flex-col gap-[3px] ${className}`}>
      {lines.map((width, index) => (
        <span key={index} className="block h-px bg-current" style={{ width: `${width}%`, opacity: 0.45 }} />
      ))}
    </span>
  )
}

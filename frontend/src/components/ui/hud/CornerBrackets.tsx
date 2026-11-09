/** Small orange corner brackets for the selected or focused item. */
export function CornerBrackets({ className = '' }: { className?: string }) {
  return (
    <span aria-hidden="true" className={`pointer-events-none absolute inset-[3px] ${className}`}>
      <span className="absolute left-0 top-0 h-2.5 w-2.5 border-l border-t border-accent" />
      <span className="absolute right-0 top-0 h-2.5 w-2.5 border-r border-t border-accent" />
      <span className="absolute bottom-0 right-0 h-2.5 w-2.5 border-b border-r border-accent" />
      <span className="absolute bottom-0 left-0 h-2.5 w-2.5 border-b border-l border-accent" />
    </span>
  )
}

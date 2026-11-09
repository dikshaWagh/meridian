export function Logo({ compact = false }: { compact?: boolean }) {
  return (
    <div className="flex items-center gap-2.5">
      <span className="btn-chamfer relative grid h-9 w-9 shrink-0 place-items-center rounded-none border border-line bg-panel-alt">
        <svg viewBox="0 0 24 24" fill="none" className="h-5 w-5 text-accent" aria-hidden="true">
          <path
            d="M4 18c4-1.2 5.6-5 8-12M20 18c-4-1.2-5.6-5-8-12"
            stroke="currentColor"
            strokeWidth="1.4"
            strokeLinecap="round"
          />
          <path d="M7.5 15.5h9" stroke="currentColor" strokeWidth="1" strokeOpacity="0.55" />
          <circle cx="12" cy="8" r="1.4" fill="currentColor" />
        </svg>
      </span>
      <span className="min-w-0">
        <span className="block text-[15px] font-semibold leading-tight tracking-tight text-ink">
          Meridian
        </span>
        {!compact ? (
          <span className="mono-label block text-[9px] leading-tight">LLM control plane</span>
        ) : null}
      </span>
    </div>
  )
}

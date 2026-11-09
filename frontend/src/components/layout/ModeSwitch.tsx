import { useMode } from '../../lib/useMode'
import type { Mode } from '../../lib/modeContext'

const options: Array<{ value: Mode; label: string; dot: string }> = [
  { value: 'live', label: 'Live', dot: 'dot-ok' },
  { value: 'simulation', label: 'Simulated', dot: 'dot-warn' },
]

export function ModeSwitch({ compact = false }: { compact?: boolean }) {
  const { mode, setMode } = useMode()

  return (
    <div>
      <div
        role="group"
        aria-label="Data mode"
        className="flex rounded-none border border-line bg-panel-alt p-1"
      >
        {options.map((option) => {
          const active = mode === option.value
          return (
            <button
              key={option.value}
              type="button"
              aria-pressed={active}
              onClick={() => setMode(option.value)}
              aria-label={`${option.label} mode`}
              className={`flex flex-1 items-center justify-center gap-2 rounded-none px-2.5 py-1.5 text-xs font-medium transition-colors ${
                active ? 'bg-ink text-[#f8f8f8]' : 'text-dim hover:bg-black/[0.05] hover:text-ink'
              }`}
            >
              <span className={`status-dot ${option.dot}`} aria-hidden="true" />
              {compact ? option.value === 'live' ? 'Live' : 'Sim' : option.label}
            </button>
          )
        })}
      </div>
      {!compact ? (
        <p className="mt-2 px-1 text-[11px] leading-relaxed text-dim">
          Live polls the backend. Simulated mirrors the verified routing logic locally.
        </p>
      ) : null}
    </div>
  )
}

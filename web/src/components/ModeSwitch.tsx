import type { Mode, SystemState } from '../types'
import { ImageIcon, Sparkle } from './Icons'

interface ModeSwitchProps {
  state: SystemState | null
  onSelect: (mode: 'llm' | 'ml') => void
}

function shortLabel(mode: Mode): string {
  if (mode === 'ml') return 'ML Studio'
  if (mode === 'llm') return 'Chat'
  return 'Starting'
}

export function ModeSwitch({ state, onSelect }: ModeSwitchProps) {
  const switching = state?.switching ?? false
  const visual = switching ? (state?.target ?? 'idle') : (state?.mode ?? 'idle')

  return (
    <div className="mode-switch" data-mode={visual} role="group" aria-label="Serving mode">
      <div className="mode-thumb" aria-hidden="true" />
      <button
        className={`mode-option ${state?.mode === 'llm' && !switching ? 'active' : ''}`}
        aria-pressed={state?.mode === 'llm'}
        onClick={() => onSelect('llm')}
        disabled={switching}
      >
        <Sparkle size={15} />
        <span>Chat</span>
        {switching && state?.target === 'llm' && <span className="mini-spinner" />}
      </button>
      <button
        className={`mode-option ${state?.mode === 'ml' && !switching ? 'active' : ''}`}
        aria-pressed={state?.mode === 'ml'}
        onClick={() => onSelect('ml')}
        disabled={switching}
      >
        <ImageIcon size={16} />
        <span>ML Studio</span>
        {switching && state?.target === 'ml' && <span className="mini-spinner" />}
      </button>
      {switching && (
        <span className="mode-switching-label" aria-live="polite">
          {shortLabel(state?.target ?? 'idle')} · {state?.progress ?? 0}%
        </span>
      )}
    </div>
  )
}

import type { SystemState } from '../types'
import { ArrowRight, Bolt, Sparkle } from './Icons'

interface TransitionScreenProps {
  state: SystemState | null
  onRetry: (mode: 'llm' | 'ml') => void
}

function modeName(mode: string | undefined): string {
  if (mode === 'ml') return 'ML Studio'
  if (mode === 'llm') return 'Chat'
  return 'Idle'
}

function phaseCopy(state: SystemState): string {
  switch (state.phase) {
    case 'draining':
      return 'Letting active requests finish so no response is cut off.'
    case 'stopping':
      return 'Releasing model memory and stopping the previous subsystem.'
    case 'starting':
      return 'Launching fresh workers for the new subsystem.'
    case 'warming':
      return 'Loading weights and waiting for a healthy worker.'
    case 'failed':
      return state.last_error || 'The switch did not complete.'
    default:
      return 'Preparing the serving mode.'
  }
}

export function TransitionScreen({ state, onRetry }: TransitionScreenProps) {
  const progress = Math.max(0, Math.min(100, state?.progress ?? 0))
  const failed = state?.phase === 'failed'
  const target = state?.target ?? 'llm'
  const from = target === 'ml' ? 'llm' : 'ml'

  return (
    <section className="switching">
      <div className={`switch-card ${failed ? 'failed' : ''}`}>
        <div className="switch-route" aria-hidden="true">
          <span className={`route-chip ${state?.mode === from ? 'current' : ''}`}>
            {modeName(from)}
          </span>
          <ArrowRight size={16} className="route-arrow" />
          <span className="route-chip target">{modeName(target)}</span>
        </div>

        {failed ? (
          <div className="switch-orb failed">
            <Bolt size={26} />
          </div>
        ) : (
          <div
            className="switch-ring"
            style={{
              background: `conic-gradient(var(--accent) ${progress * 3.6}deg, var(--ring-track) 0deg)`,
            }}
            role="progressbar"
            aria-valuenow={progress}
            aria-valuemin={0}
            aria-valuemax={100}
          >
            <div className="switch-ring-inner">
              <span className="switch-percent">{progress}%</span>
              <Sparkle size={18} />
            </div>
          </div>
        )}

        <h2>{failed ? 'The switch failed' : `Switching to ${modeName(target)}`}</h2>
        <p className="switch-copy">{state ? phaseCopy(state) : 'Contacting the server…'}</p>

        {failed ? (
          <div className="switch-actions">
            <button className="primary-button" onClick={() => onRetry(target === 'ml' ? 'ml' : 'llm')}>
              Try again
            </button>
          </div>
        ) : (
          <>
            <div className="progress-track" aria-hidden="true">
              <div className="progress-fill" style={{ width: `${progress}%` }} />
            </div>
            <div className="switch-footnote">
              <span className={`dot ${state?.switching ? 'warn' : 'live'}`} />
              {state?.phase === 'active'
                ? 'Ready'
                : 'Workers are stopped before the next model loads, so only one subsystem holds memory.'}
            </div>
          </>
        )}
      </div>
    </section>
  )
}

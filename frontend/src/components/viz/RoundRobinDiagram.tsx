import { useEffect, useState } from 'react'
import { useReducedMotion } from 'framer-motion'
import { Icon } from '../ui/Icon'
import { CornerBrackets } from '../ui/hud'

const rows = [
  { model: 'qwen2.5-3b', workers: ['w1', 'w2'] },
  { model: 'llama-3.2-3b', workers: ['w3', 'w4'] },
]

export function RoundRobinDiagram() {
  const reduceMotion = useReducedMotion()
  const [tick, setTick] = useState(0)

  useEffect(() => {
    if (reduceMotion) return
    const timer = window.setInterval(() => setTick((value) => value + 1), 1800)
    return () => window.clearInterval(timer)
  }, [reduceMotion])

  return (
    <div>
      <div className="mb-3 flex items-center justify-between gap-3">
        <div className="flex flex-wrap items-center gap-x-2 gap-y-1 font-mono text-[11px] text-dim">
          <span className="rounded-none border border-line bg-panel-alt px-2 py-1 text-ink">
            POST /v1/chat/completions
          </span>
          <Icon name="arrow-right" className="h-3.5 w-3.5" />
          <span>model match</span>
          <Icon name="arrow-right" className="h-3.5 w-3.5" />
          <span>healthy only</span>
        </div>
        <span className="mono-label hidden sm:inline">per-model RR</span>
      </div>

      <div className="space-y-3" aria-label="Per-model round robin illustration">
        {rows.map((row) => {
          const activeIndex = reduceMotion ? 0 : tick % row.workers.length
          return (
            <div key={row.model} className="rounded-none border border-line bg-panel-alt p-3">
              <div className="mb-2 flex items-center justify-between gap-2">
                <span className="font-mono text-xs text-ink">{row.model}</span>
                <span className="font-mono text-[10px] uppercase tracking-wider text-dim">
                  {row.workers.length} healthy
                </span>
              </div>
              <div className="grid grid-cols-2 gap-2">
                {row.workers.map((worker, index) => {
                  const active = index === activeIndex
                  return (
                    <div
                      key={worker}
                      className={`relative flex items-center justify-between rounded-none border px-3 py-2.5 transition-all duration-250 ${
                        active
                          ? 'border-accent bg-accent-soft text-ink'
                          : 'border-line bg-panel text-dim'
                      }`}
                    >
                      {active ? <CornerBrackets /> : null}
                      <span className="flex items-center gap-2 font-mono text-xs">
                        <span className={`status-dot ${active ? 'dot-ok' : 'dot-neutral'}`} aria-hidden="true" />
                        {worker}
                      </span>
                      <span
                        className={`font-mono text-[9px] uppercase tracking-wider ${
                          active ? 'text-ink' : 'text-dim'
                        }`}
                      >
                        {active ? 'selected' : 'waiting'}
                      </span>
                    </div>
                  )
                })}
              </div>
            </div>
          )
        })}
      </div>

      <p className="mt-3 text-[11px] leading-relaxed text-dim">
        Round robin advances independently for each model. A worker in <span className="text-warn">starting</span>{' '}
        or <span className="text-bad">unhealthy</span> state is never selected.
      </p>
    </div>
  )
}

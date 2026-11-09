import type { ReactNode } from 'react'
import { Icon } from './Icon'
import type { IconName } from './Icon'
import { ChamferFrame } from './hud'

interface StatProps {
  label: string
  value: ReactNode
  hint?: string
  icon?: IconName
  accent?: 'cyan' | 'purple' | 'ok' | 'warn' | 'bad' | 'neutral'
}

const accentClass: Record<NonNullable<StatProps['accent']>, string> = {
  cyan: 'text-accent',
  purple: 'text-ml',
  ok: 'text-ok',
  warn: 'text-warn',
  bad: 'text-bad',
  neutral: 'text-dim',
}

export function Stat({ label, value, hint, icon, accent = 'neutral' }: StatProps) {
  return (
    <div className="panel corner-ticks px-4 py-3.5">
      <ChamferFrame />
      <div className="flex items-start justify-between gap-2">
        <span className="mono-label">{label}</span>
        {icon ? <Icon name={icon} className={`h-4 w-4 ${accentClass[accent]}`} /> : null}
      </div>
      <div className="mt-2 font-mono text-2xl font-semibold tracking-tight text-ink">{value}</div>
      {hint ? <p className="mt-1 text-[11px] leading-snug text-dim">{hint}</p> : null}
    </div>
  )
}

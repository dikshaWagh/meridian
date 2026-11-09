import type { ReactNode } from 'react'
import type { Tone } from '../../lib/types'

interface BadgeProps {
  tone?: Tone
  dot?: boolean
  children: ReactNode
  className?: string
}

const dotClass: Record<Tone, string> = {
  neutral: 'dot-neutral',
  ok: 'dot-ok',
  warn: 'dot-warn',
  bad: 'dot-bad',
  accent: 'dot-ok',
  ml: 'dot-neutral',
}

export function Badge({ tone = 'neutral', dot = false, children, className = '' }: BadgeProps) {
  return (
    <span className={`badge badge-${tone} ${className}`}>
      {dot ? <span className={`status-dot ${dotClass[tone]}`} aria-hidden="true" /> : null}
      {children}
    </span>
  )
}

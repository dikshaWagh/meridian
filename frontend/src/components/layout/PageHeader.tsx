import type { ReactNode } from 'react'
import { TickStrip } from '../ui/hud'

interface PageHeaderProps {
  eyebrow: string
  title: string
  description: string
  badges?: ReactNode
  actions?: ReactNode
}

export function PageHeader({ eyebrow, title, description, badges, actions }: PageHeaderProps) {
  return (
    <header className="relative mb-7 flex flex-col gap-4 border-b border-line pb-6 sm:flex-row sm:items-end sm:justify-between">
      <div className="min-w-0">
        <p className="eyebrow">{eyebrow}</p>
        <h1 className="mt-2 text-2xl font-semibold tracking-[-0.02em] text-ink sm:text-3xl">{title}</h1>
        <p className="mt-2 max-w-2xl text-sm leading-relaxed text-dim">{description}</p>
        {badges ? <div className="mt-3 flex flex-wrap items-center gap-2">{badges}</div> : null}
      </div>
      {actions ? <div className="flex shrink-0 flex-wrap items-center gap-2">{actions}</div> : null}
      <TickStrip className="absolute bottom-0 right-0 translate-y-1/2" ticks={16} />
    </header>
  )
}

import type { ReactNode } from 'react'
import { ChamferFrame, TickStrip } from './hud'

interface PanelProps {
  title?: ReactNode
  subtitle?: ReactNode
  actions?: ReactNode
  children: ReactNode
  className?: string
  bodyClassName?: string
}

export function Panel({
  title,
  subtitle,
  actions,
  children,
  className = '',
  bodyClassName = 'p-5',
}: PanelProps) {
  return (
    <section className={`panel ${className}`}>
      <ChamferFrame />
      {title || actions ? (
        <header className="panel-header relative">
          <div className="min-w-0">
            {title ? (
              <h2 className="font-mono text-[11px] font-medium uppercase tracking-[0.14em] text-ink">
                {title}
              </h2>
            ) : null}
            {subtitle ? <p className="mt-1 text-xs leading-relaxed text-dim">{subtitle}</p> : null}
          </div>
          {actions ? <div className="flex flex-wrap items-center gap-2">{actions}</div> : null}
          <TickStrip className="absolute bottom-0 right-5 translate-y-1/2" ticks={10} />
        </header>
      ) : null}
      <div className={bodyClassName}>{children}</div>
    </section>
  )
}

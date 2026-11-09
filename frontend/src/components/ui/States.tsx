import type { ReactNode } from 'react'
import { Button } from './Button'
import { Icon } from './Icon'
import type { IconName } from './Icon'

interface StateProps {
  title: string
  message?: string
  icon?: IconName
  action?: ReactNode
}

export function EmptyState({ title, message, icon = 'layers', action }: StateProps) {
  return (
    <div className="flex flex-col items-center justify-center px-4 py-12 text-center">
      <span className="grid h-11 w-11 place-items-center rounded-none border border-line bg-panel-alt text-dim">
        <Icon name={icon} className="h-5 w-5" />
      </span>
      <h3 className="mt-4 text-sm font-semibold text-ink">{title}</h3>
      {message ? <p className="mt-1.5 max-w-md text-xs leading-relaxed text-dim">{message}</p> : null}
      {action ? <div className="mt-4">{action}</div> : null}
    </div>
  )
}

interface ErrorStateProps extends StateProps {
  onRetry?: () => void
  retryLabel?: string
}

export function ErrorState({
  title,
  message,
  onRetry,
  retryLabel = 'Retry',
  action,
}: ErrorStateProps) {
  return (
    <div
      role="alert"
      className="flex flex-col items-center justify-center rounded-none border border-bad bg-bad-soft px-4 py-10 text-center"
    >
      <span className="grid h-11 w-11 place-items-center rounded-none border border-bad bg-transparent text-bad">
        <Icon name="alert" className="h-5 w-5" />
      </span>
      <h3 className="mt-4 text-sm font-semibold text-ink">{title}</h3>
      {message ? <p className="mt-1.5 max-w-lg text-xs leading-relaxed text-dim">{message}</p> : null}
      {onRetry ? (
        <Button variant="secondary" size="sm" className="mt-4" onClick={onRetry}>
          <Icon name="refresh" className="h-3.5 w-3.5" />
          {retryLabel}
        </Button>
      ) : (
        action
      )}
    </div>
  )
}

export function LoadingBlock({ label = 'Loading' }: { label?: string }) {
  return (
    <div role="status" aria-live="polite" className="flex flex-col gap-3 p-1">
      <span className="sr-only">{label}</span>
      <div className="skeleton h-4 w-32" />
      <div className="skeleton h-16 w-full" />
      <div className="skeleton h-16 w-full" />
      <div className="skeleton h-16 w-11/12" />
    </div>
  )
}

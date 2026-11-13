import type { Toast } from '../hooks/useTheme'
import { Check, X } from './Icons'

interface ToastStackProps {
  toasts: Toast[]
  onDismiss: (id: number) => void
}

export function ToastStack({ toasts, onDismiss }: ToastStackProps) {
  if (toasts.length === 0) return null
  return (
    <div className="toast-stack" role="status" aria-live="polite">
      {toasts.map((toast) => (
        <div key={toast.id} className={`toast ${toast.tone}`}>
          <span className="toast-icon">
            {toast.tone === 'success' ? <Check size={15} /> : <span className="toast-dot" />}
          </span>
          <span className="toast-message">{toast.message}</span>
          <button className="icon-button subtle" onClick={() => onDismiss(toast.id)} aria-label="Dismiss">
            <X size={14} />
          </button>
        </div>
      ))}
    </div>
  )
}

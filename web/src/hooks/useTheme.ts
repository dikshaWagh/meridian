import { useCallback, useEffect, useState } from 'react'

export type Theme = 'light' | 'dark'

function initialTheme(): Theme {
  try {
    const stored = localStorage.getItem('meridian.theme')
    if (stored === 'light' || stored === 'dark') return stored
  } catch {
    // Ignore unavailable storage.
  }
  return window.matchMedia?.('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
}

export function useTheme() {
  const [theme, setTheme] = useState<Theme>(initialTheme)

  useEffect(() => {
    document.documentElement.dataset.theme = theme
    try {
      localStorage.setItem('meridian.theme', theme)
    } catch {
      // Ignore unavailable storage.
    }
  }, [theme])

  const toggle = useCallback(() => {
    setTheme((current) => (current === 'light' ? 'dark' : 'light'))
  }, [])

  return { theme, toggle }
}

export interface Toast {
  id: number
  message: string
  tone: 'info' | 'error' | 'success'
}

let toastSeq = 0

export function useToasts() {
  const [toasts, setToasts] = useState<Toast[]>([])

  const dismiss = useCallback((id: number) => {
    setToasts((current) => current.filter((t) => t.id !== id))
  }, [])

  const push = useCallback(
    (message: string, tone: Toast['tone'] = 'info') => {
      const id = ++toastSeq
      setToasts((current) => [...current.slice(-2), { id, message, tone }])
      window.setTimeout(() => dismiss(id), 5200)
    },
    [dismiss],
  )

  return { toasts, push, dismiss }
}

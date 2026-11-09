import { useCallback, useEffect, useMemo, useState } from 'react'
import type { ReactNode } from 'react'
import { MODE_STORAGE_KEY } from './constants'
import { ModeContext } from './modeContext'
import type { Mode } from './modeContext'

function initialMode(): Mode {
  const fromEnv = import.meta.env.VITE_MODE as string | undefined
  const stored = typeof window !== 'undefined' ? window.localStorage.getItem(MODE_STORAGE_KEY) : null
  if (stored === 'live' || stored === 'simulation') return stored
  return fromEnv === 'simulation' ? 'simulation' : 'live'
}

export function ModeProvider({ children }: { children: ReactNode }) {
  const [mode, setModeState] = useState<Mode>(initialMode)

  useEffect(() => {
    document.documentElement.dataset.mode = mode
  }, [mode])

  const setMode = useCallback((next: Mode) => {
    setModeState(next)
    try {
      window.localStorage.setItem(MODE_STORAGE_KEY, next)
    } catch {
      // Private browsing can reject storage; the in-memory mode still works.
    }
  }, [])

  const value = useMemo(() => ({ mode, setMode }), [mode, setMode])

  return <ModeContext.Provider value={value}>{children}</ModeContext.Provider>
}

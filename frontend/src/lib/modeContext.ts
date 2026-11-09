import { createContext } from 'react'

export type Mode = 'live' | 'simulation'

export interface ModeContextValue {
  mode: Mode
  setMode: (mode: Mode) => void
}

export const ModeContext = createContext<ModeContextValue | null>(null)

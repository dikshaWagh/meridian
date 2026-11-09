import { useContext } from 'react'
import { ModeContext } from './modeContext'
import type { ModeContextValue } from './modeContext'

export function useMode(): ModeContextValue {
  const context = useContext(ModeContext)
  if (!context) throw new Error('useMode must be used inside ModeProvider')
  return context
}

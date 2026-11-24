import { useEffect, useRef, useState } from 'react'

// useCountUp tweens a displayed integer when it changes. Purely presentational:
// the value always settles on the real number.
export function useCountUp(value: number, durationMs = 420): number {
  const [shown, setShown] = useState(value)
  const fromRef = useRef(value)
  const frameRef = useRef<number | null>(null)

  useEffect(() => {
    const from = fromRef.current
    if (from === value) return
    const start = performance.now()
    const reduced = window.matchMedia('(prefers-reduced-motion: reduce)').matches
    if (reduced) {
      fromRef.current = value
      setShown(value)
      return
    }
    const tick = (now: number) => {
      const t = Math.min(1, (now - start) / durationMs)
      const eased = 1 - Math.pow(1 - t, 3)
      const next = Math.round(from + (value - from) * eased)
      setShown(next)
      if (t < 1) {
        frameRef.current = requestAnimationFrame(tick)
      } else {
        fromRef.current = value
        frameRef.current = null
      }
    }
    frameRef.current = requestAnimationFrame(tick)
    return () => {
      if (frameRef.current !== null) cancelAnimationFrame(frameRef.current)
      frameRef.current = null
      fromRef.current = value
    }
  }, [value, durationMs])

  return shown
}

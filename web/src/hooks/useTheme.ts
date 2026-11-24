import { useCallback, useEffect, useRef, useState } from 'react'

export type Theme = 'dark' | 'light'

export const THEME_KEY = 'meridian-theme'

// The inline script in index.html sets data-theme before first paint. This hook
// adopts that value so React has no flash and no disagreement with the DOM.
function initialTheme(): Theme {
  const attr = document.documentElement.dataset.theme
  if (attr === 'dark' || attr === 'light') return attr
  return window.matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark'
}

export function useTheme() {
  const [theme, setTheme] = useState<Theme>(initialTheme)
  const themeRef = useRef(theme)
  themeRef.current = theme

  // Write the attribute before the state update re-renders the tree. The canvas
  // reads its palette from CSS tokens in its own effect; if this waited for an
  // effect it would read the previous theme and keep painting the old canvas.
  const apply = useCallback((next: Theme) => {
    document.documentElement.dataset.theme = next
    themeRef.current = next
    setTheme(next)
  }, [])

  useEffect(() => {
    document.documentElement.dataset.theme = theme
    try {
      localStorage.setItem(THEME_KEY, theme)
    } catch {
      // Storage can be disabled; the theme still applies to this page.
    }
    const meta = document.querySelector('meta[name="theme-color"]')
    if (meta) meta.setAttribute('content', theme === 'dark' ? '#0a0c11' : '#f5f6fa')
  }, [theme])

  const toggle = useCallback(() => {
    apply(themeRef.current === 'dark' ? 'light' : 'dark')
  }, [apply])

  return { theme, toggle }
}

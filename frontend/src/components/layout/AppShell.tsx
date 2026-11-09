import { useEffect, useState } from 'react'
import type { ReactNode } from 'react'
import { AnimatePresence, motion, useReducedMotion } from 'framer-motion'
import { NavLink, useLocation } from 'react-router-dom'
import { Icon } from '../ui/Icon'
import type { IconName } from '../ui/Icon'
import { CornerBrackets, PixelGlyph } from '../ui/hud'
import { Logo } from './Logo'
import { ModeSwitch } from './ModeSwitch'

interface NavItem {
  to: string
  label: string
  icon: IconName
  end?: boolean
  meta?: string
}

const navItems: NavItem[] = [
  { to: '/', label: 'Landing', icon: 'home', end: true },
  { to: '/dashboard', label: 'Dashboard', icon: 'dashboard' },
  { to: '/playground', label: 'Playground', icon: 'playground' },
  { to: '/ml', label: 'ML Preview', icon: 'ml', meta: 'Mock' },
]

function NavList({ onNavigate }: { onNavigate?: () => void }) {
  return (
    <nav className="flex flex-col gap-1" aria-label="Primary">
      {navItems.map((item) => (
        <NavLink
          key={item.to}
          to={item.to}
          end={item.end}
          onClick={onNavigate}
          className={({ isActive }) =>
            `group relative flex items-center gap-3 rounded-none border px-3 py-2.5 text-sm transition-colors ${
              isActive
                ? 'border-accent bg-accent-soft text-ink'
                : 'border-transparent text-dim hover:bg-black/[0.05] hover:text-ink'
            }`
          }
        >
          {({ isActive }) => (
            <>
              {isActive ? <CornerBrackets /> : null}
              <Icon
                name={item.icon}
                className={`h-4 w-4 shrink-0 ${isActive ? 'text-accent' : 'text-dim group-hover:text-ink'}`}
              />
              <span className="flex-1">{item.label}</span>
              {item.meta ? (
                <span className="rounded-none border border-dashed border-ml px-1.5 py-0.5 font-mono text-[9px] uppercase tracking-wider text-ml">
                  {item.meta}
                </span>
              ) : null}
              {isActive ? <span className="status-dot dot-ok" aria-hidden="true" /> : null}
            </>
          )}
        </NavLink>
      ))}
    </nav>
  )
}

function SidebarFooter() {
  return (
    <div className="relative border-t border-line p-4">
      <PixelGlyph className="absolute right-4 top-4" accentIndex={2} />
      <ModeSwitch />
      <p className="mt-4 font-mono text-[10px] uppercase tracking-wider text-dim">
        LLM live · ML preview mock
      </p>
    </div>
  )
}

export function AppShell({ children }: { children: ReactNode }) {
  const [drawerOpen, setDrawerOpen] = useState(false)
  const location = useLocation()
  const reduceMotion = useReducedMotion()

  useEffect(() => {
    setDrawerOpen(false)
  }, [location.pathname])

  useEffect(() => {
    if (!drawerOpen) return
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setDrawerOpen(false)
    }
    document.addEventListener('keydown', onKey)
    const previous = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    return () => {
      document.removeEventListener('keydown', onKey)
      document.body.style.overflow = previous
    }
  }, [drawerOpen])

  return (
    <div className="min-h-screen">
      <aside className="fixed inset-y-0 left-0 z-30 hidden w-[248px] flex-col border-r border-line bg-panel lg:flex">
        <div className="px-5 pb-5 pt-6">
          <Logo />
        </div>
        <div className="hud-rule mx-4" />
        <div className="flex-1 overflow-y-auto px-3 py-5">
          <p className="mono-label mb-3 px-3">Console</p>
          <NavList />
        </div>
        <SidebarFooter />
      </aside>

      <div className="lg:pl-[248px]">
        <header className="sticky top-0 z-20 border-b border-line bg-bg/95 lg:hidden">
          <div className="flex h-[60px] items-center justify-between gap-3 px-4">
            <Logo compact />
            <div className="flex items-center gap-2">
              <div className="w-[150px]">
                <ModeSwitch compact />
              </div>
              <button
                type="button"
                onClick={() => setDrawerOpen(true)}
                aria-label="Open navigation"
                aria-expanded={drawerOpen}
                className="btn-chamfer grid h-9 w-9 place-items-center rounded-none border border-line bg-panel text-dim transition-colors hover:text-ink"
              >
                <Icon name="menu" className="h-5 w-5" />
              </button>
            </div>
          </div>
        </header>

        <main className="mx-auto w-full max-w-shell px-4 py-8 sm:px-6 lg:px-10 lg:py-10">
          {children}
        </main>

        <footer className="mx-auto w-full max-w-shell px-4 pb-8 sm:px-6 lg:px-10">
          <div className="hud-rule mb-4" />
          <div className="flex flex-wrap items-center justify-between gap-2 text-[11px] text-dim">
            <span className="font-mono uppercase tracking-wider">
              Meridian frontend · LLM balancer console
            </span>
            <span className="font-mono uppercase tracking-wider">ML Preview is mock only</span>
          </div>
        </footer>
      </div>

      <AnimatePresence>
        {drawerOpen ? (
          <motion.div
            className="fixed inset-0 z-40 lg:hidden"
            initial={{ opacity: 0 }}
            animate={{ opacity: 1 }}
            exit={{ opacity: 0 }}
            transition={{ duration: reduceMotion ? 0 : 0.2 }}
          >
            <button
              type="button"
              aria-label="Close navigation"
              className="absolute inset-0 bg-black/30"
              onClick={() => setDrawerOpen(false)}
            />
            <motion.div
              role="dialog"
              aria-modal="true"
              aria-label="Navigation"
              className="absolute inset-y-0 left-0 flex w-[280px] flex-col border-r border-line bg-panel"
              initial={{ x: reduceMotion ? 0 : -24, opacity: reduceMotion ? 0 : 1 }}
              animate={{ x: 0, opacity: 1 }}
              exit={{ x: reduceMotion ? 0 : -24, opacity: reduceMotion ? 0 : 1 }}
              transition={{ duration: reduceMotion ? 0 : 0.22, ease: [0.16, 1, 0.3, 1] }}
            >
              <div className="flex items-center justify-between px-5 pb-5 pt-5">
                <Logo />
                <button
                  type="button"
                  onClick={() => setDrawerOpen(false)}
                  aria-label="Close navigation"
                  className="btn-chamfer grid h-9 w-9 place-items-center rounded-none border border-line text-dim hover:text-ink"
                >
                  <Icon name="close" className="h-4 w-4" />
                </button>
              </div>
              <div className="hud-rule mx-4" />
              <div className="flex-1 overflow-y-auto px-3 py-5">
                <p className="mono-label mb-3 px-3">Console</p>
                <NavList onNavigate={() => setDrawerOpen(false)} />
              </div>
              <SidebarFooter />
            </motion.div>
          </motion.div>
        ) : null}
      </AnimatePresence>
    </div>
  )
}

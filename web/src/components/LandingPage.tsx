import { useEffect, useState } from 'react'
import { BrandMark } from './BrandMark'
import { GatewayCanvas } from './GatewayCanvas'
import { ThemeToggle } from './ThemeToggle'
import type { Theme } from '../hooks/useTheme'
import { usePrefersReducedMotion } from '../hooks/usePrefersReducedMotion'

interface Props {
  theme: Theme
  onToggleTheme: () => void
  onEnter: () => void
}

// LandingPage is the public front door. It sits before the existing role
// gateway and never touches how a role is chosen.
export function LandingPage({ theme, onToggleTheme, onEnter }: Props) {
  const [leaving, setLeaving] = useState(false)
  const reduced = usePrefersReducedMotion()

  useEffect(() => {
    if (!leaving) return
    if (reduced) {
      onEnter()
      return
    }
    const timer = window.setTimeout(onEnter, 640)
    return () => window.clearTimeout(timer)
  }, [leaving, reduced, onEnter])

  const enter = () => {
    if (leaving) return
    if (reduced) {
      onEnter()
      return
    }
    setLeaving(true)
  }

  return (
    <div className={`gateway${leaving ? ' is-leaving' : ''}`}>
      <div className="gateway-canvas-wrap">
        <GatewayCanvas theme={theme} rushing={leaving} />
      </div>

      <header className="gateway-nav">
        <div className="brand brand-quiet">
          <BrandMark size={20} />
          <span>Meridian</span>
        </div>
        <ThemeToggle theme={theme} onToggle={onToggleTheme} />
      </header>

      <main className="gateway-hero">
        <span className="gateway-mark">
          <BrandMark size={44} />
        </span>
        <p className="eyebrow mono">Distributed inference gateway</p>
        <h1 className="wordmark">Meridian</h1>
        <p className="gateway-tagline">
          Private AI for your office, pooled across every machine on the network.
        </p>
        <div className="gateway-actions">
          <button type="button" className="btn btn-primary btn-lg" onClick={enter}>
            Enter Meridian
          </button>
          <a className="btn btn-quiet btn-lg" href="#how-it-works">
            How it works
          </a>
        </div>
        <p className="gateway-caption mono" aria-hidden="true">
          requests <span>→</span> meridian <span>→</span> workers
        </p>
      </main>

      <section className="gateway-how" id="how-it-works">
        <div className="gateway-how-head">
          <p className="eyebrow mono">The path of a request</p>
          <h2>Many clients. One gateway. Every worker.</h2>
        </div>
        <ol className="how-grid">
          <li className="how-step">
            <span className="how-num mono">01</span>
            <h3>Connect machines</h3>
            <p>Each machine runs Meridian and keeps its worker listeners on loopback.</p>
          </li>
          <li className="how-step">
            <span className="how-num mono">02</span>
            <h3>Pool models</h3>
            <p>Models are registered with their live instances, health and capacity.</p>
          </li>
          <li className="how-step">
            <span className="how-num mono">03</span>
            <h3>Route intelligently</h3>
            <p>Requests follow the system policy; a failed instance moves to the next.</p>
          </li>
        </ol>
      </section>

      <footer className="gateway-foot mono">
        <span>meridian</span>
        <span className="gateway-foot-rule" aria-hidden="true" />
        <span>private infrastructure</span>
        <span className="gateway-foot-rule" aria-hidden="true" />
        <span>failover-aware routing</span>
      </footer>
    </div>
  )
}

import { useState } from 'react'
import { checkAdmin, errorMessage } from '../api'
import { BrandMark } from './BrandMark'
import { GatewayCanvas } from './GatewayCanvas'
import { ThemeToggle } from './ThemeToggle'
import type { Theme } from '../hooks/useTheme'

export type Role = 'user' | 'admin'

interface Props {
  theme: Theme
  onToggleTheme: () => void
  onPick: (role: Role, token?: string) => void
}

// Landing is the role gateway. Two choices: use the models, or open the admin
// console with the token this leader was started with.
export function Landing({ theme, onToggleTheme, onPick }: Props) {
  const [askToken, setAskToken] = useState(false)
  const [token, setToken] = useState('')
  const [checking, setChecking] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const submitToken = async () => {
    const value = token.trim()
    if (!value || checking) return
    setChecking(true)
    setError(null)
    try {
      if (await checkAdmin(value)) {
        onPick('admin', value)
      } else {
        setError('That token was not accepted.')
      }
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setChecking(false)
    }
  }

  return (
    <div className="landing">
      <div className="landing-canvas">
        <GatewayCanvas theme={theme} dim />
      </div>

      <header className="landing-nav">
        <div className="brand brand-quiet">
          <BrandMark size={20} />
          <span>Meridian</span>
        </div>
        <ThemeToggle theme={theme} onToggle={onToggleTheme} />
      </header>

      <div className="landing-body">
        <div className="landing-hero">
          <span className="landing-mark">
            <BrandMark size={38} />
          </span>
          <h1 className="wordmark">Meridian</h1>
          <p className="landing-sub">
            Private AI for your office, pooled across every machine on the network.
          </p>
        </div>

        {!askToken ? (
          <div className="role-cards">
            <button type="button" className="role-card role-user" onClick={() => onPick('user')}>
              <span className="role-icon" aria-hidden="true">
                <svg viewBox="0 0 24 24" width="30" height="30" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinejoin="round">
                  <path d="M4.5 5.5h15v9h-9l-4.5 4v-4H4.5z" />
                  <path className="role-icon-dots" d="M8.5 10h.01M12 10h.01M15.5 10h.01" strokeWidth="2.4" strokeLinecap="round" />
                </svg>
              </span>
              <span className="role-title">I'm a user</span>
              <span className="role-desc">Chat with the models. Pick a model and start talking.</span>
              <span className="role-enter mono" aria-hidden="true">open chat <span>→</span></span>
            </button>

            <button type="button" className="role-card role-admin" onClick={() => setAskToken(true)}>
              <span className="role-icon" aria-hidden="true">
                <svg viewBox="0 0 24 24" width="30" height="30" fill="none" stroke="currentColor" strokeWidth="1.5">
                  <rect x="5" y="10.5" width="14" height="9.5" rx="2" />
                  <path className="role-icon-shackle" d="M8.5 10.5V8a3.5 3.5 0 0 1 7 0v2.5" />
                  <path d="M12 14.5v2" strokeLinecap="round" />
                </svg>
              </span>
              <span className="role-title">I'm an admin</span>
              <span className="role-desc">Control panel, live routing graph, and the system routing policy.</span>
              <span className="role-enter mono" aria-hidden="true">sign in <span>→</span></span>
            </button>
          </div>
        ) : (
          <form
            className="token-card"
            onSubmit={(e) => {
              e.preventDefault()
              void submitToken()
            }}
          >
            <p className="eyebrow mono">Admin access</p>
            <h2>Admin sign-in</h2>
            <p className="muted small">Enter the admin token this leader was started with.</p>
            <input
              type="password"
              autoFocus
              value={token}
              onChange={(e) => setToken(e.target.value)}
              placeholder="Admin token"
              autoComplete="off"
            />
            {error && <p className="error-line">{error}</p>}
            <div className="token-actions">
              <button type="button" className="btn btn-quiet" onClick={() => setAskToken(false)}>
                Back
              </button>
              <button type="submit" className="btn btn-primary" disabled={checking || !token.trim()}>
                {checking ? 'Checking…' : 'Open console'}
              </button>
            </div>
          </form>
        )}
      </div>

      <footer className="landing-foot mono">
        <span>role gateway</span>
        <span className="gateway-foot-rule" aria-hidden="true" />
        <span>admin access is token-gated</span>
      </footer>
    </div>
  )
}

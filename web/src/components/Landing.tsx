import { useState } from 'react'
import { checkAdmin, errorMessage } from '../api'

export type Role = 'user' | 'admin'

interface Props {
  onPick: (role: Role, token?: string) => void
}

// Landing is the first screen on the leader. Two large choices: use the
// models, or open the admin console. Admin needs the token, which the server
// checks before the screen changes.
export function Landing({ onPick }: Props) {
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
      <div className="landing-hero">
        <div className="landing-mark">
          <svg viewBox="0 0 24 24" width="40" height="40" aria-hidden="true">
            <circle cx="12" cy="12" r="10.5" fill="none" stroke="currentColor" strokeWidth="1.5" />
            <path d="M12 3 L13.6 12 L12 21 L10.4 12 Z" fill="currentColor" />
            <path d="M3 12 L12 10.4 L21 12 L12 13.6 Z" fill="currentColor" opacity="0.45" />
          </svg>
        </div>
        <h1>Meridian</h1>
        <p className="landing-sub">Private AI for your office, pooled across every machine on the network.</p>
      </div>

      {!askToken ? (
        <div className="role-cards">
          <button className="role-card role-user" onClick={() => onPick('user')}>
            <span className="role-icon" aria-hidden="true">
              <svg viewBox="0 0 24 24" width="44" height="44">
                <path
                  d="M4 5h16v10H9l-5 4z"
                  fill="none"
                  stroke="currentColor"
                  strokeWidth="1.8"
                  strokeLinejoin="round"
                />
              </svg>
            </span>
            <span className="role-title">I'm a user</span>
            <span className="role-desc">Chat with the models. Pick a model and start talking.</span>
          </button>

          <button className="role-card role-admin" onClick={() => setAskToken(true)}>
            <span className="role-icon" aria-hidden="true">
              <svg viewBox="0 0 24 24" width="44" height="44">
                <rect x="5" y="11" width="14" height="10" rx="2" fill="none" stroke="currentColor" strokeWidth="1.8" />
                <path d="M8 11V8a4 4 0 0 1 8 0v3" fill="none" stroke="currentColor" strokeWidth="1.8" />
              </svg>
            </span>
            <span className="role-title">I'm an admin</span>
            <span className="role-desc">Control panel, live routing graph, and the system routing policy.</span>
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
            <button type="button" className="btn" onClick={() => setAskToken(false)}>
              Back
            </button>
            <button type="submit" className="btn btn-primary" disabled={checking || !token.trim()}>
              {checking ? 'Checking…' : 'Open console'}
            </button>
          </div>
        </form>
      )}
    </div>
  )
}

import { useCallback, useEffect, useRef, useState } from 'react'
import { errorMessage, fetchCatalog, sendChat } from './api'
import { AdminPanel } from './components/AdminPanel'
import { ChatPanel } from './components/ChatPanel'
import { Landing, type Role } from './components/Landing'
import { ModelsPanel } from './components/ModelsPanel'
import { uid, useConversations } from './hooks/useConversations'
import { usePoll } from './hooks/usePoll'
import type { ChatMessage, ChatTurn, Conversation } from './types'

type AdminTab = 'control' | 'chat'

interface Prefs {
  model: string
  stream: boolean
}

const PREFS_KEY = 'meridian.ui.prefs.v1'
const ROLE_KEY = 'meridian.role'
const TOKEN_KEY = 'meridian.adminToken'

function loadPrefs(): Prefs {
  try {
    const raw = localStorage.getItem(PREFS_KEY)
    if (raw) return { model: '', stream: true, ...(JSON.parse(raw) as Partial<Prefs>) }
  } catch {
    // Corrupt preferences fall back to defaults.
  }
  return { model: '', stream: true }
}

function titleFrom(text: string): string {
  const clean = text.replace(/\s+/g, ' ').trim()
  return clean.length <= 46 ? clean : `${clean.slice(0, 46).trimEnd()}…`
}

interface Session {
  role: Role | null
  token: string
}

function loadSession(): Session {
  const role = sessionStorage.getItem(ROLE_KEY)
  const token = sessionStorage.getItem(TOKEN_KEY) ?? ''
  if (role === 'user') return { role: 'user', token: '' }
  if (role === 'admin' && token) return { role: 'admin', token }
  return { role: null, token: '' }
}

export default function App() {
  const [session, setSession] = useState<Session>(loadSession)
  const [adminTab, setAdminTab] = useState<AdminTab>('control')

  const signIn = (role: Role, token?: string) => {
    sessionStorage.setItem(ROLE_KEY, role)
    if (role === 'admin' && token) sessionStorage.setItem(TOKEN_KEY, token)
    else sessionStorage.removeItem(TOKEN_KEY)
    setSession({ role, token: token ?? '' })
    setAdminTab('control')
  }

  const signOut = useCallback(() => {
    sessionStorage.removeItem(ROLE_KEY)
    sessionStorage.removeItem(TOKEN_KEY)
    setSession({ role: null, token: '' })
  }, [])

  if (session.role === null) {
    return <Landing onPick={signIn} />
  }
  return (
    <Console
      role={session.role}
      token={session.token}
      adminTab={adminTab}
      onAdminTab={setAdminTab}
      onSignOut={signOut}
    />
  )
}

interface ConsoleProps {
  role: Role
  token: string
  adminTab: AdminTab
  onAdminTab: (t: AdminTab) => void
  onSignOut: () => void
}

function Console({ role, token, adminTab, onAdminTab, onSignOut }: ConsoleProps) {
  const catalog = usePoll(fetchCatalog, 5000)
  const convs = useConversations()
  const [prefs, setPrefs] = useState<Prefs>(loadPrefs)
  const [busy, setBusy] = useState(false)
  const abortRef = useRef<AbortController | null>(null)

  useEffect(() => {
    try {
      localStorage.setItem(PREFS_KEY, JSON.stringify(prefs))
    } catch {
      // Preferences are a convenience; ignore storage failures.
    }
  }, [prefs])

  const models = catalog.data?.llm ?? []
  // Prefer the saved model; if it is gone, pick a healthy one.
  const model =
    models.find((m) => m.id === prefs.model)?.id ??
    models.find((m) => m.healthy)?.id ??
    models[0]?.id ??
    ''
  const systemPolicy = catalog.data?.default_routing_policy ?? ''
  const active: Conversation | null = convs.list.find((c) => c.id === convs.activeId) ?? null

  const send = async (text: string) => {
    if (busy || !model) return
    const convId = convs.activeId ?? convs.create()
    const prior = active?.id === convId ? active.messages : []
    const userMsg: ChatMessage = { id: uid(), role: 'user', content: text }
    const replyId = uid()

    convs.update(convId, (c) => ({
      ...c,
      title: c.messages.length === 0 ? titleFrom(text) : c.title,
      messages: [...c.messages, userMsg, { id: replyId, role: 'assistant', content: '', pending: true }],
    }))

    // Send the whole visible history each turn. Failed or pending turns are
    // left out so the model never sees an error message as conversation.
    const history: ChatTurn[] = [...prior, userMsg]
      .filter((m) => !m.pending && !m.error && m.content)
      .map((m) => ({ role: m.role, content: m.content }))

    const patchReply = (fn: (m: ChatMessage) => ChatMessage) =>
      convs.update(convId, (c) => ({
        ...c,
        messages: c.messages.map((m) => (m.id === replyId ? fn(m) : m)),
      }))

    const controller = new AbortController()
    abortRef.current = controller
    setBusy(true)
    try {
      const res = await sendChat({
        model,
        messages: history,
        stream: prefs.stream,
        signal: controller.signal,
        onDelta: (delta) => patchReply((m) => ({ ...m, content: m.content + delta })),
      })
      patchReply((m) => ({ ...m, pending: false, worker: res.worker, failover: res.failover }))
    } catch (err) {
      if (controller.signal.aborted) patchReply((m) => ({ ...m, pending: false, stopped: true }))
      else patchReply((m) => ({ ...m, pending: false, error: errorMessage(err) }))
    } finally {
      abortRef.current = null
      setBusy(false)
    }
  }

  const isAdmin = role === 'admin'
  const showChat = !isAdmin || adminTab === 'chat'

  return (
    <div className="app">
      <header className="topbar">
        <div className="brand">
          <BrandMark />
          <span>Meridian</span>
        </div>
        {isAdmin && (
          <nav className="tabs">
            <button className={adminTab === 'control' ? 'tab active' : 'tab'} onClick={() => onAdminTab('control')}>
              Control panel
            </button>
            <button className={adminTab === 'chat' ? 'tab active' : 'tab'} onClick={() => onAdminTab('chat')}>
              Chat
            </button>
          </nav>
        )}
        <div className="topbar-right">
          <span className={isAdmin ? 'badge badge-admin' : 'badge'}>{isAdmin ? 'Admin' : 'User'}</span>
          {catalog.error ? (
            <span className="badge badge-warn" title={catalog.error}>leader unreachable</span>
          ) : (
            <span className="badge badge-healthy">connected</span>
          )}
          <button className="btn btn-ghost" onClick={onSignOut}>Switch role</button>
        </div>
      </header>

      {isAdmin && adminTab === 'control' ? (
        <div className="page">
          <AdminPanel token={token} onExpired={onSignOut} />
        </div>
      ) : (
        <div className="layout">
          <aside className="sidebar">
            <button className="btn btn-primary full" onClick={() => convs.create()}>
              New chat
            </button>
            <ul className="conv-list">
              {convs.list.map((c) => (
                <li key={c.id} className={c.id === convs.activeId ? 'conv active' : 'conv'}>
                  <button className="conv-open" onClick={() => convs.setActiveId(c.id)}>
                    <span className="conv-title">{c.title}</span>
                    <span className="muted small">{new Date(c.updatedAt).toLocaleString()}</span>
                  </button>
                  <button className="conv-delete" title="Delete chat" onClick={() => convs.remove(c.id)}>
                    ×
                  </button>
                </li>
              ))}
              {convs.list.length === 0 && <li className="muted small pad">No chats yet.</li>}
            </ul>
          </aside>

          <main className="main">
            {showChat && (
              <ChatPanel
                conversation={active}
                models={models}
                model={model}
                stream={prefs.stream}
                busy={busy}
                onModel={(id) => setPrefs((p) => ({ ...p, model: id }))}
                onStream={(on) => setPrefs((p) => ({ ...p, stream: on }))}
                onSend={(text) => void send(text)}
                onStop={() => abortRef.current?.abort()}
              />
            )}
          </main>

          <ModelsPanel models={models} policy={systemPolicy} />
        </div>
      )}
    </div>
  )
}

function BrandMark() {
  return (
    <svg viewBox="0 0 24 24" width="22" height="22" aria-hidden="true">
      <circle cx="12" cy="12" r="10.5" fill="none" stroke="currentColor" strokeWidth="1.5" />
      <path d="M12 3 L13.6 12 L12 21 L10.4 12 Z" fill="currentColor" />
      <path d="M3 12 L12 10.4 L21 12 L12 13.6 Z" fill="currentColor" opacity="0.45" />
    </svg>
  )
}

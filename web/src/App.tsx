import { useCallback, useEffect, useRef, useState } from 'react'
import type { CSSProperties, PointerEvent as ReactPointerEvent } from 'react'
import { errorMessage, fetchCatalog, sendChat } from './api'
import { AdminPanel } from './components/AdminPanel'
import { BrandMark } from './components/BrandMark'
import { ChatPanel } from './components/ChatPanel'
import { Landing, type Role } from './components/Landing'
import { LandingPage } from './components/LandingPage'
import { ModelsPanel } from './components/ModelsPanel'
import { ThemeToggle } from './components/ThemeToggle'
import { uid, useConversations } from './hooks/useConversations'
import { usePoll } from './hooks/usePoll'
import { useTheme } from './hooks/useTheme'
import type { ChatMessage, ChatTurn, Conversation } from './types'

type AdminTab = 'control' | 'chat'
type Rail = 'none' | 'chats' | 'models'

interface Prefs {
  model: string
  stream: boolean
}

const PREFS_KEY = 'meridian.ui.prefs.v1'
const ROLE_KEY = 'meridian.role'
const TOKEN_KEY = 'meridian.adminToken'
const SPLIT_KEY = 'meridian-split'

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

function splitPreference(): boolean {
  try {
    return localStorage.getItem(SPLIT_KEY) === 'on'
  } catch {
    return false
  }
}

export default function App() {
  const { theme, toggle } = useTheme()
  // The role gateway is skipped when a session is restored from this tab.
  const [entered, setEntered] = useState(() => loadSession().role !== null)
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
    if (!entered) {
      return <LandingPage theme={theme} onToggleTheme={toggle} onEnter={() => setEntered(true)} />
    }
    return <Landing theme={theme} onToggleTheme={toggle} onPick={signIn} />
  }
  return (
    <Console
      role={session.role}
      token={session.token}
      adminTab={adminTab}
      onAdminTab={setAdminTab}
      onSignOut={signOut}
      theme={theme}
      onToggleTheme={toggle}
    />
  )
}

interface ConsoleProps {
  role: Role
  token: string
  adminTab: AdminTab
  onAdminTab: (t: AdminTab) => void
  onSignOut: () => void
  theme: 'dark' | 'light'
  onToggleTheme: () => void
}

function Console({ role, token, adminTab, onAdminTab, onSignOut, theme, onToggleTheme }: ConsoleProps) {
  const catalog = usePoll(fetchCatalog, 5000)
  const convs = useConversations()
  const [prefs, setPrefs] = useState<Prefs>(loadPrefs)
  const [busy, setBusy] = useState(false)
  const abortRef = useRef<AbortController | null>(null)

  // Split view is UI state only. It never changes sending, streaming or routing.
  const [split, setSplit] = useState(splitPreference)
  const [splitRatio, setSplitRatio] = useState(50)
  const [dragging, setDragging] = useState(false)
  const [rail, setRail] = useState<Rail>('none')
  const layoutRef = useRef<HTMLDivElement>(null)

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
  const isAdmin = role === 'admin'
  const showChat = !isAdmin || adminTab === 'chat'
  const messageCount = active?.messages.length ?? 0

  const applySplit = useCallback((on: boolean) => {
    setSplit(on)
    try {
      localStorage.setItem(SPLIT_KEY, on ? 'on' : 'off')
    } catch {
      // The split still works for this session.
    }
  }, [])

  // Opening split when an admin sends the first message uses only existing
  // state (the message list). The send handler is untouched.
  const prevCount = useRef(messageCount)
  const prevConv = useRef(convs.activeId)
  useEffect(() => {
    const sameConversation = prevConv.current === convs.activeId
    prevConv.current = convs.activeId
    const grew = messageCount > prevCount.current
    const fresh = prevCount.current === 0
    prevCount.current = messageCount
    if (!isAdmin || adminTab !== 'chat' || !grew || (!sameConversation && !fresh) || messageCount === 0) return
    let pref: string | null = null
    try {
      pref = localStorage.getItem(SPLIT_KEY)
    } catch {
      // No stored preference: auto-open.
    }
    if (pref !== 'off') setSplit(true)
  }, [messageCount, convs.activeId, isAdmin, adminTab])

  // A brand-new empty conversation returns to full-width chat.
  useEffect(() => {
    if (split && isAdmin && adminTab === 'chat' && messageCount === 0) applySplit(false)
  }, [split, isAdmin, adminTab, messageCount, applySplit])

  useEffect(() => {
    if (!split) setRail('none')
  }, [split])

  const startDrag = useCallback((e: ReactPointerEvent<HTMLDivElement>) => {
    e.preventDefault()
    const el = layoutRef.current
    if (!el) return
    setDragging(true)
    const move = (ev: PointerEvent) => {
      const rect = el.getBoundingClientRect()
      const pct = ((ev.clientX - rect.left) / rect.width) * 100
      setSplitRatio(Math.min(70, Math.max(30, pct)))
    }
    const up = () => {
      setDragging(false)
      window.removeEventListener('pointermove', move)
    }
    window.addEventListener('pointermove', move)
    window.addEventListener('pointerup', up, { once: true })
  }, [])

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

  const changeTab = (tab: AdminTab) => {
    setSplit(false)
    onAdminTab(tab)
  }

  return (
    <div className="app">
      <header className="topbar">
        <div className="brand">
          <BrandMark />
          <span className="brand-name">Meridian</span>
          <span className="brand-sub mono" aria-hidden="true">gateway</span>
        </div>
        {isAdmin && (
          <nav className="tabs" data-active={adminTab} aria-label="Console views">
            <span className="tab-pill" aria-hidden="true" />
            <button className={adminTab === 'control' ? 'tab active' : 'tab'} onClick={() => changeTab('control')}>
              Control panel
            </button>
            <button className={adminTab === 'chat' ? 'tab active' : 'tab'} onClick={() => changeTab('chat')}>
              Chat
            </button>
          </nav>
        )}
        <div className="topbar-right">
          {isAdmin && adminTab === 'chat' && (
            <button className="btn btn-quiet btn-split" onClick={() => applySplit(!split)}>
              {split ? 'Close split' : 'Split view'}
            </button>
          )}
          <span className={isAdmin ? 'badge badge-admin' : 'badge badge-role'}>
            {isAdmin ? 'Admin' : 'User'}
          </span>
          {catalog.error ? (
            <span className="badge badge-warn" title={catalog.error}>
              <span className="status-led status-led-warn" aria-hidden="true" />
              leader unreachable
            </span>
          ) : (
            <span className="badge badge-healthy">
              <span className="status-led" aria-hidden="true" />
              connected
            </span>
          )}
          <ThemeToggle theme={theme} onToggle={onToggleTheme} />
          <button className="btn btn-ghost" onClick={onSignOut}>Switch role</button>
        </div>
      </header>

      {isAdmin && adminTab === 'control' ? (
        <div className="page">
          <AdminPanel token={token} onExpired={onSignOut} />
        </div>
      ) : (
        <div
          ref={layoutRef}
          className={`layout${split ? ' layout-split' : ''}${dragging ? ' is-dragging' : ''}`}
          style={{ '--split-left': `${splitRatio}%` } as CSSProperties}
        >
          <aside className="sidebar">
            <button className="btn btn-primary full new-chat" onClick={() => convs.create()}>
              <span className="new-chat-icon" aria-hidden="true">+</span>
              <span className="new-chat-label">New chat</span>
            </button>
            <ConversationList convs={convs} />
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

          {split && isAdmin && adminTab === 'chat' && (
            <>
              <div className="split-control">
                <AdminPanel token={token} onExpired={onSignOut} />
              </div>
              <div
                className="split-divider"
                role="separator"
                aria-orientation="vertical"
                aria-label="Resize panes"
                tabIndex={0}
                onPointerDown={startDrag}
                onDoubleClick={() => setSplitRatio(50)}
                onKeyDown={(e) => {
                  if (e.key === 'ArrowLeft') setSplitRatio((r) => Math.max(30, r - 2))
                  if (e.key === 'ArrowRight') setSplitRatio((r) => Math.min(70, r + 2))
                }}
              >
                <span aria-hidden="true" />
              </div>
              <div className="split-rail">
                <button
                  type="button"
                  className="rail-btn"
                  title="New chat"
                  aria-label="New chat"
                  onClick={() => convs.create()}
                >
                  <svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" strokeWidth="1.7"><path d="M12 5v14M5 12h14" /></svg>
                </button>
                <button
                  type="button"
                  className={rail === 'chats' ? 'rail-btn active' : 'rail-btn'}
                  title="Conversations"
                  aria-label="Conversations"
                  onClick={() => setRail((r) => (r === 'chats' ? 'none' : 'chats'))}
                >
                  <svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" strokeWidth="1.7"><path d="M4 6h16M4 12h16M4 18h10" /></svg>
                </button>
                <button
                  type="button"
                  className={rail === 'models' ? 'rail-btn active' : 'rail-btn'}
                  title="Models and routing"
                  aria-label="Models and routing"
                  onClick={() => setRail((r) => (r === 'models' ? 'none' : 'models'))}
                >
                  <svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" strokeWidth="1.7"><path d="M12 3 3.5 8 12 13l8.5-5Z" /><path d="M3.5 13 12 18l8.5-5" /></svg>
                </button>
              </div>
              {rail !== 'none' && (
                <div className="split-drawer">
                  <div className="split-drawer-head">
                    <span className="mono">{rail === 'chats' ? 'Conversations' : 'Models & routing'}</span>
                    <button type="button" className="icon-btn" onClick={() => setRail('none')} aria-label="Close drawer">×</button>
                  </div>
                  {rail === 'chats' ? (
                    <ConversationList convs={convs} onPick={() => setRail('none')} />
                  ) : (
                    <ModelsPanel models={models} policy={systemPolicy} />
                  )}
                </div>
              )}
            </>
          )}
        </div>
      )}
    </div>
  )
}

function ConversationList({
  convs,
  onPick,
}: {
  convs: ReturnType<typeof useConversations>
  onPick?: () => void
}) {
  return (
    <ul className="conv-list">
      {convs.list.map((c) => (
        <li key={c.id} className={c.id === convs.activeId ? 'conv active' : 'conv'}>
          <button
            className="conv-open"
            onClick={() => {
              convs.setActiveId(c.id)
              onPick?.()
            }}
          >
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
  )
}

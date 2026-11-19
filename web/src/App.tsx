import { useEffect, useRef, useState } from 'react'
import { errorMessage, fetchCatalog, sendChat } from './api'
import { ChatPanel } from './components/ChatPanel'
import { ClusterPanel } from './components/ClusterPanel'
import { OCRPanel } from './components/OCRPanel'
import { uid, useConversations } from './hooks/useConversations'
import { usePoll } from './hooks/usePoll'
import type { ChatMessage, ChatTurn, Conversation } from './types'

type Tab = 'chat' | 'ocr'

interface Prefs {
  model: string
  policy: string
  stream: boolean
}

const PREFS_KEY = 'meridian.ui.prefs.v1'

function loadPrefs(): Prefs {
  try {
    const raw = localStorage.getItem(PREFS_KEY)
    if (raw) return { model: '', policy: '', stream: true, ...(JSON.parse(raw) as Partial<Prefs>) }
  } catch {
    // Corrupt preferences fall back to defaults.
  }
  return { model: '', policy: '', stream: true }
}

function titleFrom(text: string): string {
  const clean = text.replace(/\s+/g, ' ').trim()
  return clean.length <= 46 ? clean : `${clean.slice(0, 46).trimEnd()}…`
}

export default function App() {
  const catalog = usePoll(fetchCatalog, 5000)
  const convs = useConversations()
  const [tab, setTab] = useState<Tab>('chat')
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
        policy: prefs.policy || undefined,
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

  return (
    <div className="app">
      <header className="topbar">
        <div className="brand">
          <BrandMark />
          <span>Meridian</span>
        </div>
        <nav className="tabs">
          <button className={tab === 'chat' ? 'tab active' : 'tab'} onClick={() => setTab('chat')}>
            Chat
          </button>
          <button className={tab === 'ocr' ? 'tab active' : 'tab'} onClick={() => setTab('ocr')}>
            Document OCR
          </button>
        </nav>
        <div className="topbar-right">
          <span className="badge badge-policy" title="Default routing policy">
            routing: {catalog.data?.default_routing_policy || '—'}
          </span>
          {catalog.error ? (
            <span className="badge badge-warn" title={catalog.error}>gateway unreachable</span>
          ) : (
            <span className="badge badge-healthy">connected</span>
          )}
        </div>
      </header>

      <div className={tab === 'chat' ? 'layout' : 'layout layout-no-sidebar'}>
        {tab === 'chat' && (
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
        )}

        <main className="main">
          {tab === 'chat' ? (
            <ChatPanel
              conversation={active}
              models={models}
              policies={catalog.data?.routing_policies ?? []}
              defaultPolicy={catalog.data?.default_routing_policy ?? ''}
              model={model}
              policy={prefs.policy}
              stream={prefs.stream}
              busy={busy}
              onModel={(id) => setPrefs((p) => ({ ...p, model: id }))}
              onPolicy={(name) => setPrefs((p) => ({ ...p, policy: name }))}
              onStream={(on) => setPrefs((p) => ({ ...p, stream: on }))}
              onSend={(text) => void send(text)}
              onStop={() => abortRef.current?.abort()}
            />
          ) : (
            <OCRPanel />
          )}
        </main>

        <ClusterPanel models={models} policy={catalog.data?.default_routing_policy ?? ''} />
      </div>
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

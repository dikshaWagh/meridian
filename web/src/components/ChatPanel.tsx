import { useEffect, useRef, useState } from 'react'
import type { CatalogModel, ChatMessage, Conversation, RoutingPolicy } from '../types'

interface Props {
  conversation: Conversation | null
  models: CatalogModel[]
  policies: RoutingPolicy[]
  defaultPolicy: string
  model: string
  policy: string
  stream: boolean
  busy: boolean
  onModel: (id: string) => void
  onPolicy: (name: string) => void
  onStream: (on: boolean) => void
  onSend: (text: string) => void
  onStop: () => void
}

export function ChatPanel(props: Props) {
  const { conversation, models, policies, defaultPolicy, model, policy, stream, busy } = props
  const [draft, setDraft] = useState('')
  const endRef = useRef<HTMLDivElement>(null)
  const messages = conversation?.messages ?? []

  useEffect(() => {
    endRef.current?.scrollIntoView({ block: 'end' })
  }, [messages.length, messages[messages.length - 1]?.content])

  const submit = () => {
    const text = draft.trim()
    if (!text || busy || !model) return
    setDraft('')
    props.onSend(text)
  }

  return (
    <section className="panel chat">
      <div className="chat-scroll">
        {messages.length === 0 && (
          <div className="empty">
            <h2>Start a conversation</h2>
            <p className="muted">
              Each turn sends the full history, so the model remembers earlier messages even if a
              worker is replaced mid-chat.
            </p>
          </div>
        )}
        {messages.map((m) => (
          <MessageBubble key={m.id} message={m} />
        ))}
        <div ref={endRef} />
      </div>

      <div className="composer">
        <div className="controls">
          <label className="control">
            <span>Model</span>
            <select value={model} onChange={(e) => props.onModel(e.target.value)} disabled={busy || models.length === 0}>
              {models.length === 0 && <option value="">No LLM models</option>}
              {models.map((m) => (
                <option key={m.id} value={m.id}>
                  {m.id} · {m.healthy_workers ?? 0}/{m.workers ?? 0} up
                </option>
              ))}
            </select>
          </label>
          <label className="control">
            <span>Routing</span>
            <select value={policy} onChange={(e) => props.onPolicy(e.target.value)} disabled={busy}>
              <option value="">Server default ({defaultPolicy || '—'})</option>
              {policies.map((p) => (
                <option key={p.name} value={p.name} title={p.description}>
                  {p.name}
                </option>
              ))}
            </select>
          </label>
          <label className="check">
            <input type="checkbox" checked={stream} onChange={(e) => props.onStream(e.target.checked)} disabled={busy} />
            Stream replies
          </label>
        </div>

        <div className="input-row">
          <textarea
            value={draft}
            placeholder={model ? 'Message Meridian…  (Enter to send, Shift+Enter for a new line)' : 'No model is available yet'}
            onChange={(e) => setDraft(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' && !e.shiftKey) {
                e.preventDefault()
                submit()
              }
            }}
            rows={2}
            disabled={!model}
          />
          {busy ? (
            <button className="btn btn-stop" onClick={props.onStop}>Stop</button>
          ) : (
            <button className="btn btn-primary" onClick={submit} disabled={!draft.trim() || !model}>
              Send
            </button>
          )}
        </div>
      </div>
    </section>
  )
}

function MessageBubble({ message: m }: { message: ChatMessage }) {
  return (
    <div className={`msg msg-${m.role}`}>
      <div className="msg-role">{m.role === 'user' ? 'You' : 'Meridian'}</div>
      <div className="msg-body">
        {m.content || (m.pending ? <span className="typing">● ● ●</span> : null)}
        {m.error && <div className="error-line">{m.error}</div>}
        {m.stopped && <div className="muted small">Stopped.</div>}
      </div>
      {m.role === 'assistant' && (m.worker || m.failover) && (
        <div className="msg-meta">
          {m.worker && <span className="badge">served by {m.worker}</span>}
          {m.failover && <span className="badge badge-warn">failover {m.failover}</span>}
        </div>
      )}
    </div>
  )
}

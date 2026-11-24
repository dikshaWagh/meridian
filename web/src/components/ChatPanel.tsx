import { useEffect, useRef, useState } from 'react'
import type { CatalogModel, ChatMessage, Conversation } from '../types'
import { BrandMark } from './BrandMark'

interface Props {
  conversation: Conversation | null
  models: CatalogModel[]
  model: string
  stream: boolean
  busy: boolean
  onModel: (id: string) => void
  onStream: (on: boolean) => void
  onSend: (text: string) => void
  onStop: () => void
}

export function ChatPanel(props: Props) {
  const { conversation, models, model, stream, busy } = props
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
            <span className="empty-mark" aria-hidden="true">
              <BrandMark size={34} />
            </span>
            <p className="eyebrow mono">No active conversation</p>
            <h2>Start a conversation</h2>
            <p className="muted">
              Each turn sends the full history, so the model remembers earlier messages even if a
              worker is replaced mid-chat.
            </p>
            <div className="empty-hints mono" aria-hidden="true">
              <span><kbd>Enter</kbd> send</span>
              <span><kbd>Shift</kbd> + <kbd>Enter</kbd> newline</span>
            </div>
          </div>
        )}
        {messages.map((m) => (
          <MessageBubble key={m.id} message={m} />
        ))}
        <div ref={endRef} />
      </div>

      <div className="composer">
        <div className="composer-bar">
          <span className="composer-state mono">
            <span className={busy ? 'live-dot live-dot-busy' : 'live-dot'} aria-hidden="true" />
            {busy ? 'receiving' : 'ready'}
          </span>
          <div className="controls">
            <label className="control">
              <span className="control-label mono">Model</span>
              <select value={model} onChange={(e) => props.onModel(e.target.value)} disabled={busy || models.length === 0}>
                {models.length === 0 && <option value="">No LLM models</option>}
                {models.map((m) => (
                  <option key={m.id} value={m.id}>
                    {m.id} · {m.healthy_workers ?? 0}/{m.workers ?? 0} up
                  </option>
                ))}
              </select>
            </label>
            <label className="check">
              <input type="checkbox" checked={stream} onChange={(e) => props.onStream(e.target.checked)} disabled={busy} />
              Stream replies
            </label>
          </div>
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
        <div className="composer-hints mono" aria-hidden="true">
          <span><kbd>Enter</kbd> send</span>
          <span><kbd>Shift</kbd>+<kbd>Enter</kbd> newline</span>
        </div>
      </div>
    </section>
  )
}

function MessageBubble({ message: m }: { message: ChatMessage }) {
  const streaming = Boolean(m.pending && m.content)
  return (
    <div className={`msg msg-${m.role}`}>
      <div className="msg-role mono">{m.role === 'user' ? 'You' : 'Meridian'}</div>
      <div className="msg-body">
        {m.content || (m.pending ? (
          <span className="typing" aria-hidden="true">
            <i /><i /><i />
          </span>
        ) : null)}
        {streaming && <span className="stream-caret" aria-hidden="true" />}
        {m.error && <div className="error-line">{m.error}</div>}
        {m.stopped && <div className="muted small">Stopped.</div>}
      </div>
      {m.role === 'assistant' && (m.worker || m.failover) && (
        <div className="msg-meta">
          {m.worker && <span className="badge mono">served by {m.worker}</span>}
          {m.failover && <span className="badge badge-warn mono">failover {m.failover}</span>}
        </div>
      )}
    </div>
  )
}

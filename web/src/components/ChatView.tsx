import { useEffect, useRef, useState } from 'react'
import type { CatalogModel, Conversation, RoutingPolicy } from '../types'
import { Markdown } from './Markdown'
import { Bolt, Send, Sparkle, Stop } from './Icons'
import { ModelPicker } from './ModelPicker'
import { PolicyPicker } from './PolicyPicker'

interface ChatViewProps {
  conversation: Conversation | undefined
  models: CatalogModel[]
  policies: RoutingPolicy[]
  policy: string
  streaming: boolean
  active: boolean
  onSend: (text: string) => void
  onStop: () => void
  onSelectModel: (id: string) => void
  onSelectPolicy: (name: string) => void
}

const suggestions = [
  {
    label: 'Explain a trade-off',
    prompt: 'Explain the trade-off between running one large model and several smaller models on the same GPU.',
  },
  {
    label: 'Summarize my notes',
    prompt: 'Summarize the key points of a design review, keeping it under five bullets.',
  },
  {
    label: 'Write a Go handler',
    prompt: 'Write a small Go HTTP handler with context cancellation and a JSON error response.',
  },
]

function formatTime(timestamp: number): string {
  return new Date(timestamp).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
}

export function ChatView({
  conversation,
  models,
  policies,
  policy,
  streaming,
  active,
  onSend,
  onStop,
  onSelectModel,
  onSelectPolicy,
}: ChatViewProps) {
  const [value, setValue] = useState('')
  const scrollRef = useRef<HTMLDivElement>(null)
  const textareaRef = useRef<HTMLTextAreaElement>(null)
  const stickToBottom = useRef(true)

  const messages = conversation?.messages ?? []
  const contentLength = messages.reduce((total, message) => total + message.content.length, 0)

  useEffect(() => {
    if (stickToBottom.current && scrollRef.current) {
      scrollRef.current.scrollTop = scrollRef.current.scrollHeight
    }
  }, [messages.length, contentLength])

  useEffect(() => {
    const element = textareaRef.current
    if (!element) return
    element.style.height = '0px'
    element.style.height = `${Math.min(element.scrollHeight, 200)}px`
  }, [value])

  const submit = () => {
    const text = value.trim()
    if (!text || !active || streaming) return
    setValue('')
    stickToBottom.current = true
    onSend(text)
  }

  return (
    <section className="chat">
      <div className="chat-toolbar">
        <ModelPicker models={models} selected={conversation?.model ?? ''} onSelect={onSelectModel} />
        <PolicyPicker policies={policies} selected={policy} onSelect={onSelectPolicy} />
        <div className="toolbar-spacer" />
        <span className="local-badge">
          <Bolt size={13} />
          Local inference
        </span>
      </div>

      <div
        className="chat-scroll"
        ref={scrollRef}
        onScroll={() => {
          const element = scrollRef.current
          if (!element) return
          stickToBottom.current = element.scrollHeight - element.scrollTop - element.clientHeight < 90
        }}
      >
        {messages.length === 0 ? (
          <div className="empty-state">
            <div className="orb">
              <Sparkle size={30} />
            </div>
            <h1>How can I help?</h1>
            <p>Chat with a local model. When you need vision or image generation, switch to ML Studio — Meridian keeps only one subsystem resident at a time.</p>
            <div className="suggestions">
              {suggestions.map((suggestion) => (
                <button
                  key={suggestion.label}
                  className="chip"
                  disabled={!active}
                  onClick={() => {
                    if (!active) return
                    stickToBottom.current = true
                    onSend(suggestion.prompt)
                  }}
                >
                  {suggestion.label}
                </button>
              ))}
            </div>
          </div>
        ) : (
          <div className="messages">
            {messages.map((message) => {
              const isUser = message.role === 'user'
              return (
                <div key={message.id} className={`msg ${isUser ? 'user' : 'assistant'}`}>
                  {!isUser && (
                    <div className="avatar" aria-hidden="true">
                      <Sparkle size={15} />
                    </div>
                  )}
                  <div className="bubble-wrap">
                    <div className={`bubble ${message.error ? 'has-error' : ''}`}>
                      {isUser ? (
                        <p className="user-text">{message.content}</p>
                      ) : message.content ? (
                        <Markdown text={message.content} />
                      ) : (
                        <div className="typing" aria-label="Assistant is typing">
                          <span />
                          <span />
                          <span />
                        </div>
                      )}
                      {!isUser && message.pending && message.content && <span className="caret" aria-hidden="true" />}
                      {message.error && <div className="msg-error">{message.error}</div>}
                    </div>
                    <div className="msg-meta">
                      {isUser ? 'You' : (message.model ?? 'Assistant')} · {formatTime(message.createdAt)}
                      {!isUser && message.pending ? ' · streaming' : ''}
                    </div>
                  </div>
                </div>
              )
            })}
          </div>
        )}
      </div>

      <div className="composer-wrap">
        <div className={`composer ${!active ? 'is-disabled' : ''}`}>
          <textarea
            ref={textareaRef}
            rows={1}
            value={value}
            placeholder={active ? 'Message Meridian…' : 'Switch to Chat to start a conversation'}
            disabled={!active}
            onChange={(event) => setValue(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === 'Enter' && !event.shiftKey) {
                event.preventDefault()
                submit()
              }
            }}
          />
          {streaming ? (
            <button className="send-button stop" onClick={onStop} aria-label="Stop generating">
              <Stop size={16} />
            </button>
          ) : (
            <button
              className="send-button"
              onClick={submit}
              disabled={!active || !value.trim()}
              aria-label="Send message"
            >
              <Send size={18} />
            </button>
          )}
        </div>
        <div className="composer-hint">
          <span>Enter to send · Shift+Enter for a new line</span>
          <span>{conversation?.model ? `Model · ${conversation.model}` : 'No model selected'}</span>
        </div>
      </div>
    </section>
  )
}

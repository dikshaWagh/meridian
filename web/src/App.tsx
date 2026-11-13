import { useCallback, useEffect, useRef, useState } from 'react'
import { ApiError, errorMessage, setDefaultPolicy, streamChat } from './api'
import type { ChatMessage } from './types'
import { useConversations } from './hooks/useConversations'
import { useSystem } from './hooks/useSystem'
import { useTheme, useToasts } from './hooks/useTheme'
import { Sidebar } from './components/Sidebar'
import { ModeSwitch } from './components/ModeSwitch'
import { ChatView } from './components/ChatView'
import { MLStudio } from './components/MLStudio'
import { TransitionScreen } from './components/TransitionScreen'
import { ToastStack } from './components/ToastStack'
import { Cpu, Menu } from './components/Icons'

function uid(): string {
  if (typeof crypto !== 'undefined' && 'randomUUID' in crypto) return crypto.randomUUID()
  return `m_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 8)}`
}

function titleFrom(text: string): string {
  const clean = text.replace(/\s+/g, ' ').trim()
  if (clean.length <= 46) return clean
  return `${clean.slice(0, 46).trimEnd()}…`
}

const POLICY_KEY = 'meridian.policy.v1'

function storedPolicy(): string {
  try {
    return localStorage.getItem(POLICY_KEY) ?? ''
  } catch {
    return ''
  }
}

export default function App() {
  const { theme, toggle } = useTheme()
  const { toasts, push, dismiss } = useToasts()
  const system = useSystem()
  const conversations = useConversations()
  const {
    active,
    conversations: allConversations,
    activeId,
    createConversation,
    deleteConversation,
    ensureModel,
    patchActive,
    setActiveId,
    updateConversation,
  } = conversations

  const [policy, setPolicy] = useState(storedPolicy)
  const [streaming, setStreaming] = useState(false)
  const [sidebarOpen, setSidebarOpen] = useState(false)
  const abortRef = useRef<AbortController | null>(null)
  const lastErrorRef = useRef('')

  const state = system.state
  const catalog = system.catalog
  const activeModel = active?.model || catalog?.llm[0]?.id || ''
  const transitioning = !state || state.switching || state.phase !== 'active'
  const mode = state?.mode ?? 'idle'

  useEffect(() => {
    if (!policy && catalog?.default_routing_policy) {
      setPolicy(catalog.default_routing_policy)
    }
  }, [policy, catalog?.default_routing_policy])

  useEffect(() => {
    if (!policy) return
    try {
      localStorage.setItem(POLICY_KEY, policy)
    } catch {
      // Ignore unavailable storage.
    }
  }, [policy])

  useEffect(() => {
    if (catalog?.llm?.length) ensureModel(catalog.llm[0].id)
  }, [catalog, ensureModel])

  useEffect(() => {
    if (system.error && system.error !== lastErrorRef.current) {
      lastErrorRef.current = system.error
      push(system.error, 'error')
    }
  }, [system.error, push])

  useEffect(() => {
    return () => abortRef.current?.abort()
  }, [])

  const switchMode = useCallback(
    async (next: 'llm' | 'ml') => {
      if (state?.switching || state?.mode === next) return
      abortRef.current?.abort()
      try {
        await system.switchTo(next)
      } catch (err) {
        push(errorMessage(err), 'error')
      }
    },
    [state?.switching, state?.mode, system, push],
  )

  const handleNewConversation = useCallback(() => {
    createConversation(activeModel)
    setSidebarOpen(false)
    if (state?.mode === 'ml') void switchMode('llm')
  }, [activeModel, createConversation, state?.mode, switchMode])

  const handleSelectConversation = useCallback(
    (id: string) => {
      setActiveId(id)
      setSidebarOpen(false)
      if (state?.mode === 'ml') void switchMode('llm')
    },
    [setActiveId, state?.mode, switchMode],
  )

  const sendMessage = useCallback(
    async (text: string) => {
      const conversation = active
      if (!conversation || streaming) return
      const model = conversation.model || catalog?.llm[0]?.id
      if (!model) {
        push('No LLM model is available.', 'error')
        return
      }
      if (state?.mode !== 'llm' || state.phase !== 'active') {
        push('Switch to Chat before sending a message.', 'info')
        return
      }

      const history = conversation.messages
        .filter((message) => !message.error && message.content.trim())
        .map((message) => ({ role: message.role, content: message.content }))

      const userMessage: ChatMessage = {
        id: uid(),
        role: 'user',
        content: text,
        createdAt: Date.now(),
      }
      const assistantId = uid()
      const assistantMessage: ChatMessage = {
        id: assistantId,
        role: 'assistant',
        content: '',
        createdAt: Date.now(),
        model,
        pending: true,
      }

      updateConversation(conversation.id, (current) => ({
        ...current,
        title: current.messages.length === 0 ? titleFrom(text) : current.title,
        model,
        messages: [...current.messages, userMessage, assistantMessage],
        updatedAt: Date.now(),
      }))

      const controller = new AbortController()
      abortRef.current = controller
      setStreaming(true)
      try {
        await streamChat({
          model,
          messages: [...history, { role: 'user', content: text }],
          policy: policy || undefined,
          signal: controller.signal,
          onDelta: (delta) => {
            updateConversation(conversation.id, (current) => ({
              ...current,
              messages: current.messages.map((message) =>
                message.id === assistantId
                  ? { ...message, content: message.content + delta }
                  : message,
              ),
            }))
          },
        })
        updateConversation(conversation.id, (current) => ({
          ...current,
          messages: current.messages.map((message) =>
            message.id === assistantId ? { ...message, pending: false } : message,
          ),
          updatedAt: Date.now(),
        }))
      } catch (err) {
        if ((err as Error).name === 'AbortError') {
          updateConversation(conversation.id, (current) => ({
            ...current,
            messages: current.messages.map((message) =>
              message.id === assistantId ? { ...message, pending: false } : message,
            ),
          }))
          push('Generation stopped.', 'info')
        } else {
          const message = errorMessage(err)
          updateConversation(conversation.id, (current) => ({
            ...current,
            messages: current.messages.map((item) =>
              item.id === assistantId ? { ...item, pending: false, error: message } : item,
            ),
            updatedAt: Date.now(),
          }))
          if (err instanceof ApiError && (err.type === 'mode_conflict' || err.type === 'switching')) {
            push('Chat is not the active mode. Switch to Chat to continue.', 'info')
          } else {
            push(message, 'error')
          }
        }
      } finally {
        setStreaming(false)
        abortRef.current = null
      }
    },
    [active, streaming, catalog, state?.mode, state?.phase, policy, updateConversation, push],
  )

  const changePolicy = useCallback(
    async (name: string) => {
      if (name === policy) return
      const previous = policy
      setPolicy(name)
      try {
        await setDefaultPolicy(name)
        void system.refreshCatalog()
        push(`Routing policy set to ${name}.`, 'success')
      } catch (err) {
        setPolicy(previous)
        push(errorMessage(err), 'error')
      }
    },
    [policy, system, push],
  )

  const changeModel = useCallback(
    (id: string) => {
      patchActive((current) => ({ ...current, model: id }))
    },
    [patchActive],
  )

  return (
    <div className="app">
      <Sidebar
        conversations={allConversations}
        activeId={activeId}
        mode={mode}
        connected={system.connected}
        theme={theme}
        open={sidebarOpen}
        onClose={() => setSidebarOpen(false)}
        onSelect={handleSelectConversation}
        onNew={handleNewConversation}
        onDelete={deleteConversation}
        onToggleTheme={toggle}
      />

      <main className="main-panel">
        <header className="topbar">
          <button className="icon-button menu-button" onClick={() => setSidebarOpen(true)} aria-label="Open sidebar">
            <Menu size={20} />
          </button>
          <div className="topbar-title">
            <span className="crumb">{mode === 'ml' ? 'ML Studio' : 'Chat'}</span>
            <span className="crumb-sep">/</span>
            <span className="crumb-current">
              {mode === 'ml' ? 'Vision & generation' : (active?.title ?? 'New conversation')}
            </span>
          </div>
          <div className="topbar-actions">
            <span className={`lease-badge ${state?.exclusive ? 'on' : ''}`} title="One subsystem holds the accelerator at a time">
              <Cpu size={14} />
              1 lease
            </span>
            <ModeSwitch state={state} onSelect={(next) => void switchMode(next)} />
          </div>
        </header>

        <div className="main-content">
          {transitioning ? (
            <TransitionScreen state={state} onRetry={(next) => void switchMode(next)} />
          ) : mode === 'ml' ? (
            <MLStudio state={state} catalog={catalog} onToast={push} />
          ) : (
            <ChatView
              conversation={active}
              models={catalog?.llm ?? []}
              policies={catalog?.routing_policies ?? []}
              policy={policy}
              streaming={streaming}
              active={mode === 'llm'}
              onSend={(text) => void sendMessage(text)}
              onStop={() => abortRef.current?.abort()}
              onSelectModel={changeModel}
              onSelectPolicy={(name) => void changePolicy(name)}
            />
          )}
        </div>
      </main>

      <ToastStack toasts={toasts} onDismiss={dismiss} />
    </div>
  )
}

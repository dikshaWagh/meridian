import { useCallback, useEffect, useState } from 'react'
import type { Conversation } from '../types'

const STORAGE_KEY = 'meridian.conversations.v2'
const ACTIVE_KEY = 'meridian.activeConversation.v2'

function uid(): string {
  if (typeof crypto !== 'undefined' && 'randomUUID' in crypto) return crypto.randomUUID()
  return `c_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 8)}`
}

function newConversation(model = ''): Conversation {
  return {
    id: uid(),
    title: 'New conversation',
    model,
    messages: [],
    updatedAt: Date.now(),
  }
}

function load(): Conversation[] {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (!raw) return [newConversation()]
    const parsed = JSON.parse(raw) as Conversation[]
    if (!Array.isArray(parsed) || parsed.length === 0) return [newConversation()]
    return parsed.filter((c) => c && typeof c.id === 'string' && Array.isArray(c.messages))
  } catch {
    return [newConversation()]
  }
}

export function useConversations() {
  const [conversations, setConversations] = useState<Conversation[]>(load)
  const [activeId, setActiveId] = useState<string>(() => {
    try {
      const stored = localStorage.getItem(ACTIVE_KEY)
      if (stored) return stored
    } catch {
      // Fall through to the first conversation.
    }
    return load()[0]?.id ?? ''
  })

  useEffect(() => {
    const timer = window.setTimeout(() => {
      try {
        localStorage.setItem(STORAGE_KEY, JSON.stringify(conversations))
        localStorage.setItem(ACTIVE_KEY, activeId)
      } catch {
        // Storage can be unavailable in private windows; chat still works.
      }
    }, 200)
    return () => window.clearTimeout(timer)
  }, [conversations, activeId])

  const active = conversations.find((c) => c.id === activeId) ?? conversations[0]

  const updateConversation = useCallback((id: string, update: (c: Conversation) => Conversation) => {
    setConversations((current) => current.map((c) => (c.id === id ? update(c) : c)))
  }, [])

  const patchActive = useCallback(
    (update: (c: Conversation) => Conversation) => {
      if (!active) return
      updateConversation(active.id, update)
    },
    [active, updateConversation],
  )

  const createConversation = useCallback((model: string) => {
    const conversation = newConversation(model)
    setConversations((current) => [conversation, ...current])
    setActiveId(conversation.id)
    return conversation.id
  }, [])

  const deleteConversation = useCallback(
    (id: string) => {
      const next = conversations.filter((c) => c.id !== id)
      const ensured = next.length > 0 ? next : [newConversation()]
      setConversations(ensured)
      if (active?.id === id) setActiveId(ensured[0].id)
    },
    [conversations, active?.id],
  )

  const ensureModel = useCallback((model: string) => {
    if (!model) return
    setConversations((current) => {
      let changed = false
      const next = current.map((c) => {
        if (c.model) return c
        changed = true
        return { ...c, model }
      })
      return changed ? next : current
    })
  }, [])

  return {
    conversations,
    active,
    activeId: active?.id ?? '',
    setActiveId,
    updateConversation,
    patchActive,
    createConversation,
    deleteConversation,
    ensureModel,
  }
}

export type ConversationsApi = ReturnType<typeof useConversations>

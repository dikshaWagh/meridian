import { useCallback, useEffect, useState } from 'react'
import type { Conversation } from '../types'

const STORAGE_KEY = 'meridian.conversations.v1'

function uid(): string {
  if (typeof crypto !== 'undefined' && 'randomUUID' in crypto) return crypto.randomUUID()
  return `c_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 8)}`
}

function load(): Conversation[] {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (!raw) return []
    const parsed = JSON.parse(raw) as Conversation[]
    // A reload cannot resume a stream, so nothing can still be pending.
    return parsed.map((c) => ({
      ...c,
      messages: c.messages.map((m) => ({ ...m, pending: false })),
    }))
  } catch {
    return []
  }
}

export function useConversations() {
  const [list, setList] = useState<Conversation[]>(load)
  const [activeId, setActiveId] = useState<string | null>(null)

  useEffect(() => {
    try {
      localStorage.setItem(STORAGE_KEY, JSON.stringify(list))
    } catch {
      // Storage full or disabled: the chat still works for this session.
    }
  }, [list])

  const create = useCallback((): string => {
    const conv: Conversation = { id: uid(), title: 'New chat', updatedAt: Date.now(), messages: [] }
    setList((prev) => [conv, ...prev])
    setActiveId(conv.id)
    return conv.id
  }, [])

  const update = useCallback((id: string, fn: (c: Conversation) => Conversation) => {
    setList((prev) => prev.map((c) => (c.id === id ? { ...fn(c), updatedAt: Date.now() } : c)))
  }, [])

  const remove = useCallback((id: string) => {
    setList((prev) => prev.filter((c) => c.id !== id))
    setActiveId((cur) => (cur === id ? null : cur))
  }, [])

  return { list, activeId, setActiveId, create, update, remove }
}

export { uid }

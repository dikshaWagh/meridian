import type { Conversation, Mode } from '../types'
import type { Theme } from '../hooks/useTheme'
import { ChevronDown, Moon, Plus, Sun, Trash, X } from './Icons'
import { CompassMark } from './CompassMark'

interface SidebarProps {
  conversations: Conversation[]
  activeId: string
  mode: Mode | undefined
  connected: boolean
  theme: Theme
  open: boolean
  onClose: () => void
  onSelect: (id: string) => void
  onNew: () => void
  onDelete: (id: string) => void
  onToggleTheme: () => void
}

const modeLabel: Record<Mode, string> = {
  idle: 'Starting',
  llm: 'Chat',
  ml: 'ML Studio',
}

export function Sidebar({
  conversations,
  activeId,
  mode,
  connected,
  theme,
  open,
  onClose,
  onSelect,
  onNew,
  onDelete,
  onToggleTheme,
}: SidebarProps) {
  return (
    <>
      {open && <button className="sidebar-scrim" aria-label="Close sidebar" onClick={onClose} />}
      <aside className={`sidebar ${open ? 'open' : ''}`}>
        <div className="brand">
          <div className="brand-mark">
            <CompassMark width={34} height={34} />
          </div>
          <div className="brand-text">
            <div className="brand-name">Meridian</div>
            <div className="brand-sub">Exclusive AI serving</div>
          </div>
          <button className="icon-button sidebar-close" onClick={onClose} aria-label="Close sidebar">
            <X size={18} />
          </button>
        </div>

        <button className="new-chat" onClick={onNew}>
          <Plus size={18} />
          <span>New conversation</span>
        </button>

        <div className="conversation-list">
          <div className="list-label">Recent</div>
          {conversations.map((conversation) => (
            <div
              key={conversation.id}
              className={`conversation ${conversation.id === activeId ? 'active' : ''}`}
              role="button"
              tabIndex={0}
              onClick={() => onSelect(conversation.id)}
              onKeyDown={(event) => {
                if (event.key === 'Enter' || event.key === ' ') {
                  event.preventDefault()
                  onSelect(conversation.id)
                }
              }}
            >
              <div className="conversation-body">
                <div className="conversation-title">{conversation.title}</div>
                <div className="conversation-meta">
                  {conversation.messages.length > 0
                    ? `${conversation.messages.length} messages`
                    : 'Empty'}
                  {conversation.model ? ` · ${conversation.model}` : ''}
                </div>
              </div>
              <button
                className="icon-button subtle conversation-delete"
                aria-label="Delete conversation"
                onClick={(event) => {
                  event.stopPropagation()
                  onDelete(conversation.id)
                }}
              >
                <Trash size={15} />
              </button>
            </div>
          ))}
        </div>

        <div className="sidebar-footer">
          <div className="status-line" title={connected ? 'Live updates connected' : 'Reconnecting to server'}>
            <span className={`dot ${connected ? 'live' : 'off'}`} />
            <span>{connected ? 'Live' : 'Reconnecting'}</span>
            <span className="status-sep">·</span>
            <span>{modeLabel[mode ?? 'idle']}</span>
          </div>
          <button
            className="icon-button theme-toggle"
            onClick={onToggleTheme}
            title={`Switch to ${theme === 'light' ? 'dark' : 'light'} appearance`}
            aria-label="Toggle appearance"
          >
            {theme === 'light' ? <Moon size={18} /> : <Sun size={18} />}
          </button>
        </div>
        <div className="sidebar-footnote">
          <ChevronDown size={12} />
          One accelerator, one active subsystem.
        </div>
      </aside>
    </>
  )
}

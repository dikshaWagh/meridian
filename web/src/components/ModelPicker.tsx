import type { CatalogModel } from '../types'
import { Check, ChevronDown, Cpu } from './Icons'
import { Popover } from './Popover'

interface ModelPickerProps {
  models: CatalogModel[]
  selected: string
  onSelect: (id: string) => void
}

function stateDot(state: string): string {
  if (state === 'ready') return 'live'
  if (state === 'starting') return 'warn'
  return 'off'
}

export function ModelPicker({ models, selected, onSelect }: ModelPickerProps) {
  const current = models.find((m) => m.id === selected)

  return (
    <Popover
      width={340}
      trigger={(open, toggle) => (
        <button className="picker-trigger" onClick={toggle} aria-expanded={open} aria-label="Choose model">
          <Cpu size={16} />
          <span className="picker-value">{selected || 'Choose a model'}</span>
          {current?.context_window ? <span className="picker-meta">{current.context_window} ctx</span> : null}
          <ChevronDown size={14} className={open ? 'rotated' : ''} />
        </button>
      )}
    >
      {(close) => (
        <>
          <div className="picker-heading">
            <span>Language models</span>
            <span className="picker-heading-note">Local · llama.cpp</span>
          </div>
          {models.length === 0 && <div className="picker-empty">No LLM models are configured.</div>}
          {models.map((model) => (
            <button
              key={model.id}
              className={`picker-item ${model.id === selected ? 'active' : ''}`}
              onClick={() => {
                onSelect(model.id)
                close()
              }}
            >
              <span className={`dot ${stateDot(model.state)}`} />
              <span className="picker-item-main">
                <span className="picker-item-title">{model.id}</span>
                <span className="picker-item-sub">
                  {model.healthy_workers ?? 0}/{model.workers ?? 0} workers ready
                  {model.context_window ? ` · ${model.context_window} token window` : ''}
                </span>
              </span>
              {model.id === selected && <Check size={16} />}
            </button>
          ))}
        </>
      )}
    </Popover>
  )
}

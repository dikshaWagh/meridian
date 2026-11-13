import type { RoutingPolicy } from '../types'
import { Check, ChevronDown, Sliders } from './Icons'
import { Popover } from './Popover'

interface PolicyPickerProps {
  policies: RoutingPolicy[]
  selected: string
  onSelect: (name: string) => void
}

export function PolicyPicker({ policies, selected, onSelect }: PolicyPickerProps) {
  return (
    <Popover
      width={330}
      trigger={(open, toggle) => (
        <button className="picker-trigger" onClick={toggle} aria-expanded={open} aria-label="Choose routing policy">
          <Sliders size={16} />
          <span className="picker-value">{selected || 'Routing policy'}</span>
          <span className="picker-chevron">
            <ChevronDown size={12} className={open ? 'rotated' : ''} />
          </span>
        </button>
      )}
    >
      {(close) => (
        <>
          <div className="picker-heading">
            <span>Routing policy</span>
            <span className="picker-heading-note">New conversations</span>
          </div>
          {policies.map((policy) => (
            <button
              key={policy.name}
              className={`picker-item ${policy.name === selected ? 'active' : ''}`}
              onClick={() => {
                onSelect(policy.name)
                close()
              }}
            >
              <span className="policy-mark" aria-hidden="true" />
              <span className="picker-item-main">
                <span className="picker-item-title">{policy.name}</span>
                <span className="picker-item-sub">{policy.description}</span>
              </span>
              {policy.name === selected && <Check size={16} />}
            </button>
          ))}
          <div className="picker-footer">Requests in this chat pin the selected policy.</div>
        </>
      )}
    </Popover>
  )
}

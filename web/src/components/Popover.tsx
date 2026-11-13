import { useEffect, useRef, useState, type ReactNode } from 'react'

interface PopoverProps {
  trigger: (open: boolean, toggle: () => void) => ReactNode
  children: (close: () => void) => ReactNode
  align?: 'left' | 'right'
  width?: number
}

export function Popover({ trigger, children, align = 'left', width = 300 }: PopoverProps) {
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!open) return
    const onPointerDown = (event: MouseEvent) => {
      if (ref.current && !ref.current.contains(event.target as Node)) setOpen(false)
    }
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setOpen(false)
    }
    document.addEventListener('mousedown', onPointerDown)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onPointerDown)
      document.removeEventListener('keydown', onKey)
    }
  }, [open])

  return (
    <div className="popover" ref={ref}>
      {trigger(open, () => setOpen((v) => !v))}
      {open && (
        <div className="popover-panel" data-align={align} style={{ width }}>
          {children(() => setOpen(false))}
        </div>
      )}
    </div>
  )
}

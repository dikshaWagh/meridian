import { useEffect, useRef, useState } from 'react'
import { Icon } from './Icon'

interface CodeBlockProps {
  code: string
  label?: string
  className?: string
  maxHeight?: string
}

export function CodeBlock({ code, label, className = '', maxHeight = 'none' }: CodeBlockProps) {
  const [copied, setCopied] = useState(false)
  const timer = useRef<number | undefined>(undefined)

  useEffect(() => {
    return () => {
      if (timer.current !== undefined) window.clearTimeout(timer.current)
    }
  }, [])

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(code)
      setCopied(true)
      if (timer.current !== undefined) window.clearTimeout(timer.current)
      timer.current = window.setTimeout(() => setCopied(false), 1600)
    } catch {
      setCopied(false)
    }
  }

  return (
    <div
      className={`min-w-0 max-w-full overflow-hidden rounded-none border border-line bg-panel-alt ${className}`}
    >
      <div className="flex items-center justify-between gap-3 border-b border-line px-3 py-2">
        <span className="mono-label">{label ?? 'code'}</span>
        <button
          type="button"
          onClick={copy}
          className="inline-flex items-center gap-1.5 rounded-none px-2 py-1 font-mono text-[10px] uppercase tracking-wider text-dim transition-colors hover:bg-black/[0.05] hover:text-ink"
          aria-label="Copy code"
        >
          <Icon name={copied ? 'check' : 'copy'} className="h-3.5 w-3.5" />
          {copied ? 'Copied' : 'Copy'}
        </button>
      </div>
      <pre
        className="overflow-auto p-4 font-mono text-[12px] leading-relaxed text-ink sm:text-[12.5px]"
        style={{ maxHeight }}
      >
        <code>{code}</code>
      </pre>
      <span className="sr-only" role="status" aria-live="polite">
        {copied ? 'Code copied to clipboard' : ''}
      </span>
    </div>
  )
}

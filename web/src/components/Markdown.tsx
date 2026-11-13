import { Fragment, useMemo, type ReactNode } from 'react'

function inline(text: string, keyPrefix: string): ReactNode[] {
  const parts = text.split(/(\*\*[^*]+\*\*|`[^`]+`|\*[^*]+\*)/g)
  return parts.filter(Boolean).map((part, index) => {
    const key = `${keyPrefix}-${index}`
    if (part.startsWith('**') && part.endsWith('**')) {
      return <strong key={key}>{part.slice(2, -2)}</strong>
    }
    if (part.startsWith('`') && part.endsWith('`')) {
      return (
        <code className="inline-code" key={key}>
          {part.slice(1, -1)}
        </code>
      )
    }
    if (part.startsWith('*') && part.endsWith('*') && part.length > 2) {
      return <em key={key}>{part.slice(1, -1)}</em>
    }
    return <Fragment key={key}>{part}</Fragment>
  })
}

function prose(text: string): ReactNode[] {
  const lines = text.split('\n')
  const blocks: ReactNode[] = []
  let paragraph: string[] = []
  let list: { ordered: boolean; items: string[] } | null = null

  const flushParagraph = () => {
    if (paragraph.length === 0) return
    const content = paragraph.join(' ').trim()
    if (content) blocks.push(<p key={`p-${blocks.length}`}>{inline(content, `p-${blocks.length}`)}</p>)
    paragraph = []
  }
  const flushList = () => {
    if (!list) return
    const Tag = list.ordered ? 'ol' : 'ul'
    blocks.push(
      <Tag key={`l-${blocks.length}`}>
        {list.items.map((item, index) => (
          <li key={index}>{inline(item, `l-${blocks.length}-${index}`)}</li>
        ))}
      </Tag>,
    )
    list = null
  }

  for (const line of lines) {
    const trimmed = line.trim()
    if (!trimmed) {
      flushParagraph()
      flushList()
      continue
    }
    const heading = /^(#{1,3})\s+(.*)$/.exec(trimmed)
    if (heading) {
      flushParagraph()
      flushList()
      const level = heading[1].length
      const content = inline(heading[2], `h-${blocks.length}`)
      blocks.push(
        level === 1 ? (
          <h3 key={`h-${blocks.length}`}>{content}</h3>
        ) : level === 2 ? (
          <h4 key={`h-${blocks.length}`}>{content}</h4>
        ) : (
          <h5 key={`h-${blocks.length}`}>{content}</h5>
        ),
      )
      continue
    }
    const bullet = /^[-*]\s+(.*)$/.exec(trimmed)
    if (bullet) {
      flushParagraph()
      if (!list || list.ordered) {
        flushList()
        list = { ordered: false, items: [] }
      }
      list.items.push(bullet[1])
      continue
    }
    const numbered = /^\d+\.\s+(.*)$/.exec(trimmed)
    if (numbered) {
      flushParagraph()
      if (!list || !list.ordered) {
        flushList()
        list = { ordered: true, items: [] }
      }
      list.items.push(numbered[1])
      continue
    }
    flushList()
    paragraph.push(trimmed)
  }
  flushParagraph()
  flushList()
  return blocks
}

export function Markdown({ text }: { text: string }) {
  const blocks = useMemo(() => {
    const output: ReactNode[] = []
    const fence = /```([\w-]*)\n([\s\S]*?)```/g
    let last = 0
    let match: RegExpExecArray | null
    while ((match = fence.exec(text)) !== null) {
      if (match.index > last) {
        output.push(...prose(text.slice(last, match.index)))
      }
      output.push(
        <pre className="code-block" key={`code-${match.index}`}>
          <code>{match[2].replace(/\n$/, '')}</code>
        </pre>,
      )
      last = match.index + match[0].length
    }
    if (last < text.length) output.push(...prose(text.slice(last)))
    return output
  }, [text])

  return <div className="markdown">{blocks}</div>
}

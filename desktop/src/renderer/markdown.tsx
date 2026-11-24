import { isValidElement, useEffect, useRef, useState, type ReactNode } from 'react'
import ReactMarkdown, { type Components } from 'react-markdown'
import { Check, Copy } from 'lucide-react'
import { useTranslate } from './language'

function extractText(node: ReactNode): string {
  if (typeof node === 'string' || typeof node === 'number') return String(node)
  if (Array.isArray(node)) return node.map(extractText).join('')
  if (isValidElement<{ children?: ReactNode }>(node)) return extractText(node.props.children)
  return ''
}

function CodeBlock({ children }: { children?: ReactNode }) {
  const t = useTranslate()
  const [copied, setCopied] = useState(false)
  const timerRef = useRef<number | null>(null)

  useEffect(() => () => {
    if (timerRef.current !== null) window.clearTimeout(timerRef.current)
  }, [])

  let language = 'code'
  let codeText = ''
  if (isValidElement<{ className?: string; children?: ReactNode }>(children)) {
    const match = typeof children.props.className === 'string'
      ? /language-([\w+-]+)/.exec(children.props.className)
      : null
    if (match?.[1]) language = match[1]
    codeText = extractText(children.props.children)
  } else {
    codeText = extractText(children)
  }

  const handleCopy = async () => {
    try {
      await navigator.clipboard?.writeText(codeText.replace(/\n$/, ''))
      setCopied(true)
      if (timerRef.current !== null) window.clearTimeout(timerRef.current)
      timerRef.current = window.setTimeout(() => setCopied(false), 1800)
    } catch {
      /* ignore clipboard unavailability */
    }
  }

  return (
    <div className="code-block">
      <div className="code-block-header">
        <span className="code-lang">{language}</span>
        <button type="button" className="code-copy-button" onClick={() => void handleCopy()}>
          {copied ? <Check size={13} aria-hidden="true" /> : <Copy size={13} aria-hidden="true" />}
          <span>{t(copied ? 'Copied' : 'Copy')}</span>
        </button>
      </div>
      <pre>{children}</pre>
    </div>
  )
}

const previewComponents: Components = {
  pre: ({ children }) => <CodeBlock>{children}</CodeBlock>,
  img: ({ alt }) => <span>{alt}</span>,
  a: ({ children }) => <span>{children}</span>,
}

const defaultComponents: Components = {
  pre: ({ children }) => <CodeBlock>{children}</CodeBlock>,
}

export function MarkdownContent({ text, safePreview = false }: { text: string; safePreview?: boolean }) {
  return (
    <ReactMarkdown
      skipHtml={safePreview}
      components={safePreview ? previewComponents : defaultComponents}
    >
      {text}
    </ReactMarkdown>
  )
}

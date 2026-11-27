import { isValidElement, memo, useEffect, useMemo, useRef, useState, type ComponentProps, type ReactNode } from 'react'
import ReactMarkdown, { type Components } from 'react-markdown'
import remarkGfm from 'remark-gfm'
import rehypeHighlight from 'rehype-highlight'
import { Check, Copy } from 'lucide-react'
import { useTranslation } from 'react-i18next'

function extractText(node: ReactNode): string {
  if (typeof node === 'string' || typeof node === 'number') return String(node)
  if (Array.isArray(node)) return node.map(extractText).join('')
  if (isValidElement<{ children?: ReactNode }>(node)) return extractText(node.props.children)
  return ''
}

function CodeBlock({ children }: { children?: ReactNode }) {
  const { t } = useTranslation()
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
    <div className="code-block my-3.5 overflow-hidden rounded-[12px] border border-line-subtle bg-code shadow-[0_1px_2px_light-dark(rgb(0_0_0/.03),rgb(0_0_0/.2))]">
      <div className="code-block-header flex items-center justify-between border-b border-line-subtle bg-muted/85 shadow-[inset_0_1px_0_var(--glass-highlight)] px-3.5 py-[7px]">
        <span className="code-lang rounded-[5px] border border-line-subtle bg-canvas/70 px-1.5 py-[1px] font-mono text-[11.5px] lowercase text-ink-secondary">{language}</span>
        <button
          type="button"
          className="code-copy-button inline-flex cursor-pointer items-center gap-[5px] rounded-[6px] border-0 bg-transparent px-2 py-[3px] text-[12px] text-ink-secondary transition-[background,color] duration-120 hover:bg-hover hover:text-ink"
          onClick={() => void handleCopy()}
        >
          {copied ? <Check size={13} className="text-ok" aria-hidden="true" /> : <Copy size={13} aria-hidden="true" />}
          <span className={copied ? 'text-ok' : undefined}>{t(copied ? 'Copied' : 'Copy')}</span>
        </button>
      </div>
      <pre className="m-0 overflow-auto bg-transparent px-4 py-3.5 text-ink">{children}</pre>
    </div>
  )
}

const previewComponents: Components = {
  pre: ({ children }) => <CodeBlock>{children}</CodeBlock>,
  table: ({ children }) => <div className="table-wrap"><table>{children}</table></div>,
  img: ({ alt }) => <span>{alt}</span>,
  a: ({ children }) => <span>{children}</span>,
}

const defaultComponents: Components = {
  pre: ({ children }) => <CodeBlock>{children}</CodeBlock>,
  table: ({ children }) => <div className="table-wrap"><table>{children}</table></div>,
}

type RemarkPlugins = NonNullable<ComponentProps<typeof ReactMarkdown>['remarkPlugins']>
type RehypePlugins = NonNullable<ComponentProps<typeof ReactMarkdown>['rehypePlugins']>

const REMARK_PLUGINS: RemarkPlugins = [remarkGfm]
const EMPTY_REHYPE_PLUGINS: RehypePlugins = []
const REHYPE_HIGHLIGHT_FULL: RehypePlugins = [[rehypeHighlight, { detect: true }]]
const REHYPE_HIGHLIGHT_STREAM: RehypePlugins = [[rehypeHighlight, { detect: false, ignoreMissing: true }]]

export const MarkdownContent = memo(function MarkdownContent({
  text,
  safePreview = false,
  streaming = false,
}: {
  text: string
  safePreview?: boolean
  streaming?: boolean
}) {
  const rehypePlugins = safePreview
    ? EMPTY_REHYPE_PLUGINS
    : streaming
      ? REHYPE_HIGHLIGHT_STREAM
      : REHYPE_HIGHLIGHT_FULL

  return (
    <ReactMarkdown
      skipHtml={safePreview}
      remarkPlugins={REMARK_PLUGINS}
      rehypePlugins={rehypePlugins}
      components={safePreview ? previewComponents : defaultComponents}
    >
      {text}
    </ReactMarkdown>
  )
})

const FENCE_RE = /^ {0,3}(`{3,}|~{3,})(.*)$/
const CONTINUATION_RE = /^(?: {2,}|\t|\||[*+-]\s|\d+[.)]\s)/

/**
 * Split streaming markdown into settled top-level blocks (each memoized forever once settled)
 * and the active trailing block. Never splits inside fenced code blocks, tables, or lists.
 */
export function splitStableMarkdownBlocks(text: string): { settled: string[]; tail: string } {
  if (!text || text.indexOf('\n\n') === -1) {
    return { settled: [], tail: text }
  }

  const settled: string[] = []
  let blockStart = 0
  let pos = 0
  let fenceChar = ''
  let fenceLen = 0
  let sawNonEmptyInBlock = false

  const len = text.length
  while (pos < len) {
    const nl = text.indexOf('\n', pos)
    const lineEnd = nl === -1 ? len : nl
    const line = text.slice(pos, lineEnd)
    const trimmed = line.trim()

    if (!fenceChar) {
      const m = FENCE_RE.exec(line)
      if (m && (m[1][0] !== '`' || !m[2].includes('`'))) {
        fenceChar = m[1][0]
        fenceLen = m[1].length
      }
    } else {
      const m = FENCE_RE.exec(line)
      if (m && m[1][0] === fenceChar && m[1].length >= fenceLen && m[2].trim() === '') {
        fenceChar = ''
        fenceLen = 0
      }
    }

    if (trimmed !== '') {
      sawNonEmptyInBlock = true
    } else if (!fenceChar && sawNonEmptyInBlock && nl !== -1) {
      // Look ahead after one or more blank lines: only split if another non-empty line starts safely.
      let probe = nl + 1
      while (probe < len) {
        const nextNl = text.indexOf('\n', probe)
        const nextEnd = nextNl === -1 ? len : nextNl
        const nextLine = text.slice(probe, nextEnd)
        if (nextLine.trim() !== '') {
          if (!CONTINUATION_RE.test(nextLine)) {
            const slice = text.slice(blockStart, nl).trimEnd()
            if (slice) settled.push(slice)
            blockStart = probe
            sawNonEmptyInBlock = false
          }
          break
        }
        if (nextNl === -1) break
        probe = nextNl + 1
      }
    }

    if (nl === -1) break
    pos = nl + 1
  }

  const tail = text.slice(blockStart)
  return { settled, tail }
}

export const StreamingMarkdown = memo(function StreamingMarkdown({ text }: { text: string }) {
  const { settled, tail } = useMemo(() => splitStableMarkdownBlocks(text), [text])
  return (
    <>
      {settled.map((block, index) => (
        <MarkdownContent key={index} text={block} />
      ))}
      {tail ? <MarkdownContent text={tail} streaming /> : null}
    </>
  )
})

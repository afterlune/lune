import { memo, useEffect, useRef, useState, useSyncExternalStore } from 'react'
import { Bot, Brain, Check, ChevronRight, Copy, FileText, LoaderCircle, Paperclip, Pencil, Play, RotateCcw, Terminal, X } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { MarkdownContent, StreamingMarkdown } from './markdown'
import { extractProposedPlan, type HistoryAttachment, type LiveAgentRun, type LiveToolStep, type OptimisticMessage, type TimelineEntry } from './harness-types'
import type { StreamStore } from './stream-store'
import { btnPlain, btnPrimaryCompact, btnSecondary, cn } from './ui'

function CopyMessageButton({ text }: { text: string }) {
  const { t } = useTranslation()
  const [copied, setCopied] = useState(false)
  const timerRef = useRef<number | null>(null)

  useEffect(() => () => {
    if (timerRef.current !== null) window.clearTimeout(timerRef.current)
  }, [])

  const copy = async () => {
    try {
      await navigator.clipboard?.writeText(text)
      setCopied(true)
      if (timerRef.current !== null) window.clearTimeout(timerRef.current)
      timerRef.current = window.setTimeout(() => setCopied(false), 1800)
    } catch {
      /* ignore clipboard unavailability */
    }
  }

  return (
    <button
      type="button"
      className="message-action-btn inline-grid h-7 w-7 cursor-pointer place-items-center rounded-[7px] border-0 bg-transparent text-ink-muted transition-[background,color] duration-120 hover:bg-hover hover:text-ink"
      aria-label={t('Copy message')}
      title={t(copied ? 'Copied' : 'Copy message')}
      onClick={() => void copy()}
    >
      {copied ? <Check size={14} aria-hidden="true" /> : <Copy size={14} aria-hidden="true" />}
    </button>
  )
}

function AssistantBody({
  text,
  canImplement,
  onImplementPlan,
  working,
}: {
  text: string
  canImplement: boolean
  onImplementPlan?: (mode: 'same_context' | 'new_context') => void
  working: boolean
}) {
  const { t } = useTranslation()
  const proposed = extractProposedPlan(text)
  if (!proposed) {
    return <MarkdownContent text={text} />
  }
  return (
    <>
      {proposed.before && <MarkdownContent text={proposed.before} />}
      <section className="plan-card my-3.5 overflow-hidden rounded-[12px] border border-line bg-subtle" aria-label={t('Proposed plan')}>
        <header className="plan-card-header flex items-center gap-2 border-b border-line-subtle bg-muted px-3.5 py-2.5 text-[13px] font-semibold text-ink">
          <FileText size={14} aria-hidden="true" />
          <span>{t('Proposed plan')}</span>
        </header>
        <div className="plan-card-body px-4 py-3.5">
          <MarkdownContent text={proposed.plan} />
        </div>
        {canImplement && onImplementPlan && (
          <footer className="plan-card-actions flex flex-wrap gap-2 border-t border-line-subtle px-4 pt-2.5 pb-3.5">
            <button type="button" className={btnPrimaryCompact} disabled={working} onClick={() => onImplementPlan('same_context')}>
              <Play size={13} aria-hidden="true" />
              <span>{t('Implement in this session')}</span>
            </button>
            <button type="button" className={btnSecondary} disabled={working} onClick={() => onImplementPlan('new_context')}>
              <span>{t('Implement in new session')}</span>
            </button>
          </footer>
        )}
      </section>
      {proposed.after && <MarkdownContent text={proposed.after} />}
    </>
  )
}

const TimelineHistory = memo(function TimelineHistory({
  entries,
  optimistic,
  canImplementPlan,
  canRewind,
  sessionBusy,
  working,
  hasActiveStream,
  latestExecutionId,
  detailsExpanded,
  onImplementPlan,
  onEditAndResend,
  onRegenerate,
  onInspectAgent,
}: {
  entries: TimelineEntry[]
  optimistic: OptimisticMessage[]
  canImplementPlan: boolean
  canRewind: boolean
  sessionBusy: boolean
  working: boolean
  hasActiveStream: boolean
  latestExecutionId: string
  detailsExpanded: boolean
  onImplementPlan: (sourceExecutionId: string, mode: 'same_context' | 'new_context') => void
  onEditAndResend: (turnsFromEnd: number, nextText: string, attachments?: HistoryAttachment[]) => Promise<void> | void
  onRegenerate: () => Promise<void> | void
  onInspectAgent?: (agentId: string) => void
}) {
  const { t } = useTranslation()
  const [editingId, setEditingId] = useState<string | null>(null)
  const [editDraft, setEditDraft] = useState('')
  const editTextareaRef = useRef<HTMLTextAreaElement | null>(null)

  useEffect(() => {
    if (editingId && !entries.some((entry) => entry.kind === 'message' && entry.id === editingId)) {
      setEditingId(null)
      setEditDraft('')
    }
  }, [entries, editingId])

  useEffect(() => {
    if (editingId) {
      const el = editTextareaRef.current
      if (el) {
        el.focus()
        el.setSelectionRange(el.value.length, el.value.length)
      }
    }
  }, [editingId])

  let lastAssistantId = ''
  let userCount = 0
  const userOrderById = new Map<string, number>()
  for (let i = 0; i < entries.length; i++) {
    const item = entries[i]
    if (item.kind === 'message') {
      if (item.role === 'user') {
        userOrderById.set(item.id, userCount)
        userCount += 1
      } else if (item.role === 'assistant') {
        lastAssistantId = item.id
      }
    }
  }

  const canActOnMessages = canRewind && !sessionBusy && !working && !hasActiveStream && optimistic.length === 0
  const canEditHistoricalMessages = canRewind && !working

  return (
    <>
      {entries.map((entry) => {
        if (entry.kind === 'reasoning') {
          return (
            <details className="reasoning-block mb-3.5 rounded-[12px] border-[0.5px] border-line-subtle bg-subtle transition-[border-color] duration-150 hover:border-line" key={entry.id}>
              <summary className="group/sum flex cursor-pointer select-none items-center gap-[7px] px-3 py-2 text-[12.5px] font-medium text-ink-muted hover:text-ink">
                <ChevronRight size={13} className="disclosure-chevron" aria-hidden="true" />
                <Brain size={13} aria-hidden="true" />
                <span>{t('Thought process')}</span>
                {entry.durationMs !== undefined && entry.durationMs > 0 && (
                  <span className="reasoning-dur ml-auto font-mono text-[11px] text-ink-faint tabular-nums">
                    {entry.durationMs < 1000 ? `${entry.durationMs}ms` : `${(entry.durationMs / 1000).toFixed(1)}s`}
                  </span>
                )}
              </summary>
              <div className="reasoning-body border-t border-line-subtle px-3.5 pb-3 text-[13.5px] leading-[1.65] text-ink-secondary">
                <MarkdownContent text={entry.text} />
              </div>
            </details>
          )
        }

        if (entry.kind === 'tool_round') {
          const names = [...new Set(entry.calls.map((c) => c.name))].slice(0, 3).join(', ')
          const hasFailure = entry.calls.some((c) => c.failed)
          const hasTeamCall = entry.calls.some((c) => c.name === 'delegate_team')
          return (
            <details
              className={cn(
                'tool-record my-3 overflow-hidden rounded-[12px] border-[0.5px] bg-subtle text-[13px] transition-[border-color] duration-150',
                hasFailure ? 'has-failure border-danger-border' : 'border-line-subtle hover:border-line',
              )}
              key={entry.id}
            >
              <summary className="flex cursor-pointer select-none items-center gap-2 px-3.5 py-2.5 font-medium text-ink-secondary hover:text-ink">
                <ChevronRight size={14} className="disclosure-chevron" aria-hidden="true" />
                <Terminal size={14} aria-hidden="true" />
                <span>{t('Tool calls')}</span>
                {names && <span className="tool-summary-names truncate font-mono text-[11.5px] text-ink-muted">{names}</span>}
                {hasTeamCall && onInspectAgent && (
                  <button
                    type="button"
                    className="ml-2 inline-flex cursor-pointer items-center gap-1 rounded-[6px] border border-line bg-canvas px-2 py-[1px] font-sans text-[11px] font-medium text-ink-secondary hover:bg-hover hover:text-ink"
                    onClick={(e) => {
                      e.preventDefault()
                      e.stopPropagation()
                      onInspectAgent('')
                    }}
                  >
                    <Bot size={11} aria-hidden="true" />
                    <span>{t('Inspect subagents')}</span>
                  </button>
                )}
                <span className="tool-count ml-auto rounded-full border border-line bg-canvas px-2 py-[1px] font-mono text-[11.5px] text-ink-muted tabular-nums">
                  {entry.calls.length}
                </span>
              </summary>
              <div className="tool-record-list flex flex-col gap-1.5 px-2.5 pb-2.5">
                {entry.calls.map((call) => (
                  <details
                    key={call.callId}
                    className={cn('overflow-hidden rounded-[9px] border border-line-subtle bg-canvas', call.failed && 'failed border-danger-border')}
                  >
                    <summary className="flex cursor-pointer items-center gap-[7px] px-3 py-2 font-mono text-[12.5px] text-ink">
                      <ChevronRight size={13} className="disclosure-chevron" aria-hidden="true" />
                      <span>{call.name}</span>
                      {call.failed && (
                        <span className="tool-failed-tag ml-auto rounded bg-danger-bg px-1.5 py-[1px] font-sans text-[11px] text-danger">
                          {t('Failed')}
                        </span>
                      )}
                    </summary>
                    <pre className="m-0 max-h-[280px] overflow-auto border-t border-line-subtle bg-code px-3.5 py-2.5 font-mono text-[12.5px] leading-[1.6] text-ink-secondary whitespace-pre-wrap [overflow-wrap:anywhere]">
                      {call.arguments}
                    </pre>
                    {call.output !== undefined && (
                      <pre className="m-0 max-h-[280px] overflow-auto border-t border-line-subtle bg-code px-3.5 py-2.5 font-mono text-[12.5px] leading-[1.6] text-ink-secondary whitespace-pre-wrap [overflow-wrap:anywhere]">
                        {call.output}
                      </pre>
                    )}
                  </details>
                ))}
              </div>
            </details>
          )
        }

        const isLatestAssistant = entry.role === 'assistant' && entry.id === lastAssistantId
        const showPlanActions = isLatestAssistant && canImplementPlan && !sessionBusy && !hasActiveStream && Boolean(latestExecutionId)
        const isEditingUser = entry.role === 'user' && editingId === entry.id
        const userIndex = entry.role === 'user' ? userOrderById.get(entry.id) : undefined
        const turnsFromEnd = userIndex !== undefined ? userCount - userIndex : 1

        const submitEditedUser = () => {
          const trimmed = editDraft.trim()
          if ((!trimmed && !entry.attachments?.length) || working) return
          setEditingId(null)
          void onEditAndResend(turnsFromEnd, trimmed, entry.attachments)
        }

        return (
          <article
            className={cn(
              'message group/msg relative mb-6 flex flex-col',
              entry.role === 'user' ? 'user mt-2 items-end' : 'assistant items-stretch',
              isEditingUser && 'editing',
            )}
            key={entry.id}
            data-turn-id={entry.role === 'user' ? entry.id : undefined}
          >
            <div
              className={cn(
                'message-content',
                entry.role === 'user'
                  ? (isEditingUser
                      ? 'w-[min(100%,560px)] max-w-[min(92%,560px)] rounded-[20px] border-0 bg-[var(--bg-glass-strong)] backdrop-blur-lg px-3.5 py-2.5 shadow-elev-md'
                      : 'max-w-[min(88%,560px)] sm:max-w-[min(74%,560px)] rounded-[20px] border-0 bg-user-pill px-4 py-2.5 shadow-[0_0_0_0.5px_var(--border-subtle)]')
                  : 'w-full min-w-0',
              )}
            >
              {isEditingUser ? (
                <div className="message-edit-box flex flex-col gap-2">
                  <textarea
                    ref={editTextareaRef}
                    className="message-edit-textarea max-h-[240px] min-h-[48px] w-full resize-y border-0 bg-transparent p-1 text-[14.5px] leading-[1.6] text-ink outline-none"
                    value={editDraft}
                    rows={Math.min(10, Math.max(2, editDraft.split('\n').length))}
                    disabled={working}
                    aria-label={t('Edit and resend')}
                    onChange={(event) => setEditDraft(event.target.value)}
                    onKeyDown={(event) => {
                      if (event.key === 'Escape') {
                        event.preventDefault()
                        setEditingId(null)
                        return
                      }
                      if ((event.metaKey || event.ctrlKey) && event.key === 'Enter') {
                        event.preventDefault()
                        submitEditedUser()
                      }
                    }}
                  />
                  {entry.attachments && entry.attachments.length > 0 && (
                    <div className="px-1 text-[11.5px] text-ink-muted">
                      {t('Attachments from this message will be kept.')}
                    </div>
                  )}
                  <div className="message-edit-actions flex items-center justify-end gap-1.5">
                    <button
                      type="button"
                      className={btnPlain}
                      disabled={working}
                      onClick={() => setEditingId(null)}
                    >
                      {t('Cancel')}
                    </button>
                    <button
                      type="button"
                      className={btnPrimaryCompact}
                      disabled={(!editDraft.trim() && !entry.attachments?.length) || working}
                      onClick={submitEditedUser}
                    >
                      {t('Save & Resend')}
                    </button>
                  </div>
                </div>
              ) : (
                <div className="markdown-body">
                  {entry.role === 'assistant' ? (
                    <AssistantBody
                      text={entry.text}
                      canImplement={showPlanActions}
                      working={working}
                      onImplementPlan={(mode) => onImplementPlan(latestExecutionId, mode)}
                    />
                  ) : (
                    <MarkdownContent text={entry.text} />
                  )}
                  {entry.attachments?.map((file, index) => (
                    <details
                      className="history-attachment mt-2.5 overflow-hidden rounded-[10px] border border-line-subtle bg-subtle text-[13px]"
                      key={index}
                      open={detailsExpanded}
                    >
                      <summary className="flex cursor-pointer select-none items-center gap-2 px-3.5 py-2.5 font-medium text-ink-secondary hover:text-ink">
                        <ChevronRight size={13} className="disclosure-chevron" aria-hidden="true" />
                        <Paperclip size={13} aria-hidden="true" />
                        <span>{file.name}</span>
                      </summary>
                      {file.image ? (
                        <img className="mx-auto block max-h-[360px] max-w-full object-contain p-2.5" src={file.image} alt={file.name} />
                      ) : (
                        <pre className="m-0 max-h-[280px] overflow-auto border-t border-line-subtle bg-code px-3.5 py-2.5 font-mono text-[12.5px] leading-[1.6] text-ink-secondary whitespace-pre-wrap [overflow-wrap:anywhere]">
                          {file.text}
                        </pre>
                      )}
                    </details>
                  ))}
                </div>
              )}
              {entry.role === 'assistant' && (
                <div className="message-actions mt-1.5 flex items-center gap-1 opacity-0 transition-opacity duration-150 group-hover/msg:opacity-100 group-focus-within/msg:opacity-100">
                  <CopyMessageButton text={entry.text} />
                  {isLatestAssistant && canActOnMessages && userCount > 0 && (
                    <button
                      type="button"
                      className="message-action-btn inline-grid h-7 w-7 cursor-pointer place-items-center rounded-[7px] border-0 bg-transparent text-ink-muted transition-[background,color] duration-120 hover:bg-hover hover:text-ink"
                      aria-label={t('Regenerate response')}
                      title={t('Regenerate response')}
                      onClick={() => void onRegenerate()}
                    >
                      <RotateCcw size={14} aria-hidden="true" />
                    </button>
                  )}
                </div>
              )}
            </div>
            {entry.role === 'user' && !isEditingUser && (
              <div className="message-actions user-actions mt-1 inline-flex items-center justify-end gap-0.5 rounded-[10px] border-0 bg-[var(--bg-glass)] backdrop-blur-md p-0.5 shadow-elev-sm opacity-0 transition-opacity duration-150 group-hover/msg:opacity-100 group-focus-within/msg:opacity-100">
                {entry.text.trim() && <CopyMessageButton text={entry.text} />}
                {canEditHistoricalMessages && (
                  <button
                    type="button"
                    className="message-action-btn inline-grid h-7 w-7 cursor-pointer place-items-center rounded-[7px] border-0 bg-transparent text-ink-muted transition-[background,color] duration-120 hover:bg-hover hover:text-ink"
                    aria-label={t('Edit and resend')}
                    title={t('Edit and resend')}
                    onClick={() => {
                      setEditingId(entry.id)
                      setEditDraft(entry.text)
                    }}
                  >
                    <Pencil size={13.5} aria-hidden="true" />
                  </button>
                )}
              </div>
            )}
          </article>
        )
      })}

      {optimistic.map((message) => (
        <article
          className={cn(
            'message relative mb-6 flex flex-col',
            message.role === 'user' ? 'user mt-2 items-end' : 'assistant items-stretch',
          )}
          key={message.id}
          data-turn-id={message.role === 'user' ? message.id : undefined}
        >
          <div
            className={cn(
              'message-content',
              message.role === 'user'
                ? 'max-w-[min(88%,560px)] sm:max-w-[min(74%,560px)] rounded-[20px] border-0 bg-user-pill px-4 py-2.5 shadow-[0_0_0_0.5px_var(--border-subtle)]'
                : 'w-full min-w-0',
            )}
          >
            <div className="markdown-body">
              <MarkdownContent text={message.text} />
              {message.attachments?.map((file, index) => (
                <details
                  className="history-attachment mt-2.5 overflow-hidden rounded-[10px] border border-line-subtle bg-subtle text-[13px]"
                  key={index}
                  open={detailsExpanded}
                >
                  <summary className="flex cursor-pointer select-none items-center gap-2 px-3.5 py-2.5 font-medium text-ink-secondary hover:text-ink">
                    <ChevronRight size={13} className="disclosure-chevron" aria-hidden="true" />
                    <Paperclip size={13} aria-hidden="true" />
                    <span>{file.name}</span>
                  </summary>
                  {file.image ? (
                    <img className="mx-auto block max-h-[360px] max-w-full object-contain p-2.5" src={file.image} alt={file.name} />
                  ) : (
                    <pre className="m-0 max-h-[280px] overflow-auto border-t border-line-subtle bg-code px-3.5 py-2.5 font-mono text-[12.5px] leading-[1.6] text-ink-secondary whitespace-pre-wrap [overflow-wrap:anywhere]">
                      {file.text}
                    </pre>
                  )}
                </details>
              ))}
            </div>
          </div>
        </article>
      ))}
    </>
  )
})

const LiveStreamSection = memo(function LiveStreamSection({ streamStore }: { streamStore: StreamStore }) {
  const { t } = useTranslation()
  const snap = useSyncExternalStore(streamStore.subscribe, streamStore.getSnapshot)
  const [reasoningOpen, setReasoningOpen] = useState(true)
  const userToggledReasoningRef = useRef(false)
  const reasoningBodyRef = useRef<HTMLDivElement | null>(null)
  const stickReasoningRef = useRef(true)
  const reasoningRafRef = useRef<number | null>(null)
  const reasoningLastTsRef = useRef(0)
  const reasoningTouchYRef = useRef<number | null>(null)

  useEffect(() => () => {
    if (reasoningRafRef.current !== null) window.cancelAnimationFrame(reasoningRafRef.current)
  }, [])

  useEffect(() => {
    if (!snap.reasoning) {
      userToggledReasoningRef.current = false
      setReasoningOpen(true)
      return
    }
    if (snap.text && !userToggledReasoningRef.current) {
      setReasoningOpen(false)
    }
  }, [Boolean(snap.reasoning), Boolean(snap.text)])

  useEffect(() => {
    if (!snap.reasoning) {
      stickReasoningRef.current = true
      return
    }
    if (!reasoningOpen || !stickReasoningRef.current || reasoningRafRef.current !== null) return

    const step = (ts: number) => {
      reasoningRafRef.current = null
      const el = reasoningBodyRef.current
      if (!el || !stickReasoningRef.current) {
        reasoningLastTsRef.current = 0
        return
      }
      const maxTop = Math.max(0, el.scrollHeight - el.clientHeight)
      const gap = maxTop - el.scrollTop
      if (gap <= 0.5) {
        if (gap > 0) el.scrollTop = maxTop
        reasoningLastTsRef.current = 0
        return
      }
      if (gap > 480) {
        el.scrollTop = maxTop
        reasoningLastTsRef.current = 0
        return
      }
      const dt = reasoningLastTsRef.current ? Math.min(0.05, Math.max(0.001, (ts - reasoningLastTsRef.current) / 1000)) : 1 / 120
      reasoningLastTsRef.current = ts
      const alpha = 1 - Math.exp(-20 * dt)
      el.scrollTop = Math.min(maxTop, el.scrollTop + Math.max(0.5, gap * alpha))
      reasoningRafRef.current = window.requestAnimationFrame(step)
    }

    reasoningRafRef.current = window.requestAnimationFrame(step)
  }, [snap.reasoning, reasoningOpen])

  return (
    <>
      {snap.reasoning && (
        <details className="reasoning-block live mb-3.5 rounded-[10px] border border-line-subtle bg-subtle" open={reasoningOpen}>
          <summary
            className="flex cursor-pointer select-none items-center gap-[7px] px-3 py-2 text-[12.5px] font-medium text-ink-muted hover:text-ink"
            onClick={(e) => {
              e.preventDefault()
              userToggledReasoningRef.current = true
              setReasoningOpen((v) => !v)
            }}
          >
            <ChevronRight size={13} className="disclosure-chevron" aria-hidden="true" />
            <Brain size={13} aria-hidden="true" />
            <span>{snap.text ? t('Thought process') : t('Thinking…')}</span>
          </summary>
          <div
            className="reasoning-body max-h-[220px] overflow-y-auto overscroll-contain [overflow-anchor:none] [scrollbar-gutter:stable] border-t border-line-subtle px-3.5 pb-3 text-[13.5px] leading-[1.65] text-ink-secondary"
            ref={reasoningBodyRef}
            onWheel={(e) => {
              if (e.deltaY < 0) stickReasoningRef.current = false
            }}
            onTouchStart={(e) => {
              reasoningTouchYRef.current = e.touches[0]?.clientY ?? null
            }}
            onTouchMove={(e) => {
              if (reasoningTouchYRef.current !== null && (e.touches[0]?.clientY ?? 0) > reasoningTouchYRef.current + 4) {
                stickReasoningRef.current = false
              }
            }}
            onScroll={() => {
              const el = reasoningBodyRef.current
              if (!el) return
              if (el.scrollHeight - el.scrollTop - el.clientHeight <= 24) {
                stickReasoningRef.current = true
              }
            }}
          >
            <StreamingMarkdown text={snap.reasoning} />
          </div>
        </details>
      )}

      {snap.text && (
        <article className="message assistant streaming-message relative mb-6 flex w-full min-w-0 flex-col items-stretch">
          <div className="message-content w-full min-w-0">
            <div className="markdown-body">
              <StreamingMarkdown text={snap.text} />
            </div>
          </div>
        </article>
      )}
    </>
  )
})

export function TimelineView({
  entries,
  optimistic,
  liveAgents = [],
  liveTools,
  streamStore,
  hasActiveStream,
  canImplementPlan,
  canRewind,
  sessionBusy,
  working,
  latestExecutionId,
  detailsExpanded,
  onImplementPlan,
  onEditAndResend,
  onRegenerate,
  onInspectAgent,
}: {
  entries: TimelineEntry[]
  optimistic: OptimisticMessage[]
  liveAgents?: LiveAgentRun[]
  liveTools: LiveToolStep[]
  streamStore: StreamStore
  hasActiveStream: boolean
  canImplementPlan: boolean
  canRewind: boolean
  sessionBusy: boolean
  working: boolean
  latestExecutionId: string
  detailsExpanded: boolean
  onImplementPlan: (sourceExecutionId: string, mode: 'same_context' | 'new_context') => void
  onEditAndResend: (turnsFromEnd: number, nextText: string, attachments?: HistoryAttachment[]) => Promise<void> | void
  onRegenerate: () => Promise<void> | void
  onInspectAgent?: (agentId: string) => void
}) {
  const { t } = useTranslation()

  return (
    <>
      <TimelineHistory
        entries={entries}
        optimistic={optimistic}
        canImplementPlan={canImplementPlan}
        canRewind={canRewind}
        sessionBusy={sessionBusy}
        working={working}
        hasActiveStream={hasActiveStream}
        latestExecutionId={latestExecutionId}
        detailsExpanded={detailsExpanded}
        onImplementPlan={onImplementPlan}
        onEditAndResend={onEditAndResend}
        onRegenerate={onRegenerate}
        onInspectAgent={onInspectAgent}
      />

      {liveAgents.length > 0 && (
        <div className="live-agents mb-3.5 flex flex-col gap-2 rounded-[10px] border border-line-subtle bg-subtle px-3.5 py-2.5" aria-live="polite">
          <div className="live-agents-head flex items-center gap-1.5 text-[12px] font-semibold text-ink-secondary">
            <Bot size={13} aria-hidden="true" />
            <span>{t('Subagents')}</span>
            <span className="live-agents-count ml-auto font-mono text-[11px] font-normal text-ink-muted">
              {liveAgents.filter((a) => a.status === 'running').length}/{liveAgents.length}
            </span>
            {onInspectAgent && (
              <button
                type="button"
                className="ml-1 inline-flex cursor-pointer items-center gap-0.5 rounded-[6px] border-0 bg-transparent px-1.5 py-0.5 text-[11px] font-medium text-ink-secondary hover:bg-hover hover:text-ink"
                onClick={() => onInspectAgent('')}
              >
                <span>{t('Details')}</span>
                <ChevronRight size={12} aria-hidden="true" />
              </button>
            )}
          </div>
          <div className="live-agents-list flex flex-col gap-1">
            {liveAgents.map((agent) => {
              const rowClass = cn(
                'live-agent-row flex w-full flex-wrap items-center gap-1.5 rounded-[7px] border-0 bg-transparent px-1.5 py-1 text-left text-[12px]',
                agent.status,
                agent.status === 'failed' || agent.status === 'cancelled' ? 'text-danger' : 'text-ink-secondary',
                onInspectAgent && 'cursor-pointer hover:bg-hover',
              )
              const inner = (
                <>
                  {agent.status === 'running' ? (
                    <LoaderCircle size={13} className="spin shrink-0" aria-hidden="true" />
                  ) : agent.status === 'failed' || agent.status === 'cancelled' ? (
                    <X size={13} className="live-agent-fail-icon shrink-0 text-danger" aria-hidden="true" />
                  ) : (
                    <Check size={13} className="shrink-0 text-ok" aria-hidden="true" />
                  )}
                  <span className="live-agent-name font-medium text-ink">{agent.name}</span>
                  {agent.role && <span className="live-agent-role rounded bg-muted px-1.5 py-[1px] font-mono text-[10.5px] text-ink-muted">{t(agent.role)}</span>}
                  {agent.modelProfile && <span className="font-mono text-[10.5px] text-ink-muted">{agent.modelProfile}</span>}
                  {(agent.activity || agent.task) && (
                    <small className="live-agent-task min-w-0 flex-1 truncate text-[11.5px] text-ink-muted" title={agent.task || agent.activity}>
                      {agent.status === 'running' && agent.activity ? `${t(agent.activity)}${agent.task ? ` · ${agent.task}` : ''}` : (agent.task || t(agent.activity || ''))}
                    </small>
                  )}
                  {agent.currentTool && <span className="live-agent-tool rounded bg-canvas px-1.5 py-[1px] font-mono text-[11px] text-ink-secondary">{agent.currentTool}</span>}
                  {agent.toolCount > 0 && !agent.currentTool && (
                    <span className="live-agent-meta font-mono text-[11px] text-ink-muted">{agent.toolCount} {t('tools')}</span>
                  )}
                  {agent.durationMs !== undefined && agent.status !== 'running' && (
                    <span className="live-tool-dur ml-auto font-mono text-[11px] text-ink-muted">
                      {agent.durationMs < 1000 ? `${agent.durationMs}ms` : `${(agent.durationMs / 1000).toFixed(1)}s`}
                    </span>
                  )}
                  {onInspectAgent && <ChevronRight size={12} className={cn('shrink-0 text-ink-muted', !(agent.durationMs !== undefined && agent.status !== 'running') && 'ml-auto')} aria-hidden="true" />}
                </>
              )
              return onInspectAgent ? (
                <button type="button" className={rowClass} key={agent.id} onClick={() => onInspectAgent(agent.id)}>
                  {inner}
                </button>
              ) : (
                <div className={rowClass} key={agent.id}>
                  {inner}
                </div>
              )
            })}
          </div>
        </div>
      )}

      {liveTools.length > 0 && (
        <div className="live-tools mb-4 flex flex-col gap-[5px] rounded-[10px] border border-line-subtle bg-subtle px-3.5 py-2.5" aria-live="polite">
          {liveTools.map((step) => (
            <div
              className={cn(
                'live-tool-step flex items-center gap-2 font-mono text-[12px]',
                step.status,
                step.status === 'failed' ? 'text-danger' : 'text-ink-secondary',
              )}
              key={step.id}
            >
              {step.status === 'running' ? (
                <LoaderCircle size={13} className="spin shrink-0" aria-hidden="true" />
              ) : (
                <Terminal size={13} className="shrink-0" aria-hidden="true" />
              )}
              <span className="live-tool-name">{step.name}</span>
              {step.durationMs !== undefined && step.status !== 'running' && (
                <span className="live-tool-dur ml-auto font-mono text-[11px] text-ink-muted">
                  {step.durationMs < 1000 ? `${step.durationMs}ms` : `${(step.durationMs / 1000).toFixed(1)}s`}
                </span>
              )}
              {step.detail && <small className="min-w-0 truncate text-[11px] text-ink-muted">{step.detail}</small>}
            </div>
          ))}
        </div>
      )}

      <LiveStreamSection streamStore={streamStore} />
    </>
  )
}

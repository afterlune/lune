import { useEffect, useRef, useState } from 'react'
import { Bot, Brain, Check, ChevronRight, Copy, FileText, LoaderCircle, Paperclip, Pencil, Play, RotateCcw, Terminal, X } from 'lucide-react'
import { useTranslate } from './language'
import { MarkdownContent } from './markdown'
import { extractProposedPlan, type LiveAgentRun, type LiveToolStep, type OptimisticMessage, type TimelineEntry } from './harness-types'

function CopyMessageButton({ text }: { text: string }) {
  const t = useTranslate()
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
      className="message-action-btn"
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
  const t = useTranslate()
  const proposed = extractProposedPlan(text)
  if (!proposed) {
    return <MarkdownContent text={text} />
  }
  return (
    <>
      {proposed.before && <MarkdownContent text={proposed.before} />}
      <section className="plan-card" aria-label={t('Proposed plan')}>
        <header className="plan-card-header">
          <FileText size={14} aria-hidden="true" />
          <span>{t('Proposed plan')}</span>
        </header>
        <div className="plan-card-body">
          <MarkdownContent text={proposed.plan} />
        </div>
        {canImplement && onImplementPlan && (
          <footer className="plan-card-actions">
            <button type="button" className="primary-button compact" disabled={working} onClick={() => onImplementPlan('same_context')}>
              <Play size={13} aria-hidden="true" />
              <span>{t('Implement in this session')}</span>
            </button>
            <button type="button" className="secondary-button" disabled={working} onClick={() => onImplementPlan('new_context')}>
              <span>{t('Implement in new session')}</span>
            </button>
          </footer>
        )}
      </section>
      {proposed.after && <MarkdownContent text={proposed.after} />}
    </>
  )
}

export function TimelineView({
  entries,
  optimistic,
  liveAgents = [],
  liveTools,
  streamingReasoning,
  streamingText,
  canImplementPlan,
  canRewind,
  sessionBusy,
  working,
  latestExecutionId,
  detailsExpanded,
  onImplementPlan,
  onEditAndResend,
  onRegenerate,
}: {
  entries: TimelineEntry[]
  optimistic: OptimisticMessage[]
  liveAgents?: LiveAgentRun[]
  liveTools: LiveToolStep[]
  streamingReasoning: string
  streamingText: string
  canImplementPlan: boolean
  canRewind: boolean
  sessionBusy: boolean
  working: boolean
  latestExecutionId: string
  detailsExpanded: boolean
  onImplementPlan: (sourceExecutionId: string, mode: 'same_context' | 'new_context') => void
  onEditAndResend: (turnsFromEnd: number, nextText: string) => Promise<void> | void
  onRegenerate: () => Promise<void> | void
}) {
  const t = useTranslate()
  const [editingId, setEditingId] = useState<string | null>(null)
  const [editDraft, setEditDraft] = useState('')
  const editTextareaRef = useRef<HTMLTextAreaElement | null>(null)

  useEffect(() => {
    setEditingId(null)
    setEditDraft('')
  }, [entries])

  useEffect(() => {
    if (sessionBusy && editingId) {
      setEditingId(null)
    }
  }, [sessionBusy, editingId])

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

  const canActOnMessages = canRewind && !sessionBusy && !working && !streamingText && optimistic.length === 0

  return (
    <>
      {entries.map((entry) => {
        if (entry.kind === 'reasoning') {
          return (
            <details className="reasoning-block" key={entry.id} open={detailsExpanded}>
              <summary>
                <ChevronRight size={13} className="disclosure-chevron" aria-hidden="true" />
                <Brain size={13} aria-hidden="true" />
                <span>{t('Thought process')}</span>
              </summary>
              <div className="reasoning-body">
                <MarkdownContent text={entry.text} />
              </div>
            </details>
          )
        }

        if (entry.kind === 'tool_round') {
          const names = [...new Set(entry.calls.map((c) => c.name))].slice(0, 3).join(', ')
          const hasFailure = entry.calls.some((c) => c.failed)
          return (
            <details className={`tool-record${hasFailure ? ' has-failure' : ''}`} key={entry.id} open={detailsExpanded}>
              <summary>
                <ChevronRight size={14} className="disclosure-chevron" aria-hidden="true" />
                <Terminal size={14} aria-hidden="true" />
                <span>{t('Tool calls')}</span>
                {names && <span className="tool-summary-names">{names}</span>}
                <span className="tool-count">{entry.calls.length}</span>
              </summary>
              <div className="tool-record-list">
                {entry.calls.map((call) => (
                  <details key={call.callId} className={call.failed ? 'failed' : undefined} open={detailsExpanded}>
                    <summary>
                      <ChevronRight size={13} className="disclosure-chevron" aria-hidden="true" />
                      <span>{call.name}</span>
                      {call.failed && <span className="tool-failed-tag">{t('Failed')}</span>}
                    </summary>
                    <pre>{call.arguments}</pre>
                    {call.output !== undefined && <pre>{call.output}</pre>}
                  </details>
                ))}
              </div>
            </details>
          )
        }

        const isLatestAssistant = entry.role === 'assistant' && entry.id === lastAssistantId
        const showPlanActions = isLatestAssistant && canImplementPlan && !sessionBusy && !streamingText && Boolean(latestExecutionId)
        const isEditingUser = entry.role === 'user' && editingId === entry.id
        const userIndex = entry.role === 'user' ? userOrderById.get(entry.id) : undefined
        const turnsFromEnd = userIndex !== undefined ? userCount - userIndex : 1

        const submitEditedUser = () => {
          const trimmed = editDraft.trim()
          if (!trimmed || working || sessionBusy) return
          setEditingId(null)
          void onEditAndResend(turnsFromEnd, trimmed)
        }

        return (
          <article className={`message ${entry.role}${isEditingUser ? ' editing' : ''}`} key={entry.id}>
            <div className="message-content">
              {isEditingUser ? (
                <div className="message-edit-box">
                  <textarea
                    ref={editTextareaRef}
                    className="message-edit-textarea"
                    value={editDraft}
                    rows={Math.min(10, Math.max(2, editDraft.split('\n').length))}
                    disabled={working || sessionBusy}
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
                  <div className="message-edit-actions">
                    <button
                      type="button"
                      className="plain-button"
                      disabled={working}
                      onClick={() => setEditingId(null)}
                    >
                      {t('Cancel')}
                    </button>
                    <button
                      type="button"
                      className="primary-button compact"
                      disabled={!editDraft.trim() || working || sessionBusy}
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
                    <details className="history-attachment" key={index} open={detailsExpanded}>
                      <summary>
                        <ChevronRight size={13} className="disclosure-chevron" aria-hidden="true" />
                        <Paperclip size={13} aria-hidden="true" />
                        <span>{file.name}</span>
                      </summary>
                      {file.image ? <img src={file.image} alt={file.name} /> : <pre>{file.text}</pre>}
                    </details>
                  ))}
                </div>
              )}
              {entry.role === 'assistant' && (
                <div className="message-actions">
                  <CopyMessageButton text={entry.text} />
                  {isLatestAssistant && canActOnMessages && userCount > 0 && (
                    <button
                      type="button"
                      className="message-action-btn"
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
              <div className="message-actions user-actions">
                {entry.text.trim() && <CopyMessageButton text={entry.text} />}
                {canActOnMessages && (
                  <button
                    type="button"
                    className="message-action-btn"
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
        <article className={`message ${message.role}`} key={message.id}>
          <div className="message-content">
            <div className="markdown-body">
              <MarkdownContent text={message.text} />
              {message.attachments?.map((file, index) => (
                <details className="history-attachment" key={index} open={detailsExpanded}>
                  <summary>
                    <ChevronRight size={13} className="disclosure-chevron" aria-hidden="true" />
                    <Paperclip size={13} aria-hidden="true" />
                    <span>{file.name}</span>
                  </summary>
                  {file.image ? <img src={file.image} alt={file.name} /> : <pre>{file.text}</pre>}
                </details>
              ))}
            </div>
          </div>
        </article>
      ))}

      {liveAgents.length > 0 && (
        <div className="live-agents" aria-live="polite">
          <div className="live-agents-head">
            <Bot size={13} aria-hidden="true" />
            <span>{t('Subagents')}</span>
            <span className="live-agents-count">
              {liveAgents.filter((a) => a.status === 'running').length}/{liveAgents.length}
            </span>
          </div>
          <div className="live-agents-list">
            {liveAgents.map((agent) => (
              <div className={`live-agent-row ${agent.status}`} key={agent.id}>
                {agent.status === 'running' ? (
                  <LoaderCircle size={13} className="spin" aria-hidden="true" />
                ) : agent.status === 'failed' ? (
                  <X size={13} className="live-agent-fail-icon" aria-hidden="true" />
                ) : (
                  <Check size={13} aria-hidden="true" />
                )}
                <span className="live-agent-name">{agent.name}</span>
                {agent.role && <span className="live-agent-role">{agent.role}</span>}
                {agent.task && <small className="live-agent-task" title={agent.task}>{agent.task}</small>}
                {agent.currentTool && <span className="live-agent-tool">{agent.currentTool}</span>}
                {agent.toolCount > 0 && !agent.currentTool && (
                  <span className="live-agent-meta">{agent.toolCount} {t('tools')}</span>
                )}
                {agent.durationMs !== undefined && agent.status !== 'running' && (
                  <span className="live-tool-dur">
                    {agent.durationMs < 1000 ? `${agent.durationMs}ms` : `${(agent.durationMs / 1000).toFixed(1)}s`}
                  </span>
                )}
              </div>
            ))}
          </div>
        </div>
      )}

      {liveTools.length > 0 && (
        <div className="live-tools" aria-live="polite">
          {liveTools.map((step) => (
            <div className={`live-tool-step ${step.status}`} key={step.id}>
              {step.status === 'running' ? (
                <LoaderCircle size={13} className="spin" aria-hidden="true" />
              ) : (
                <Terminal size={13} aria-hidden="true" />
              )}
              <span className="live-tool-name">{step.name}</span>
              {step.durationMs !== undefined && step.status !== 'running' && (
                <span className="live-tool-dur">{step.durationMs < 1000 ? `${step.durationMs}ms` : `${(step.durationMs / 1000).toFixed(1)}s`}</span>
              )}
              {step.detail && <small>{step.detail}</small>}
            </div>
          ))}
        </div>
      )}

      {streamingReasoning && (
        <details className="reasoning-block live" open>
          <summary>
            <ChevronRight size={13} className="disclosure-chevron" aria-hidden="true" />
            <Brain size={13} aria-hidden="true" />
            <span>{t('Thinking…')}</span>
          </summary>
          <div className="reasoning-body">
            <MarkdownContent text={streamingReasoning} />
          </div>
        </details>
      )}

      {streamingText && (
        <article className="message assistant streaming-message">
          <div className="message-content">
            <div className="markdown-body">
              <MarkdownContent text={streamingText} />
              <span className="caret" />
            </div>
          </div>
        </article>
      )}
    </>
  )
}

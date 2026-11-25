import { ArrowLeft, Bot, Brain, Check, ChevronRight, Cpu, LoaderCircle, Pause, Terminal, X } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import type { AgentSnapshot } from './agent-state'
import { MarkdownContent } from './markdown'
import { bannerError, btnPlain, cn, subtleCard } from './ui'
import { formatTokenCount } from './usage-metrics'

function formatDurationMs(ms?: number): string {
  if (!ms || ms <= 0) return '—'
  if (ms < 1000) return `${Math.round(ms)}ms`
  const sec = ms / 1000
  if (sec < 60) return `${sec.toFixed(1)}s`
  const min = Math.floor(sec / 60)
  const rem = Math.round(sec % 60)
  return `${min}m${rem}s`
}

function StatusGlyph({ status }: { status: AgentSnapshot['status'] }) {
  if (status === 'running') return <LoaderCircle size={14} className="spin shrink-0 text-ink" aria-hidden="true" />
  if (status === 'queued') return <LoaderCircle size={14} className="shrink-0 text-ink-muted" aria-hidden="true" />
  if (status === 'paused') return <Pause size={14} className="shrink-0 text-ink-muted" aria-hidden="true" />
  if (status === 'failed' || status === 'cancelled') return <X size={14} className="shrink-0 text-danger" aria-hidden="true" />
  if (status === 'completed') return <Check size={14} className="shrink-0 text-ok" aria-hidden="true" />
  return <Bot size={14} className="shrink-0 text-ink-muted" aria-hidden="true" />
}

function statusTranslationKey(status: AgentSnapshot['status']): string {
  switch (status) {
    case 'running': return 'Running'
    case 'queued': return 'Queued'
    case 'paused': return 'Paused'
    case 'completed': return 'Completed'
    case 'failed': return 'Failed'
    case 'cancelled': return 'Cancelled'
    default: return 'Idle'
  }
}

export function AgentInspector({
  agents,
  selectedAgentId,
  onSelectAgent,
}: {
  agents: AgentSnapshot[]
  selectedAgentId: string
  onSelectAgent: (id: string) => void
}) {
  const { t } = useTranslation()
  const selected = selectedAgentId ? agents.find((a) => a.id === selectedAgentId) ?? null : null
  const subagents = agents.filter((a) => !a.isLead)
  const runningSubagents = subagents.filter((a) => a.status === 'running').length

  if (selected) {
    const modelLabel = selected.modelProfile && selected.model && selected.model !== selected.modelProfile
      ? `${selected.modelProfile} (${selected.model})`
      : (selected.modelProfile || selected.model || '—')
    const kvRowCls = 'flex items-center justify-between gap-2 text-[12px] text-ink-secondary [&_code]:truncate [&_code]:font-mono [&_code]:text-[11.5px] [&_code]:text-ink'

    return (
      <div className="agent-inspector-detail flex min-h-0 flex-1 flex-col gap-3 overflow-y-auto p-4">
        <div className="flex items-center gap-2">
          <button
            type="button"
            className={cn(btnPlain, 'h-7 gap-1 px-2 text-[12px]')}
            onClick={() => onSelectAgent('')}
          >
            <ArrowLeft size={13} aria-hidden="true" />
            <span>{t('All agents')}</span>
          </button>
          <span className="ml-auto inline-flex items-center gap-1.5 rounded-full border border-line bg-canvas px-2 py-0.5 text-[11px] font-medium text-ink">
            <StatusGlyph status={selected.status} />
            <span>{t(statusTranslationKey(selected.status))}</span>
          </span>
        </div>

        <div className="flex flex-wrap items-baseline gap-2">
          <h3 className="m-0 text-[15px] font-semibold text-ink">{selected.isLead ? t('Main agent') : selected.name}</h3>
          {!selected.isLead && (
            <span className="rounded bg-muted px-1.5 py-[1px] font-mono text-[10.5px] text-ink-muted">{t(selected.role)}</span>
          )}
          {selected.durationMs !== undefined && selected.durationMs > 0 && (
            <span className="ml-auto font-mono text-[11px] text-ink-muted tabular-nums">{formatDurationMs(selected.durationMs)}</span>
          )}
        </div>

        {selected.error && (
          <div className={cn('agent-detail-error text-[12.5px]', bannerError)} role="alert">
            {selected.error}
          </div>
        )}

        <div className={cn('flex flex-col gap-2 p-3', subtleCard, 'bg-canvas')}>
          <div className={kvRowCls}><span>ID</span><code title={selected.id}>{selected.id}</code></div>
          <div className={kvRowCls}><span>{t('Model')}</span><code title={modelLabel}>{modelLabel}</code></div>
          {selected.contextMode && <div className={kvRowCls}><span>{t('Context mode')}</span><code>{selected.contextMode}</code></div>}
          <div className={kvRowCls}><span>{t('Activity')}</span><code>{t(selected.activity || statusTranslationKey(selected.status))}</code></div>
          {selected.task && (
            <div className="mt-1 border-t border-line-subtle pt-2">
              <div className="mb-1 text-[11.5px] font-medium text-ink-muted">{t('Task')}</div>
              <p className="m-0 whitespace-pre-wrap text-[12.5px] leading-[1.55] text-ink [overflow-wrap:anywhere]">{selected.task}</p>
            </div>
          )}
        </div>

        {(selected.totalTokens > 0 || selected.contextWindow > 0 || selected.usedContext > 0 || selected.tokensPerSecond) && (
          <div className={cn('flex flex-col gap-2 p-3', subtleCard, 'bg-canvas')}>
            <div className="flex items-center gap-1.5 text-[12px] font-semibold text-ink">
              <Cpu size={13} aria-hidden="true" />
              <span>{t('Token usage')}</span>
              {selected.tokensPerSecond && selected.tokensPerSecond > 0 && (
                <span className="ml-auto font-mono text-[11px] font-normal text-ink-muted tabular-nums">
                  {selected.tokensPerSecond.toFixed(1)} tok/s
                </span>
              )}
            </div>
            {selected.contextWindow > 0 && (
              <>
                <div className={kvRowCls}>
                  <span>{t('Context window')}</span>
                  <code>{formatTokenCount(selected.usedContext)} / {formatTokenCount(selected.contextWindow)} ({selected.contextPct}%)</code>
                </div>
                <div className="meter-track h-1.5 w-full overflow-hidden rounded-full bg-muted" aria-hidden="true">
                  <div className="meter-fill h-full rounded-full bg-ink transition-[width] duration-200" style={{ width: `${selected.contextPct}%` }} />
                </div>
              </>
            )}
            <div className={kvRowCls}><span>{t('Input tokens')}</span><code>{formatTokenCount(selected.inputTokens)}</code></div>
            <div className={kvRowCls}><span>{t('Output tokens')}</span><code>{formatTokenCount(selected.outputTokens)}</code></div>
            <div className={kvRowCls}><span>{t('Cached input')}</span><code>{formatTokenCount(selected.cachedTokens)} ({selected.cachePct}%)</code></div>
            {selected.reasoningTokens > 0 && (
              <div className={kvRowCls}><span>{t('Reasoning tokens')}</span><code>{formatTokenCount(selected.reasoningTokens)}</code></div>
            )}
            <div className={kvRowCls}><span>{t('Total tokens')}</span><code>{formatTokenCount(selected.totalTokens)}</code></div>
          </div>
        )}

        {selected.tools.length > 0 && (
          <div className={cn('flex flex-col gap-1.5 p-3', subtleCard, 'bg-canvas')}>
            <div className="flex items-center justify-between text-[12px] font-semibold text-ink">
              <span className="inline-flex items-center gap-1.5"><Terminal size={13} aria-hidden="true" />{t('Tool calls')}</span>
              <span className="font-mono text-[11px] font-normal text-ink-muted">{selected.toolCount}</span>
            </div>
            <div className="flex max-h-[180px] flex-col gap-1 overflow-y-auto">
              {selected.tools.map((step) => (
                <div
                  key={step.id}
                  className={cn(
                    'flex items-center gap-2 rounded-[7px] px-2 py-1 font-mono text-[11.5px]',
                    step.status === 'failed' ? 'bg-danger-bg text-danger' : 'bg-subtle text-ink-secondary',
                  )}
                >
                  {step.status === 'running' ? (
                    <LoaderCircle size={12} className="spin shrink-0" aria-hidden="true" />
                  ) : step.status === 'failed' ? (
                    <X size={12} className="shrink-0" aria-hidden="true" />
                  ) : (
                    <Check size={12} className="shrink-0 text-ok" aria-hidden="true" />
                  )}
                  <span className="truncate">{step.name}</span>
                  {step.durationMs !== undefined && step.status !== 'running' && (
                    <span className="ml-auto shrink-0 text-[10.5px] text-ink-muted tabular-nums">{formatDurationMs(step.durationMs)}</span>
                  )}
                </div>
              ))}
            </div>
          </div>
        )}

        {selected.reasoningText.trim() && (
          <details
            className="agent-detail-reasoning overflow-hidden rounded-[10px] border border-line-subtle bg-canvas"
            open={selected.status === 'running' && !selected.outputText.trim()}
          >
            <summary className="flex cursor-pointer select-none items-center gap-1.5 px-3 py-2 text-[12px] font-medium text-ink-secondary hover:text-ink">
              <ChevronRight size={13} className="disclosure-chevron" aria-hidden="true" />
              <Brain size={13} aria-hidden="true" />
              <span>{t('Thought process')}</span>
            </summary>
            <div className="max-h-[240px] overflow-y-auto border-t border-line-subtle px-3 py-2 text-[12.5px] leading-[1.6] text-ink-secondary">
              <MarkdownContent text={selected.reasoningText} />
            </div>
          </details>
        )}

        {selected.outputText.trim() && (
          <div className={cn('agent-detail-output flex flex-col gap-1.5 p-3', subtleCard, 'bg-canvas')}>
            <div className="text-[12px] font-semibold text-ink">{t('Output')}</div>
            <div className="markdown-body max-h-[380px] overflow-y-auto text-[13px] leading-[1.6] text-ink">
              <MarkdownContent text={selected.outputText} />
            </div>
          </div>
        )}
      </div>
    )
  }

  return (
    <div className="agent-inspector-list flex min-h-0 flex-1 flex-col gap-3 overflow-y-auto p-4">
      <div className="flex items-center justify-between gap-2">
        <h3 className="m-0 text-[14px] font-semibold text-ink">
          {t('Agents')}
        </h3>
        <span className="rounded-full border border-line bg-canvas px-2 py-0.5 font-mono text-[11px] text-ink-muted">
          {runningSubagents > 0 ? `${runningSubagents} ${t('Running')} · ${agents.length}` : agents.length}
        </span>
      </div>

      <div className="flex flex-col gap-2">
        {agents.map((agent) => {
          const displayName = agent.isLead ? t('Main agent') : agent.name
          const modelShort = agent.modelProfile || agent.model
          return (
            <button
              key={agent.id}
              type="button"
              className={cn(
                'agent-card flex w-full cursor-pointer flex-col gap-1.5 rounded-[12px] border border-line-subtle bg-canvas p-3 text-left transition-[border-color,background] duration-120 hover:border-line hover:bg-hover',
                agent.status === 'failed' || agent.status === 'cancelled' ? 'border-danger-border/60' : '',
              )}
              style={agent.depth > 1 ? { marginLeft: `${Math.min(2, agent.depth - 1) * 12}px`, width: `calc(100% - ${Math.min(2, agent.depth - 1) * 12}px)` } : undefined}
              data-agent-id={agent.id}
              onClick={() => onSelectAgent(agent.id)}
            >
              <div className="flex items-center gap-1.5">
                <StatusGlyph status={agent.status} />
                <span className="min-w-0 truncate text-[13px] font-semibold text-ink">{displayName}</span>
                {!agent.isLead && (
                  <span className="rounded bg-muted px-1.5 py-[1px] font-mono text-[10.5px] text-ink-muted">{t(agent.role)}</span>
                )}
                {modelShort && (
                  <span className="truncate font-mono text-[11px] text-ink-muted">{modelShort}</span>
                )}
                {agent.durationMs !== undefined && agent.durationMs > 0 && (
                  <span className="ml-auto shrink-0 font-mono text-[11px] text-ink-muted tabular-nums">{formatDurationMs(agent.durationMs)}</span>
                )}
                <ChevronRight size={13} className={cn('shrink-0 text-ink-muted', !(agent.durationMs && agent.durationMs > 0) && 'ml-auto')} aria-hidden="true" />
              </div>

              {(agent.preview || agent.activity) && (
                <div className="truncate text-[12px] text-ink-secondary">
                  {agent.status === 'running' ? (
                    <span className="font-mono text-[11.5px] text-ink">{t(agent.activity)}</span>
                  ) : null}
                  {agent.status === 'running' && agent.preview ? ' · ' : ''}
                  {agent.preview || (!agent.isLead ? t(agent.activity) : '')}
                </div>
              )}

              {(agent.totalTokens > 0 || agent.contextWindow > 0 || agent.toolCount > 0 || (agent.tokensPerSecond ?? 0) > 0) && (
                <div className="mt-0.5 flex flex-wrap items-center gap-x-2.5 gap-y-1 font-mono text-[11px] text-ink-muted tabular-nums">
                  {agent.contextWindow > 0 && (
                    <span>ctx {formatTokenCount(agent.usedContext)}/{formatTokenCount(agent.contextWindow)} ({agent.contextPct}%)</span>
                  )}
                  {agent.inputTokens > 0 && <span>{t('Cache')} {agent.cachePct}%</span>}
                  {agent.totalTokens > 0 && <span>{t('Total')} {formatTokenCount(agent.totalTokens)}</span>}
                  {agent.toolCount > 0 && <span>{agent.toolCount} {t('tools')}</span>}
                  {agent.tokensPerSecond && agent.tokensPerSecond > 0 && <span>{agent.tokensPerSecond.toFixed(1)} tok/s</span>}
                </div>
              )}
            </button>
          )
        })}
      </div>

      {subagents.length === 0 && (
        <p className="m-0 px-1 text-[12px] text-ink-muted">
          {t('No subagents have run in this session yet.')}
        </p>
      )}
    </div>
  )
}

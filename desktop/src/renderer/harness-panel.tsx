import { useCallback, useEffect, useRef, useState, type FormEvent } from 'react'
import {
  Activity, Check, ChevronRight, Copy, Cpu, FolderOpen, Maximize2, Minimize2, Pause, Play,
  Plug, RefreshCw, Settings, Square, Target, Terminal, Trash2, X,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import type { RuntimeClient } from './runtime'
import { WorkspaceFiles, type WorkspaceInfo } from './workspace-files'
import {
  bannerError, btnIcon, btnPlain, btnPlainDanger, btnPrimaryCompact, btnSecondary,
  btnSubtleIcon, cn, fieldInput, fieldSelect, fieldTextarea, statusPill, subtleCard,
} from './ui'
import {
  contentText, objectValue,
  type CommandInfo, type CommandOutput, type Goal, type HarnessTab, type MCPPrompt,
  type MCPPromptResult, type MCPResource, type MCPResourceTemplate, type MCPServerStatus,
  type ModelOption, type RuntimeState, type SessionSummary,
} from './harness-types'

function extractResponseInputText(input: unknown): string {
  if (input === null || input === undefined) return ''
  if (typeof input === 'string') return input
  const obj = objectValue(input)
  if (typeof obj.text === 'string') return obj.text
  const items = Array.isArray(input) ? input : Array.isArray(obj.items) ? obj.items : null
  if (items) {
    const parts = items.map((entry) => {
      if (typeof entry === 'string') return entry
      const item = objectValue(entry)
      if (item.content !== undefined) return contentText(item.content)
      if (typeof item.text === 'string') return item.text
      return contentText([entry])
    }).filter(Boolean)
    if (parts.length > 0) return parts.join('\n\n')
  }
  const direct = contentText(input)
  if (direct) return direct
  try {
    return JSON.stringify(input, null, 2)
  } catch {
    return String(input)
  }
}

function parseTemplateParams(uriTemplate: string): string[] {
  const names: string[] = []
  const seen = new Set<string>()
  const re = /\{[+]?([a-zA-Z0-9_.-]+)\}/g
  let m: RegExpExecArray | null
  while ((m = re.exec(uriTemplate)) !== null) {
    const key = m[1]
    if (!seen.has(key)) {
      seen.add(key)
      names.push(key)
    }
  }
  return names
}

function resolveUriTemplate(uriTemplate: string, values: Record<string, string>): string {
  return uriTemplate.replace(/\{([+]?)([a-zA-Z0-9_.-]+)\}/g, (_, plus: string, key: string) => {
    const raw = values[key] ?? ''
    if (!raw) return `{${plus}${key}}`
    return plus ? raw : encodeURIComponent(raw)
  })
}

export function HarnessPanel({
  client,
  workspace,
  visible,
  fullScreen,
  onToggleFullScreen,
  tab,
  onTabChange,
  onClose,
  capabilities,
  activeSession,
  runtime,
  models,
  initialFile,
  initialCommandId,
  onInsertPath,
  onInsertText,
  onOpenMCPSettings,
  onRefreshState,
  onChangeReasoningEffort,
  permissionMode,
  allowedPermissionModes,
  onChangePermissionMode,
}: {
  client: RuntimeClient
  workspace: WorkspaceInfo | null
  visible: boolean
  fullScreen: boolean
  onToggleFullScreen: () => void
  tab: HarnessTab
  onTabChange: (tab: HarnessTab) => void
  onClose: () => void
  capabilities: string[]
  activeSession: SessionSummary | null
  runtime: RuntimeState
  models: ModelOption[]
  initialFile?: string
  initialCommandId?: string
  onInsertPath: (path: string) => void
  onInsertText?: (text: string) => void
  onOpenMCPSettings?: () => void
  onRefreshState: () => Promise<void>
  onChangeReasoningEffort?: (effort: string) => void
  permissionMode?: string
  allowedPermissionModes?: string[]
  onChangePermissionMode?: (mode: string) => void
}) {
  const { t } = useTranslation()
  const has = (cap: string) => capabilities.includes(cap)
  const tabs: Array<{ id: HarnessTab; label: string; icon: typeof FolderOpen; available: boolean }> = [
    { id: 'files', label: 'Files', icon: FolderOpen, available: has('workspace.files') },
    { id: 'goal', label: 'Goal', icon: Target, available: has('goal.manage') && Boolean(activeSession) },
    { id: 'processes', label: 'Processes', icon: Terminal, available: has('command.control') && Boolean(activeSession) },
    { id: 'mcp', label: 'MCP', icon: Plug, available: has('mcp.inspect') },
    { id: 'context', label: 'Context', icon: Activity, available: true },
  ]
  const visibleTabs = tabs.filter((item) => item.available)
  const effectiveTab = visibleTabs.some((item) => item.id === tab) ? tab : (visibleTabs[0]?.id ?? 'context')
  const tabsRef = useRef<HTMLDivElement | null>(null)
  const [tabFade, setTabFade] = useState({ left: false, right: false })

  useEffect(() => {
    if (!visible) return
    const el = tabsRef.current
    if (!el) return

    const syncFade = () => {
      const left = el.scrollLeft > 2
      const right = el.scrollLeft + el.clientWidth < el.scrollWidth - 2
      setTabFade((prev) => (prev.left === left && prev.right === right ? prev : { left, right }))
    }

    let targetLeft: number | null = null
    let rafId: number | null = null
    let lastTs = 0

    const cancelAnim = () => {
      if (rafId !== null) {
        window.cancelAnimationFrame(rafId)
        rafId = null
      }
      lastTs = 0
      targetLeft = null
    }

    const step = (ts: number) => {
      rafId = null
      if (targetLeft === null) {
        lastTs = 0
        return
      }
      const gap = targetLeft - el.scrollLeft
      if (Math.abs(gap) <= 0.5) {
        el.scrollLeft = targetLeft
        targetLeft = null
        lastTs = 0
        syncFade()
        return
      }
      const dt = lastTs ? Math.min(0.05, Math.max(0.001, (ts - lastTs) / 1000)) : 1 / 120
      lastTs = ts
      const alpha = 1 - Math.exp(-20 * dt)
      const delta = Math.abs(gap) * alpha
      el.scrollLeft += Math.sign(gap) * Math.max(0.5, delta)
      syncFade()
      rafId = window.requestAnimationFrame(step)
    }

    const onWheel = (e: WheelEvent) => {
      if (el.scrollWidth <= el.clientWidth + 1) return
      if (Math.abs(e.deltaY) <= Math.abs(e.deltaX)) return
      e.preventDefault()
      const scale = e.deltaMode === 1 ? 16 : e.deltaMode === 2 ? el.clientWidth : 1
      const delta = e.deltaY * scale
      const maxScroll = Math.max(0, el.scrollWidth - el.clientWidth)
      const base = targetLeft ?? el.scrollLeft
      const nextTarget = Math.max(0, Math.min(maxScroll, base + delta))
      if (window.matchMedia?.('(prefers-reduced-motion: reduce)').matches) {
        cancelAnim()
        el.scrollLeft = nextTarget
        syncFade()
        return
      }
      targetLeft = nextTarget
      if (rafId === null) {
        lastTs = 0
        rafId = window.requestAnimationFrame(step)
      }
    }

    const onScroll = () => {
      if (rafId === null) targetLeft = null
      syncFade()
    }

    el.addEventListener('wheel', onWheel, { passive: false })
    el.addEventListener('scroll', onScroll, { passive: true })

    let observer: ResizeObserver | null = null
    if (typeof ResizeObserver !== 'undefined') {
      observer = new ResizeObserver(syncFade)
      observer.observe(el)
    }
    syncFade()

    return () => {
      el.removeEventListener('wheel', onWheel)
      el.removeEventListener('scroll', onScroll)
      observer?.disconnect()
      cancelAnim()
    }
  }, [visible, visibleTabs.length])

  useEffect(() => {
    if (!visible) return
    const activeBtn = tabsRef.current?.querySelector<HTMLButtonElement>('button.active')
    activeBtn?.scrollIntoView({ inline: 'nearest', block: 'nearest', behavior: 'smooth' })
  }, [visible, effectiveTab])

  if (!visible) return null

  return (
    <aside
      className={cn(
        'harness-panel z-[5] flex h-full min-h-0 min-w-0 flex-col border-l border-line-subtle bg-sidebar',
        fullScreen && 'fullscreen fixed inset-0 z-[60] h-dvh w-screen border-0',
      )}
      aria-label={t('Harness')}
      onKeyDown={(event) => { if (event.key === 'Escape') onClose() }}
    >
      <header className="harness-panel-header flex h-[52px] items-center justify-between gap-1.5 border-b border-line-subtle bg-canvas px-2.5">
        <div
          ref={tabsRef}
          className={cn(
            'harness-tabs flex min-w-0 flex-1 items-center gap-0.5 overflow-x-auto',
            tabFade.left && 'can-scroll-left',
            tabFade.right && 'can-scroll-right',
          )}
          role="tablist"
        >
          {visibleTabs.map((item) => {
            const Icon = item.icon
            const active = effectiveTab === item.id
            return (
              <button
                key={item.id}
                type="button"
                role="tab"
                aria-selected={active}
                className={cn(
                  'inline-flex h-8 flex-none cursor-pointer items-center gap-1.5 whitespace-nowrap rounded-[8px] border-0 px-2.5 text-[12.5px] font-medium transition-[background,color] duration-120',
                  active ? 'active bg-active text-ink' : 'bg-transparent text-ink-secondary hover:bg-hover hover:text-ink',
                )}
                onClick={() => onTabChange(item.id)}
              >
                <Icon size={14} aria-hidden="true" />
                <span>{t(item.label)}</span>
              </button>
            )
          })}
        </div>
        <button type="button" className={cn('icon-button', btnIcon)} aria-label={t(fullScreen ? 'Exit full screen' : 'Expand panel')} title={t(fullScreen ? 'Exit full screen' : 'Expand panel')} onClick={onToggleFullScreen}>
          {fullScreen ? <Minimize2 size={15} /> : <Maximize2 size={15} />}
        </button>
        <button type="button" className={cn('icon-button', btnIcon)} aria-label={t('Close')} title={t('Close')} onClick={onClose}>
          <X size={16} />
        </button>
      </header>

      <div className="harness-panel-body flex min-h-0 flex-1 flex-col overflow-hidden">
        {effectiveTab === 'files' && has('workspace.files') && (
          <WorkspaceFiles
            client={client}
            workspace={workspace}
            visible={visible}
            close={onClose}
            initialFile={initialFile}
            onInsertPath={onInsertPath}
            embedded
          />
        )}
        {effectiveTab === 'goal' && activeSession && (
          <GoalInspector
            client={client}
            sessionId={activeSession.id}
            goal={runtime.goal ?? null}
            sessionBusy={runtime.task.status === 'running' || runtime.task.status === 'queued' || runtime.task.status === 'paused' || runtime.queue.length > 0}
            onUpdated={onRefreshState}
          />
        )}
        {effectiveTab === 'processes' && activeSession && (
          <ProcessInspector
            client={client}
            sessionId={activeSession.id}
            initialCommandId={initialCommandId}
            visible={visible && effectiveTab === 'processes'}
          />
        )}
        {effectiveTab === 'mcp' && (
          <MCPInspector
            client={client}
            sessionId={activeSession?.id ?? ''}
            onInsertText={onInsertText}
            onOpenMCPSettings={onOpenMCPSettings}
          />
        )}
        {effectiveTab === 'context' && (
          <ContextInspector
            activeSession={activeSession}
            runtime={runtime}
            models={models}
            capabilities={capabilities}
            onChangeReasoningEffort={onChangeReasoningEffort}
            permissionMode={permissionMode}
            allowedPermissionModes={allowedPermissionModes}
            onChangePermissionMode={onChangePermissionMode}
          />
        )}
      </div>
    </aside>
  )
}

function GoalInspector({
  client,
  sessionId,
  goal: initialGoal,
  sessionBusy,
  onUpdated,
}: {
  client: RuntimeClient
  sessionId: string
  goal: Goal | null
  sessionBusy: boolean
  onUpdated: () => Promise<void>
}) {
  const { t } = useTranslation()
  const [goal, setGoal] = useState<Goal | null>(initialGoal)
  const [objective, setObjective] = useState('')
  const [budget, setBudget] = useState('')
  const [editing, setEditing] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => { setGoal(initialGoal) }, [initialGoal])

  const load = useCallback(async () => {
    setBusy(true)
    setError('')
    try {
      const res = await client.request<{ goal: Goal | null }>('goal.get', { sessionId })
      setGoal(res.goal)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }, [client, sessionId])

  useEffect(() => { void load() }, [load])

  const run = async (work: () => Promise<void>) => {
    setBusy(true)
    setError('')
    try {
      await work()
      await load()
      await onUpdated()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  const createGoal = (event: FormEvent) => {
    event.preventDefault()
    const trimmed = objective.trim()
    if (!trimmed) return
    const parsedBudget = budget.trim() ? Number(budget.trim()) : undefined
    if (parsedBudget !== undefined && (!Number.isSafeInteger(parsedBudget) || parsedBudget <= 0)) {
      setError(t('Token budget must be a positive integer.'))
      return
    }
    void run(async () => {
      await client.request('goal.create', {
        sessionId,
        objective: trimmed,
        ...(parsedBudget !== undefined ? { tokenBudget: parsedBudget } : {}),
      })
      setObjective('')
      setBudget('')
    })
  }

  const saveEdit = (event: FormEvent) => {
    event.preventDefault()
    const trimmed = objective.trim()
    const parsedBudget = budget.trim() ? Number(budget.trim()) : null
    if (parsedBudget !== null && (!Number.isSafeInteger(parsedBudget) || parsedBudget <= 0)) {
      setError(t('Token budget must be a positive integer.'))
      return
    }
    void run(async () => {
      if (trimmed && trimmed !== goal?.objective) {
        await client.request('goal.edit', { sessionId, objective: trimmed })
      }
      await client.request('goal.setBudget', { sessionId, tokenBudget: parsedBudget })
      setEditing(false)
    })
  }

  const fieldCls = 'flex flex-col gap-1.5 text-[12.5px] font-medium text-ink-secondary'

  return (
    <div className="inspector-pane flex min-h-0 flex-1 flex-col gap-3.5 overflow-y-auto p-4">
      <div className="inspector-head flex items-center justify-between">
        <h3 className="m-0 text-[14px] font-semibold text-ink">{t('Persistent goal')}</h3>
        <button type="button" className={btnSubtleIcon} aria-label={t('Refresh')} disabled={busy} onClick={() => void load()}>
          <RefreshCw size={13} className={busy ? 'spin' : undefined} />
        </button>
      </div>
      {error && <div className={cn('form-error', bannerError)} role="alert">{error}</div>}

      {!goal || goal.status === 'complete' ? (
        <form className="inspector-form flex flex-col gap-3" onSubmit={createGoal}>
          <p className="inspector-hint m-0 text-[13px] leading-[1.55] text-ink-muted">{t('Create a persistent objective that guides autonomous execution across turns.')}</p>
          <label className={fieldCls}>
            {t('Objective')}
            <textarea
              rows={4}
              className={cn(fieldTextarea, 'resize-y')}
              value={objective}
              disabled={busy || sessionBusy}
              onChange={(event) => setObjective(event.target.value)}
              placeholder={t('Describe the goal to pursue…')}
              required
            />
          </label>
          <label className={fieldCls}>
            {t('Token budget (optional)')}
            <input
              type="number"
              min={1}
              className={fieldInput}
              value={budget}
              disabled={busy || sessionBusy}
              onChange={(event) => setBudget(event.target.value)}
              placeholder="e.g. 120000"
            />
          </label>
          <button className={btnPrimaryCompact} disabled={busy || sessionBusy || !objective.trim()}>
            <Target size={14} aria-hidden="true" />
            <span>{t('Start goal')}</span>
          </button>
        </form>
      ) : (
        <div className={cn('goal-card flex flex-col gap-3 rounded-[11px] border border-line bg-canvas p-3.5')}>
          <div className="goal-status-row flex items-center justify-between">
            <span className={cn(statusPill, goal.status === 'active' && 'border-accent bg-accent text-accent-fg')}>{t(goal.status)}</span>
            <span className="goal-rev font-mono text-[11.5px] text-ink-muted">rev {goal.revision}</span>
          </div>
          {!editing ? (
            <>
              <p className="goal-objective m-0 text-[13.5px] leading-[1.55] text-ink">{goal.objective}</p>
              <div className="goal-metrics grid grid-cols-2 gap-2">
                <div className="rounded-[8px] bg-subtle px-2.5 py-2">
                  <span className="block text-[11px] text-ink-muted">{t('Tokens used')}</span>
                  <strong className="mt-0.5 block font-mono text-[12.5px] font-semibold text-ink">{goal.tokens_used.toLocaleString()}{goal.token_budget ? ` / ${goal.token_budget.toLocaleString()}` : ''}</strong>
                </div>
                <div className="rounded-[8px] bg-subtle px-2.5 py-2">
                  <span className="block text-[11px] text-ink-muted">{t('Cached')}</span>
                  <strong className="mt-0.5 block font-mono text-[12.5px] font-semibold text-ink">{goal.cached_tokens.toLocaleString()}</strong>
                </div>
              </div>
              {goal.token_budget && goal.token_budget > 0 && (
                <div className="meter-track h-1.5 w-full overflow-hidden rounded-full bg-muted" aria-hidden="true">
                  <div className="meter-fill h-full rounded-full bg-ink transition-[width] duration-200" style={{ width: `${Math.min(100, Math.round((goal.tokens_used / goal.token_budget) * 100))}%` }} />
                </div>
              )}
              {goal.stop_reason && <p className="inspector-hint m-0 text-[13px] leading-[1.55] text-ink-muted">{goal.stop_reason}</p>}
              <div className="goal-actions flex flex-wrap items-center gap-2 pt-1">
                {goal.status === 'active' ? (
                  <button type="button" className={btnSecondary} disabled={busy} onClick={() => void run(async () => { await client.request('goal.pause', { sessionId }) })}>
                    <Pause size={13} aria-hidden="true" />
                    <span>{t('Pause')}</span>
                  </button>
                ) : (
                  <button type="button" className={btnPrimaryCompact} disabled={busy || sessionBusy} onClick={() => void run(async () => { await client.request('goal.resume', { sessionId }) })}>
                    <Play size={13} aria-hidden="true" />
                    <span>{t('Resume')}</span>
                  </button>
                )}
                <button
                  type="button"
                  className={btnSecondary}
                  disabled={busy || sessionBusy}
                  onClick={() => {
                    setObjective(goal.objective)
                    setBudget(goal.token_budget ? String(goal.token_budget) : '')
                    setEditing(true)
                  }}
                >
                  <span>{t('Edit')}</span>
                </button>
                <button
                  type="button"
                  className={btnPlainDanger}
                  disabled={busy || sessionBusy}
                  onClick={() => void run(async () => { await client.request('goal.clear', { sessionId }) })}
                >
                  <Trash2 size={13} aria-hidden="true" />
                  <span>{t('Clear')}</span>
                </button>
              </div>
            </>
          ) : (
            <form className="inspector-form flex flex-col gap-3" onSubmit={saveEdit}>
              <label className={fieldCls}>
                {t('Objective')}
                <textarea rows={3} className={cn(fieldTextarea, 'resize-y')} value={objective} disabled={busy} onChange={(event) => setObjective(event.target.value)} required />
              </label>
              <label className={fieldCls}>
                {t('Token budget (optional)')}
                <input type="number" min={1} className={fieldInput} value={budget} disabled={busy} onChange={(event) => setBudget(event.target.value)} />
              </label>
              <div className="goal-actions flex items-center justify-end gap-2">
                <button type="button" className={btnPlain} disabled={busy} onClick={() => setEditing(false)}>{t('Cancel')}</button>
                <button className={btnPrimaryCompact} disabled={busy}>{t('Save')}</button>
              </div>
            </form>
          )}
        </div>
      )}
    </div>
  )
}

function ProcessInspector({
  client,
  sessionId,
  initialCommandId,
  visible,
}: {
  client: RuntimeClient
  sessionId: string
  initialCommandId?: string
  visible: boolean
}) {
  const { t } = useTranslation()
  const [commands, setCommands] = useState<CommandInfo[]>([])
  const [selectedId, setSelectedId] = useState(initialCommandId ?? '')
  const [output, setOutput] = useState<CommandOutput | null>(null)
  const [followLog, setFollowLog] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const logRef = useRef<HTMLPreElement | null>(null)
  const pollingRef = useRef(false)

  useEffect(() => {
    if (initialCommandId) setSelectedId(initialCommandId)
  }, [initialCommandId])

  const loadList = useCallback(async () => {
    setBusy(true)
    setError('')
    try {
      const list = await client.request<CommandInfo[]>('command.list', { sessionId })
      setCommands(list ?? [])
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }, [client, sessionId])

  const loadOutput = useCallback(async (commandId: string) => {
    if (!commandId) return
    try {
      const res = await client.request<CommandOutput>('command.read', { sessionId, commandId, cursor: 0, waitMs: 0 })
      setOutput(res)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }, [client, sessionId])

  useEffect(() => { void loadList() }, [loadList])
  useEffect(() => {
    if (selectedId) {
      setFollowLog(true)
      void loadOutput(selectedId)
    } else {
      setOutput(null)
    }
  }, [selectedId, loadOutput])

  const selectedCommand = commands.find((cmd) => cmd.id === selectedId)
  const selectedStatus = selectedCommand?.status ?? output?.command.status ?? ''
  const selectedActive = selectedStatus === 'running' || selectedStatus === 'stopping'

  useEffect(() => {
    if (!visible || !selectedId || !selectedActive) return
    let cancelled = false
    const timer = window.setInterval(() => {
      if (pollingRef.current || cancelled) return
      pollingRef.current = true
      void Promise.all([
        client.request<CommandOutput>('command.read', { sessionId, commandId: selectedId, cursor: 0, waitMs: 0 }),
        client.request<CommandInfo[]>('command.list', { sessionId }),
      ])
        .then(([nextOut, nextList]) => {
          if (cancelled) return
          if (nextOut) setOutput(nextOut)
          if (nextList) setCommands(nextList)
        })
        .catch(() => undefined)
        .finally(() => {
          pollingRef.current = false
        })
    }, 1500)
    return () => {
      cancelled = true
      window.clearInterval(timer)
    }
  }, [visible, selectedId, selectedActive, client, sessionId])

  useEffect(() => {
    if (!followLog || !logRef.current) return
    logRef.current.scrollTop = logRef.current.scrollHeight
  }, [output?.output, followLog])

  const handleLogScroll = () => {
    const el = logRef.current
    if (!el) return
    const distance = el.scrollHeight - el.scrollTop - el.clientHeight
    if (distance > 24 && followLog) setFollowLog(false)
    else if (distance <= 16 && !followLog) setFollowLog(true)
  }

  const stopCommand = async (commandId: string) => {
    setBusy(true)
    setError('')
    try {
      const res = await client.request<CommandOutput>('command.stop', { sessionId, commandId })
      if (selectedId === commandId) setOutput(res)
      await loadList()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="inspector-pane flex min-h-0 flex-1 flex-col gap-3.5 overflow-y-auto p-4">
      <div className="inspector-head flex items-center justify-between">
        <h3 className="m-0 text-[14px] font-semibold text-ink">{t('Commands & processes')}</h3>
        <button type="button" className={btnSubtleIcon} aria-label={t('Refresh')} disabled={busy} onClick={() => { void loadList(); if (selectedId) void loadOutput(selectedId) }}>
          <RefreshCw size={13} className={busy ? 'spin' : undefined} />
        </button>
      </div>
      {error && <div className={cn('form-error', bannerError)} role="alert">{error}</div>}
      {commands.length === 0 ? (
        <p className="inspector-hint m-0 text-[13px] leading-[1.55] text-ink-muted">{t('No commands recorded in this session.')}</p>
      ) : (
        <div className="process-list flex flex-col gap-1.5">
          {commands.map((cmd) => {
            const active = cmd.status === 'running' || cmd.status === 'stopping'
            const isSelected = selectedId === cmd.id
            return (
              <div
                className={cn(
                  'process-row flex items-center gap-1 rounded-[9px] border p-1 transition-[border-color,background] duration-120',
                  isSelected ? 'selected border-line-strong bg-canvas' : 'border-line-subtle bg-canvas hover:border-line',
                )}
                key={cmd.id}
              >
                <button
                  type="button"
                  className="process-select flex min-w-0 flex-1 cursor-pointer items-center gap-2 rounded-[7px] border-0 bg-transparent px-2 py-1.5 text-left"
                  onClick={() => setSelectedId(cmd.id)}
                >
                  <span
                    className={cn(
                      'process-dot h-2 w-2 shrink-0 rounded-full',
                      active ? 'bg-ok animate-[pulse_1.4s_infinite]' : cmd.status === 'failed' ? 'bg-danger' : 'bg-ink-faint',
                    )}
                  />
                  <span className="process-cmd min-w-0 flex-1 truncate font-mono text-[12px] text-ink" title={cmd.command_text}>{cmd.command_text}</span>
                  <span className="process-meta shrink-0 font-mono text-[11px] text-ink-muted">
                    {cmd.status}
                    {cmd.exit_code !== undefined ? ` (${cmd.exit_code})` : ''}
                  </span>
                </button>
                {active && (
                  <button
                    type="button"
                    className={cn(btnSubtleIcon, 'hover:not-disabled:bg-danger-bg hover:not-disabled:text-danger')}
                    aria-label={t('Stop')}
                    title={t('Stop')}
                    disabled={busy}
                    onClick={() => void stopCommand(cmd.id)}
                  >
                    <Square size={12} fill="currentColor" />
                  </button>
                )}
              </div>
            )
          })}
        </div>
      )}
      {output && (
        <div className="process-log-wrap flex min-h-0 flex-1 flex-col overflow-hidden rounded-[10px] border border-line-subtle bg-canvas">
          <div className="process-log-head flex items-center justify-between gap-2 border-b border-line-subtle bg-subtle px-3 py-1.5 text-[12px] text-ink-secondary">
            <span className="process-log-title inline-flex items-center gap-1.5 font-mono text-[11.5px]">
              {selectedActive && <span className="process-live-dot h-1.5 w-1.5 rounded-full bg-ok animate-[pulse_1.3s_infinite]" aria-hidden="true" />}
              {output.command.id.slice(0, 8)} · {output.command.status}
            </span>
            <div className="process-log-actions flex items-center gap-1.5">
              <button
                type="button"
                className={cn(
                  'process-follow-btn cursor-pointer rounded-[6px] border px-2 py-0.5 text-[11.5px] font-medium transition-[background,color,border-color] duration-120',
                  followLog ? 'active border-accent bg-accent text-accent-fg' : 'border-line bg-canvas text-ink-secondary hover:text-ink',
                )}
                aria-pressed={followLog}
                onClick={() => setFollowLog((v) => !v)}
              >
                {t('Follow')}
              </button>
              <button type="button" className={btnPlain} onClick={() => void loadOutput(output.command.id)}>
                <RefreshCw size={12} />
                <span>{t('Refresh')}</span>
              </button>
            </div>
          </div>
          <pre
            className="process-log m-0 max-h-[340px] flex-1 overflow-auto bg-code p-3 font-mono text-[12px] leading-[1.6] text-ink-secondary whitespace-pre-wrap [overflow-wrap:anywhere]"
            ref={logRef}
            onScroll={handleLogScroll}
          >
            {output.output || t('(no output yet)')}
          </pre>
        </div>
      )}
    </div>
  )
}

function MCPInspector({
  client,
  sessionId,
  onInsertText,
  onOpenMCPSettings,
}: {
  client: RuntimeClient
  sessionId: string
  onInsertText?: (text: string) => void
  onOpenMCPSettings?: () => void
}) {
  const { t } = useTranslation()
  const [servers, setServers] = useState<MCPServerStatus[]>([])
  const [expanded, setExpanded] = useState<Record<string, {
    prompts: MCPPrompt[]
    resources: MCPResource[]
    templates: MCPResourceTemplate[]
  }>>({})
  const [activePromptKey, setActivePromptKey] = useState('')
  const [promptArgs, setPromptArgs] = useState<Record<string, string>>({})
  const [activeTemplateKey, setActiveTemplateKey] = useState('')
  const [templateVars, setTemplateVars] = useState<Record<string, string>>({})
  const [previewState, setPreviewState] = useState<{
    key: string
    title: string
    text: string
  } | null>(null)
  const [copiedKey, setCopiedKey] = useState('')
  const [loadingKey, setLoadingKey] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  const load = useCallback(async () => {
    setBusy(true)
    setError('')
    try {
      const list = await client.request<MCPServerStatus[]>('mcp.servers')
      setServers(list ?? [])
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }, [client])

  useEffect(() => { void load() }, [load])

  const toggleServer = async (server: MCPServerStatus) => {
    setBusy(true)
    setError('')
    try {
      await client.request(server.enabled ? 'mcp.disable' : 'mcp.enable', { serverName: server.name })
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  const inspectServer = async (server: MCPServerStatus) => {
    if (expanded[server.name]) {
      setExpanded((prev) => {
        const next = { ...prev }
        delete next[server.name]
        return next
      })
      return
    }
    if (!server.connected || !sessionId) return
    setLoadingKey(`server:${server.name}`)
    setError('')
    try {
      const [prompts, resources, templates] = await Promise.all([
        client.request<MCPPrompt[]>('mcp.listPrompts', { sessionId, serverName: server.name }).catch(() => []),
        client.request<MCPResource[]>('mcp.listResources', { sessionId, serverName: server.name }).catch(() => []),
        client.request<MCPResourceTemplate[]>('mcp.listResourceTemplates', { sessionId, serverName: server.name }).catch(() => []),
      ])
      setExpanded((prev) => ({
        ...prev,
        [server.name]: { prompts: prompts ?? [], resources: resources ?? [], templates: templates ?? [] },
      }))
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setLoadingKey('')
    }
  }

  const fetchPromptPreview = async (serverName: string, prompt: MCPPrompt) => {
    if (!sessionId) return
    const key = `${serverName}:prompt:${prompt.name}`
    setLoadingKey(key)
    setError('')
    try {
      const args: Record<string, string> = {}
      for (const arg of prompt.arguments ?? []) {
        const val = (promptArgs[`${key}:${arg.name}`] ?? '').trim()
        if (val) args[arg.name] = val
      }
      const res = await client.request<MCPPromptResult>('mcp.getPrompt', {
        sessionId,
        serverName,
        promptName: prompt.name,
        ...(Object.keys(args).length ? { arguments: args } : {}),
      })
      const text = extractResponseInputText(res?.input) || res?.description || ''
      setPreviewState({ key, title: prompt.title || prompt.name, text })
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setLoadingKey('')
    }
  }

  const readResourcePreview = async (serverName: string, key: string, title: string, uri: string) => {
    if (!sessionId || !uri.trim()) return
    setLoadingKey(key)
    setError('')
    try {
      const res = await client.request<unknown>('mcp.readResource', {
        sessionId,
        serverName,
        uri: uri.trim(),
      })
      setPreviewState({ key, title, text: extractResponseInputText(res) })
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setLoadingKey('')
    }
  }

  const copyPreview = async (key: string, text: string) => {
    try {
      await navigator.clipboard?.writeText(text)
      setCopiedKey(key)
      window.setTimeout(() => setCopiedKey((cur) => (cur === key ? '' : cur)), 1500)
    } catch {
      /* ignore clipboard unavailability */
    }
  }

  const renderPreview = (key: string) => {
    if (!previewState || previewState.key !== key) return null
    return (
      <div className="mcp-detail-drawer mt-2 overflow-hidden rounded-[9px] border border-line-subtle bg-canvas">
        <div className="mcp-preview-head flex items-center justify-between gap-2 border-b border-line-subtle bg-subtle px-2.5 py-1.5 text-[12px] font-medium text-ink">
          <span className="truncate">{previewState.title}</span>
          <div className="mcp-preview-actions flex shrink-0 items-center gap-1.5">
            <button type="button" className={btnPlain} onClick={() => void copyPreview(key, previewState.text)}>
              {copiedKey === key ? <Check size={12} /> : <Copy size={12} />}
              <span>{t(copiedKey === key ? 'Copied' : 'Copy')}</span>
            </button>
            {onInsertText && previewState.text.trim() && (
              <button
                type="button"
                className={btnPrimaryCompact}
                onClick={() => onInsertText(previewState.text)}
              >
                {t('Insert into composer')}
              </button>
            )}
          </div>
        </div>
        <pre className="mcp-preview-pre m-0 max-h-[240px] overflow-auto bg-code p-2.5 font-mono text-[11.5px] leading-[1.55] text-ink-secondary whitespace-pre-wrap [overflow-wrap:anywhere]">
          {previewState.text || t('(empty)')}
        </pre>
      </div>
    )
  }

  return (
    <div className="inspector-pane flex min-h-0 flex-1 flex-col gap-3.5 overflow-y-auto p-4">
      <div className="inspector-head flex items-center justify-between">
        <h3 className="m-0 text-[14px] font-semibold text-ink">{t('MCP servers')}</h3>
        <div className="inspector-head-actions flex items-center gap-1">
          {onOpenMCPSettings && (
            <button type="button" className={btnPlain} onClick={onOpenMCPSettings}>
              <Settings size={12} aria-hidden="true" />
              <span>{t('Configure')}</span>
            </button>
          )}
          <button type="button" className={btnSubtleIcon} aria-label={t('Refresh')} disabled={busy} onClick={() => void load()}>
            <RefreshCw size={13} className={busy ? 'spin' : undefined} />
          </button>
        </div>
      </div>
      {error && <div className={cn('form-error', bannerError)} role="alert">{error}</div>}
      {servers.length === 0 ? (
        <p className="inspector-hint m-0 text-[13px] leading-[1.55] text-ink-muted">{t('No MCP servers configured in Core.')}</p>
      ) : (
        <div className="mcp-list flex flex-col gap-2.5">
          {servers.map((server) => {
            const details = expanded[server.name]
            const loadingThis = loadingKey === `server:${server.name}`
            return (
              <div className="mcp-card flex flex-col gap-2 rounded-[11px] border border-line-subtle bg-canvas p-3" key={server.name}>
                <div className="mcp-card-head flex items-center justify-between gap-2">
                  <button
                    type="button"
                    className="mcp-title-btn flex min-w-0 flex-1 cursor-pointer items-center gap-1.5 border-0 bg-transparent p-0 text-left text-[13px] text-ink"
                    onClick={() => void inspectServer(server)}
                  >
                    {server.connected && <ChevronRight size={13} className={cn('shrink-0 text-ink-muted transition-transform duration-150', details && 'rotate-90')} />}
                    <strong className="truncate font-semibold">{server.name}</strong>
                    <span className="mcp-meta shrink-0 font-mono text-[11px] text-ink-muted">{server.transport} · {server.toolCount} {t('tools')}</span>
                  </button>
                  <button
                    type="button"
                    className={server.enabled ? btnSecondary : btnPrimaryCompact}
                    disabled={busy}
                    onClick={() => void toggleServer(server)}
                  >
                    {t(server.enabled ? 'Disable' : 'Enable')}
                  </button>
                </div>
                {(server.endpoint || server.command) && (
                  <div className="mcp-endpoint truncate font-mono text-[11.5px] text-ink-muted">{server.endpoint || server.command}</div>
                )}
                {server.lastError && <div className="mcp-error text-[12px] text-danger">{server.lastError}</div>}
                {loadingThis && <div className="mcp-item text-[12px] text-ink-muted">{t('Loading…')}</div>}
                {details && (
                  <div className="mcp-details flex flex-col gap-2.5 border-t border-line-subtle pt-2.5">
                    <div className="flex flex-col gap-1">
                      <span className="mcp-section-label text-[11px] font-semibold text-ink-muted">{t('Prompts')}: {details.prompts.length}</span>
                      {details.prompts.map((p) => {
                        const pKey = `${server.name}:prompt:${p.name}`
                        const isOpen = activePromptKey === pKey
                        const isBusy = loadingKey === pKey
                        return (
                          <div className="mcp-interactive-item flex flex-col gap-1" key={p.name}>
                            <button
                              type="button"
                              className={cn(
                                'mcp-item-btn flex w-full cursor-pointer items-center gap-1.5 rounded-[7px] border-0 px-2 py-1.5 text-left text-[12.5px] transition-[background] duration-120 hover:bg-hover',
                                isOpen ? 'active bg-muted text-ink font-medium' : 'bg-transparent text-ink-secondary',
                              )}
                              onClick={() => {
                                if ((p.arguments?.length ?? 0) > 0) {
                                  setActivePromptKey((cur) => (cur === pKey ? '' : pKey))
                                } else {
                                  setActivePromptKey(pKey)
                                  void fetchPromptPreview(server.name, p)
                                }
                              }}
                            >
                              <ChevronRight size={12} className={cn('shrink-0 transition-transform duration-150', isOpen && 'rotate-90')} />
                              <span className="truncate text-ink">{p.title || p.name}</span>
                              {p.description && <small className="min-w-0 flex-1 truncate text-[11px] text-ink-muted">{p.description}</small>}
                            </button>
                            {isOpen && (
                              <div className="mcp-arg-panel flex flex-col gap-2 rounded-[8px] border border-line-subtle bg-subtle p-2.5">
                                {(p.arguments ?? []).map((arg) => (
                                  <label className="mcp-arg-field flex flex-col gap-1 text-[11.5px] text-ink-secondary" key={arg.name}>
                                    <span>
                                      {arg.name}{arg.required ? ' *' : ''}
                                      {arg.description ? ` — ${arg.description}` : ''}
                                    </span>
                                    <input
                                      type="text"
                                      className={cn(fieldInput, 'h-[31px] text-[12px]')}
                                      value={promptArgs[`${pKey}:${arg.name}`] ?? ''}
                                      disabled={isBusy}
                                      onChange={(event) => setPromptArgs((prev) => ({
                                        ...prev,
                                        [`${pKey}:${arg.name}`]: event.target.value,
                                      }))}
                                    />
                                  </label>
                                ))}
                                <button
                                  type="button"
                                  className={btnSecondary}
                                  disabled={isBusy}
                                  onClick={() => void fetchPromptPreview(server.name, p)}
                                >
                                  {isBusy ? t('Loading…') : t('Load prompt')}
                                </button>
                                {renderPreview(pKey)}
                              </div>
                            )}
                          </div>
                        )
                      })}
                    </div>

                    <div className="flex flex-col gap-1">
                      <span className="mcp-section-label text-[11px] font-semibold text-ink-muted">{t('Resources')}: {details.resources.length}</span>
                      {details.resources.map((r) => {
                        const rKey = `${server.name}:res:${r.uri}`
                        const isBusy = loadingKey === rKey
                        const isSelected = previewState?.key === rKey
                        return (
                          <div className="mcp-interactive-item flex flex-col gap-1" key={r.uri}>
                            <button
                              type="button"
                              className={cn(
                                'mcp-item-btn flex w-full cursor-pointer items-center gap-1.5 rounded-[7px] border-0 px-2 py-1.5 text-left text-[12.5px] transition-[background] duration-120 hover:bg-hover',
                                isSelected ? 'active bg-muted text-ink font-medium' : 'bg-transparent text-ink-secondary',
                              )}
                              disabled={isBusy}
                              onClick={() => void readResourcePreview(server.name, rKey, r.title || r.name || r.uri, r.uri)}
                            >
                              <ChevronRight size={12} className={cn('shrink-0 transition-transform duration-150', isSelected && 'rotate-90')} />
                              <span className="truncate text-ink">{r.title || r.name || r.uri}</span>
                              <small className="min-w-0 flex-1 truncate font-mono text-[11px] text-ink-muted">{r.mimeType || r.uri}</small>
                            </button>
                            {renderPreview(rKey)}
                          </div>
                        )
                      })}
                    </div>

                    {details.templates.length > 0 && (
                      <div className="flex flex-col gap-1">
                        <span className="mcp-section-label text-[11px] font-semibold text-ink-muted">{t('Resource templates')}: {details.templates.length}</span>
                        {details.templates.map((tpl) => {
                          const tKey = `${server.name}:tpl:${tpl.uriTemplate}`
                          const isOpen = activeTemplateKey === tKey
                          const params = parseTemplateParams(tpl.uriTemplate)
                          const resolved = resolveUriTemplate(
                            tpl.uriTemplate,
                            Object.fromEntries(params.map((name) => [name, templateVars[`${tKey}:${name}`] ?? ''])),
                          )
                          const isBusy = loadingKey === tKey
                          return (
                            <div className="mcp-interactive-item flex flex-col gap-1" key={tpl.uriTemplate}>
                              <button
                                type="button"
                                className={cn(
                                  'mcp-item-btn flex w-full cursor-pointer items-center gap-1.5 rounded-[7px] border-0 px-2 py-1.5 text-left text-[12.5px] transition-[background] duration-120 hover:bg-hover',
                                  isOpen ? 'active bg-muted text-ink font-medium' : 'bg-transparent text-ink-secondary',
                                )}
                                onClick={() => setActiveTemplateKey((cur) => (cur === tKey ? '' : tKey))}
                              >
                                <ChevronRight size={12} className={cn('shrink-0 transition-transform duration-150', isOpen && 'rotate-90')} />
                                <span className="truncate text-ink">{tpl.title || tpl.name || tpl.uriTemplate}</span>
                                <small className="min-w-0 flex-1 truncate font-mono text-[11px] text-ink-muted">{tpl.uriTemplate}</small>
                              </button>
                              {isOpen && (
                                <div className="mcp-arg-panel flex flex-col gap-2 rounded-[8px] border border-line-subtle bg-subtle p-2.5">
                                  {params.map((param) => (
                                    <label className="mcp-arg-field flex flex-col gap-1 text-[11.5px] text-ink-secondary" key={param}>
                                      <span>{param}</span>
                                      <input
                                        type="text"
                                        className={cn(fieldInput, 'h-[31px] text-[12px]')}
                                        value={templateVars[`${tKey}:${param}`] ?? ''}
                                        disabled={isBusy}
                                        onChange={(event) => setTemplateVars((prev) => ({
                                          ...prev,
                                          [`${tKey}:${param}`]: event.target.value,
                                        }))}
                                      />
                                    </label>
                                  ))}
                                  <div className="mcp-resolved-uri truncate font-mono text-[11px] text-ink-muted">{resolved}</div>
                                  <button
                                    type="button"
                                    className={btnSecondary}
                                    disabled={isBusy}
                                    onClick={() => void readResourcePreview(server.name, tKey, tpl.title || tpl.name || resolved, resolved)}
                                  >
                                    {isBusy ? t('Loading…') : t('Read resource')}
                                  </button>
                                  {renderPreview(tKey)}
                                </div>
                              )}
                            </div>
                          )
                        })}
                      </div>
                    )}
                  </div>
                )}
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}

const EFFORT_LABELS: ReadonlyArray<{ value: string; label: string }> = [
  { value: '', label: 'Auto' },
  { value: 'none', label: 'None' },
  { value: 'minimal', label: 'Minimal' },
  { value: 'low', label: 'Low' },
  { value: 'medium', label: 'Medium' },
  { value: 'high', label: 'High' },
  { value: 'xhigh', label: 'XHigh' },
  { value: 'max', label: 'Max' },
]

const PERM_LABEL: Record<string, string> = {
  'read-only': 'Read-only',
  workspace: 'Workspace',
  auto: 'Auto review',
  'full-access': 'Full access',
}

function ContextInspector({
  activeSession,
  runtime,
  models,
  capabilities,
  onChangeReasoningEffort,
  permissionMode,
  allowedPermissionModes,
  onChangePermissionMode,
}: {
  activeSession: SessionSummary | null
  runtime: RuntimeState
  models: ModelOption[]
  capabilities: string[]
  onChangeReasoningEffort?: (effort: string) => void
  permissionMode?: string
  allowedPermissionModes?: string[]
  onChangePermissionMode?: (mode: string) => void
}) {
  const { t } = useTranslation()
  const usage = activeSession?.sessionUsage
  const selectedProfile = activeSession?.settings.modelProfile || models[0]?.profile || ''
  const modelOption = models.find((m) => m.profile === selectedProfile)
  const contextLimit = runtime.task.contextWindow || modelOption?.contextWindow || 0
  const latestInput = usage?.estimated_input || usage?.input || 0
  const contextPct = contextLimit > 0 ? Math.min(100, Math.round((latestInput / contextLimit) * 100)) : 0
  const cachePct = usage && usage.input > 0 ? Math.round((usage.cached / usage.input) * 100) : 0
  const busy = runtime.task.status === 'running' || runtime.task.status === 'queued' || runtime.task.status === 'paused' || runtime.queue.length > 0
  const currentEffort = activeSession?.settings.reasoningEffort || ''
  const currentEffortLabel = EFFORT_LABELS.find((e) => e.value === currentEffort)?.label ?? currentEffort
  const permModes = allowedPermissionModes?.length ? allowedPermissionModes : ['read-only', 'workspace', 'auto', 'full-access']

  const kvRowCls = 'flex items-center justify-between gap-2 text-[12.5px] text-ink-secondary [&_code]:truncate [&_code]:font-mono [&_code]:text-[11.5px] [&_code]:text-ink'

  return (
    <div className="inspector-pane flex min-h-0 flex-1 flex-col gap-3.5 overflow-y-auto p-4">
      <div className="inspector-head flex items-center justify-between">
        <h3 className="m-0 text-[14px] font-semibold text-ink">{t('Context & runtime')}</h3>
      </div>

      <div className={cn('inspector-section flex flex-col gap-2 p-3', subtleCard, 'bg-canvas')}>
        <div className={kvRowCls}><span>{t('Session ID')}</span><code>{activeSession?.id ?? '-'}</code></div>
        <div className={kvRowCls}><span>{t('Model')}</span><code>{modelOption?.displayName || modelOption?.model || selectedProfile || '-'}</code></div>
        <div className={kvRowCls}>
          <span>{t('Reasoning effort')}</span>
          {onChangeReasoningEffort ? (
            <select
              className={cn('inspector-select', fieldSelect, 'h-[28px] w-auto max-w-[170px] text-[12px]')}
              aria-label={t('Reasoning effort')}
              value={currentEffort}
              disabled={!activeSession || busy}
              onChange={(e) => onChangeReasoningEffort(e.target.value)}
            >
              {EFFORT_LABELS.map((item) => (
                <option key={item.value || 'auto'} value={item.value}>{item.label}</option>
              ))}
            </select>
          ) : (
            <code>{currentEffortLabel}</code>
          )}
        </div>
        {capabilities.includes('permission.manage') && permissionMode && (
          <div className={kvRowCls}>
            <span>{t('Permission mode')}</span>
            {onChangePermissionMode ? (
              <select
                className={cn('inspector-select', fieldSelect, 'h-[28px] w-auto max-w-[170px] text-[12px]')}
                aria-label={t('Permission mode')}
                value={permissionMode}
                disabled={busy}
                onChange={(e) => onChangePermissionMode(e.target.value)}
              >
                {permModes.map((mode) => (
                  <option key={mode} value={mode}>{t(PERM_LABEL[mode] ?? mode)} ({mode})</option>
                ))}
              </select>
            ) : (
              <code>{t(PERM_LABEL[permissionMode] ?? permissionMode)}</code>
            )}
          </div>
        )}
        <div className={kvRowCls}><span>{t('Task state')}</span><code>{t(runtime.task.status || 'idle')}</code></div>
        <div className={kvRowCls}><span>{t('Queued turns')}</span><code>{runtime.queue.length}</code></div>
      </div>

      {usage && (
        <div className={cn('inspector-section flex flex-col gap-2 p-3', subtleCard, 'bg-canvas')}>
          <div className="kv-title flex items-center gap-1.5 text-[12px] font-semibold text-ink"><Cpu size={13} aria-hidden="true" /><span>{t('Token usage')}</span></div>
          {contextLimit > 0 && (
            <>
              <div className={kvRowCls}>
                <span>{t('Context window')}</span>
                <code>{latestInput.toLocaleString()} / {contextLimit.toLocaleString()} ({contextPct}%)</code>
              </div>
              <div className="meter-track h-1.5 w-full overflow-hidden rounded-full bg-muted" aria-hidden="true">
                <div className="meter-fill h-full rounded-full bg-ink transition-[width] duration-200" style={{ width: `${contextPct}%` }} />
              </div>
            </>
          )}
          <div className={kvRowCls}><span>{t('Input tokens')}</span><code>{usage.input.toLocaleString()}</code></div>
          <div className={kvRowCls}><span>{t('Output tokens')}</span><code>{usage.output.toLocaleString()}</code></div>
          <div className={kvRowCls}><span>{t('Cached input')}</span><code>{usage.cached.toLocaleString()} ({cachePct}%)</code></div>
          {usage.reasoning > 0 && <div className={kvRowCls}><span>{t('Reasoning tokens')}</span><code>{usage.reasoning.toLocaleString()}</code></div>}
        </div>
      )}

      <div className={cn('inspector-section flex flex-col gap-2 p-3', subtleCard, 'bg-canvas')}>
        <div className="kv-title flex items-center gap-1.5 text-[12px] font-semibold text-ink"><span>{t('Core capabilities')} ({capabilities.length})</span></div>
        <div className="cap-pills flex flex-wrap gap-1.5">
          {capabilities.map((cap) => (
            <span className="cap-pill rounded-[6px] border border-line-subtle bg-subtle px-2 py-0.5 font-mono text-[11px] text-ink-secondary" key={cap}>{cap}</span>
          ))}
        </div>
      </div>
    </div>
  )
}

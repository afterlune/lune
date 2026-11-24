import { useCallback, useEffect, useRef, useState, type FormEvent } from 'react'
import {
  Activity, Check, ChevronRight, Copy, Cpu, FolderOpen, Maximize2, Minimize2, Pause, Play,
  Plug, RefreshCw, Settings, Square, Target, Terminal, Trash2, X,
} from 'lucide-react'
import { useTranslate } from './language'
import type { RuntimeClient } from './runtime'
import { WorkspaceFiles, type WorkspaceInfo } from './workspace-files'
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
  const t = useTranslate()
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

  if (!visible) return null

  return (
    <aside
      className={`harness-panel${fullScreen ? ' fullscreen' : ''}`}
      aria-label={t('Harness')}
      onKeyDown={(event) => { if (event.key === 'Escape') onClose() }}
    >
      <header className="harness-panel-header">
        <div className="harness-tabs" role="tablist">
          {visibleTabs.map((item) => {
            const Icon = item.icon
            return (
              <button
                key={item.id}
                type="button"
                role="tab"
                aria-selected={effectiveTab === item.id}
                className={effectiveTab === item.id ? 'active' : ''}
                onClick={() => onTabChange(item.id)}
              >
                <Icon size={14} aria-hidden="true" />
                <span>{t(item.label)}</span>
              </button>
            )
          })}
        </div>
        <button type="button" className="icon-button" aria-label={t(fullScreen ? 'Exit full screen' : 'Expand panel')} title={t(fullScreen ? 'Exit full screen' : 'Expand panel')} onClick={onToggleFullScreen}>
          {fullScreen ? <Minimize2 size={15} /> : <Maximize2 size={15} />}
        </button>
        <button type="button" className="icon-button" aria-label={t('Close')} title={t('Close')} onClick={onClose}>
          <X size={16} />
        </button>
      </header>

      <div className="harness-panel-body">
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
  const t = useTranslate()
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

  return (
    <div className="inspector-pane">
      <div className="inspector-head">
        <h3>{t('Persistent goal')}</h3>
        <button type="button" className="subtle-icon" aria-label={t('Refresh')} disabled={busy} onClick={() => void load()}>
          <RefreshCw size={13} className={busy ? 'spin' : undefined} />
        </button>
      </div>
      {error && <div className="form-error" role="alert">{error}</div>}

      {!goal || goal.status === 'complete' ? (
        <form className="inspector-form" onSubmit={createGoal}>
          <p className="inspector-hint">{t('Create a persistent objective that guides autonomous execution across turns.')}</p>
          <label className="form-field">
            {t('Objective')}
            <textarea
              rows={4}
              value={objective}
              disabled={busy || sessionBusy}
              onChange={(event) => setObjective(event.target.value)}
              placeholder={t('Describe the goal to pursue…')}
              required
            />
          </label>
          <label className="form-field">
            {t('Token budget (optional)')}
            <input
              type="number"
              min={1}
              value={budget}
              disabled={busy || sessionBusy}
              onChange={(event) => setBudget(event.target.value)}
              placeholder="e.g. 120000"
            />
          </label>
          <button className="primary-button compact" disabled={busy || sessionBusy || !objective.trim()}>
            <Target size={14} aria-hidden="true" />
            <span>{t('Start goal')}</span>
          </button>
        </form>
      ) : (
        <div className="goal-card">
          <div className="goal-status-row">
            <span className={`status-pill ${goal.status}`}>{t(goal.status)}</span>
            <span className="goal-rev">rev {goal.revision}</span>
          </div>
          {!editing ? (
            <>
              <p className="goal-objective">{goal.objective}</p>
              <div className="goal-metrics">
                <div>
                  <span>{t('Tokens used')}</span>
                  <strong>{goal.tokens_used.toLocaleString()}{goal.token_budget ? ` / ${goal.token_budget.toLocaleString()}` : ''}</strong>
                </div>
                <div>
                  <span>{t('Cached')}</span>
                  <strong>{goal.cached_tokens.toLocaleString()}</strong>
                </div>
              </div>
              {goal.token_budget && goal.token_budget > 0 && (
                <div className="meter-track" aria-hidden="true">
                  <div className="meter-fill" style={{ width: `${Math.min(100, Math.round((goal.tokens_used / goal.token_budget) * 100))}%` }} />
                </div>
              )}
              {goal.stop_reason && <p className="inspector-hint">{goal.stop_reason}</p>}
              <div className="goal-actions">
                {goal.status === 'active' ? (
                  <button type="button" className="secondary-button" disabled={busy} onClick={() => void run(async () => { await client.request('goal.pause', { sessionId }) })}>
                    <Pause size={13} aria-hidden="true" />
                    <span>{t('Pause')}</span>
                  </button>
                ) : (
                  <button type="button" className="primary-button compact" disabled={busy || sessionBusy} onClick={() => void run(async () => { await client.request('goal.resume', { sessionId }) })}>
                    <Play size={13} aria-hidden="true" />
                    <span>{t('Resume')}</span>
                  </button>
                )}
                <button
                  type="button"
                  className="secondary-button"
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
                  className="plain-button danger"
                  disabled={busy || sessionBusy}
                  onClick={() => void run(async () => { await client.request('goal.clear', { sessionId }) })}
                >
                  <Trash2 size={13} aria-hidden="true" />
                  <span>{t('Clear')}</span>
                </button>
              </div>
            </>
          ) : (
            <form className="inspector-form" onSubmit={saveEdit}>
              <label className="form-field">
                {t('Objective')}
                <textarea rows={3} value={objective} disabled={busy} onChange={(event) => setObjective(event.target.value)} required />
              </label>
              <label className="form-field">
                {t('Token budget (optional)')}
                <input type="number" min={1} value={budget} disabled={busy} onChange={(event) => setBudget(event.target.value)} />
              </label>
              <div className="goal-actions">
                <button type="button" className="plain-button" disabled={busy} onClick={() => setEditing(false)}>{t('Cancel')}</button>
                <button className="primary-button compact" disabled={busy}>{t('Save')}</button>
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
  const t = useTranslate()
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
    <div className="inspector-pane">
      <div className="inspector-head">
        <h3>{t('Commands & processes')}</h3>
        <button type="button" className="subtle-icon" aria-label={t('Refresh')} disabled={busy} onClick={() => { void loadList(); if (selectedId) void loadOutput(selectedId) }}>
          <RefreshCw size={13} className={busy ? 'spin' : undefined} />
        </button>
      </div>
      {error && <div className="form-error" role="alert">{error}</div>}
      {commands.length === 0 ? (
        <p className="inspector-hint">{t('No commands recorded in this session.')}</p>
      ) : (
        <div className="process-list">
          {commands.map((cmd) => {
            const active = cmd.status === 'running' || cmd.status === 'stopping'
            return (
              <div className={`process-row${selectedId === cmd.id ? ' selected' : ''}`} key={cmd.id}>
                <button type="button" className="process-select" onClick={() => setSelectedId(cmd.id)}>
                  <span className={`process-dot ${active ? 'running' : cmd.status}`} />
                  <span className="process-cmd" title={cmd.command_text}>{cmd.command_text}</span>
                  <span className="process-meta">
                    {cmd.status}
                    {cmd.exit_code !== undefined ? ` (${cmd.exit_code})` : ''}
                  </span>
                </button>
                {active && (
                  <button
                    type="button"
                    className="subtle-icon danger"
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
        <div className="process-log-wrap">
          <div className="process-log-head">
            <span className="process-log-title">
              {selectedActive && <span className="process-live-dot" aria-hidden="true" />}
              {output.command.id.slice(0, 8)} · {output.command.status}
            </span>
            <div className="process-log-actions">
              <button
                type="button"
                className={`process-follow-btn${followLog ? ' active' : ''}`}
                aria-pressed={followLog}
                onClick={() => setFollowLog((v) => !v)}
              >
                {t('Follow')}
              </button>
              <button type="button" className="plain-button" onClick={() => void loadOutput(output.command.id)}>
                <RefreshCw size={12} />
                <span>{t('Refresh')}</span>
              </button>
            </div>
          </div>
          <pre className="process-log" ref={logRef} onScroll={handleLogScroll}>{output.output || t('(no output yet)')}</pre>
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
  const t = useTranslate()
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
      <div className="mcp-detail-drawer">
        <div className="mcp-preview-head">
          <span>{previewState.title}</span>
          <div className="mcp-preview-actions">
            <button type="button" className="plain-button" onClick={() => void copyPreview(key, previewState.text)}>
              {copiedKey === key ? <Check size={12} /> : <Copy size={12} />}
              <span>{t(copiedKey === key ? 'Copied' : 'Copy')}</span>
            </button>
            {onInsertText && previewState.text.trim() && (
              <button
                type="button"
                className="primary-button compact"
                onClick={() => onInsertText(previewState.text)}
              >
                {t('Insert into composer')}
              </button>
            )}
          </div>
        </div>
        <pre className="mcp-preview-pre">{previewState.text || t('(empty)')}</pre>
      </div>
    )
  }

  return (
    <div className="inspector-pane">
      <div className="inspector-head">
        <h3>{t('MCP servers')}</h3>
        <div className="inspector-head-actions">
          {onOpenMCPSettings && (
            <button type="button" className="plain-button" onClick={onOpenMCPSettings}>
              <Settings size={12} aria-hidden="true" />
              <span>{t('Configure')}</span>
            </button>
          )}
          <button type="button" className="subtle-icon" aria-label={t('Refresh')} disabled={busy} onClick={() => void load()}>
            <RefreshCw size={13} className={busy ? 'spin' : undefined} />
          </button>
        </div>
      </div>
      {error && <div className="form-error" role="alert">{error}</div>}
      {servers.length === 0 ? (
        <p className="inspector-hint">{t('No MCP servers configured in Core.')}</p>
      ) : (
        <div className="mcp-list">
          {servers.map((server) => {
            const details = expanded[server.name]
            const loadingThis = loadingKey === `server:${server.name}`
            return (
              <div className="mcp-card" key={server.name}>
                <div className="mcp-card-head">
                  <button type="button" className="mcp-title-btn" onClick={() => void inspectServer(server)}>
                    {server.connected && <ChevronRight size={13} className={details ? 'rotated' : undefined} />}
                    <strong>{server.name}</strong>
                    <span className="mcp-meta">{server.transport} · {server.toolCount} {t('tools')}</span>
                  </button>
                  <button
                    type="button"
                    className={server.enabled ? 'secondary-button' : 'primary-button compact'}
                    disabled={busy}
                    onClick={() => void toggleServer(server)}
                  >
                    {t(server.enabled ? 'Disable' : 'Enable')}
                  </button>
                </div>
                {(server.endpoint || server.command) && (
                  <div className="mcp-endpoint">{server.endpoint || server.command}</div>
                )}
                {server.lastError && <div className="mcp-error">{server.lastError}</div>}
                {loadingThis && <div className="mcp-item">{t('Loading…')}</div>}
                {details && (
                  <div className="mcp-details">
                    <div>
                      <span className="mcp-section-label">{t('Prompts')}: {details.prompts.length}</span>
                      {details.prompts.map((p) => {
                        const pKey = `${server.name}:prompt:${p.name}`
                        const isOpen = activePromptKey === pKey
                        const isBusy = loadingKey === pKey
                        return (
                          <div className="mcp-interactive-item" key={p.name}>
                            <button
                              type="button"
                              className={`mcp-item-btn${isOpen ? ' active' : ''}`}
                              onClick={() => {
                                if ((p.arguments?.length ?? 0) > 0) {
                                  setActivePromptKey((cur) => (cur === pKey ? '' : pKey))
                                } else {
                                  setActivePromptKey(pKey)
                                  void fetchPromptPreview(server.name, p)
                                }
                              }}
                            >
                              <ChevronRight size={12} className={isOpen ? 'rotated' : undefined} />
                              <span>{p.title || p.name}</span>
                              {p.description && <small>{p.description}</small>}
                            </button>
                            {isOpen && (
                              <div className="mcp-arg-panel">
                                {(p.arguments ?? []).map((arg) => (
                                  <label className="mcp-arg-field" key={arg.name}>
                                    <span>
                                      {arg.name}{arg.required ? ' *' : ''}
                                      {arg.description ? ` — ${arg.description}` : ''}
                                    </span>
                                    <input
                                      type="text"
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
                                  className="secondary-button"
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

                    <div>
                      <span className="mcp-section-label">{t('Resources')}: {details.resources.length}</span>
                      {details.resources.map((r) => {
                        const rKey = `${server.name}:res:${r.uri}`
                        const isBusy = loadingKey === rKey
                        return (
                          <div className="mcp-interactive-item" key={r.uri}>
                            <button
                              type="button"
                              className={`mcp-item-btn${previewState?.key === rKey ? ' active' : ''}`}
                              disabled={isBusy}
                              onClick={() => void readResourcePreview(server.name, rKey, r.title || r.name || r.uri, r.uri)}
                            >
                              <ChevronRight size={12} className={previewState?.key === rKey ? 'rotated' : undefined} />
                              <span>{r.title || r.name || r.uri}</span>
                              <small>{r.mimeType || r.uri}</small>
                            </button>
                            {renderPreview(rKey)}
                          </div>
                        )
                      })}
                    </div>

                    {details.templates.length > 0 && (
                      <div>
                        <span className="mcp-section-label">{t('Resource templates')}: {details.templates.length}</span>
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
                            <div className="mcp-interactive-item" key={tpl.uriTemplate}>
                              <button
                                type="button"
                                className={`mcp-item-btn${isOpen ? ' active' : ''}`}
                                onClick={() => setActiveTemplateKey((cur) => (cur === tKey ? '' : tKey))}
                              >
                                <ChevronRight size={12} className={isOpen ? 'rotated' : undefined} />
                                <span>{tpl.title || tpl.name || tpl.uriTemplate}</span>
                                <small>{tpl.uriTemplate}</small>
                              </button>
                              {isOpen && (
                                <div className="mcp-arg-panel">
                                  {params.map((param) => (
                                    <label className="mcp-arg-field" key={param}>
                                      <span>{param}</span>
                                      <input
                                        type="text"
                                        value={templateVars[`${tKey}:${param}`] ?? ''}
                                        disabled={isBusy}
                                        onChange={(event) => setTemplateVars((prev) => ({
                                          ...prev,
                                          [`${tKey}:${param}`]: event.target.value,
                                        }))}
                                      />
                                    </label>
                                  ))}
                                  <div className="mcp-resolved-uri">{resolved}</div>
                                  <button
                                    type="button"
                                    className="secondary-button"
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
  const t = useTranslate()
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

  return (
    <div className="inspector-pane">
      <div className="inspector-head">
        <h3>{t('Context & runtime')}</h3>
      </div>

      <div className="inspector-section">
        <div className="kv-row"><span>{t('Session ID')}</span><code>{activeSession?.id ?? '-'}</code></div>
        <div className="kv-row"><span>{t('Model')}</span><code>{modelOption?.displayName || modelOption?.model || selectedProfile || '-'}</code></div>
        <div className="kv-row">
          <span>{t('Reasoning effort')}</span>
          {onChangeReasoningEffort ? (
            <select
              className="inspector-select"
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
          <div className="kv-row">
            <span>{t('Permission mode')}</span>
            {onChangePermissionMode ? (
              <select
                className="inspector-select"
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
        <div className="kv-row"><span>{t('Task state')}</span><code>{t(runtime.task.status || 'idle')}</code></div>
        <div className="kv-row"><span>{t('Queued turns')}</span><code>{runtime.queue.length}</code></div>
      </div>

      {usage && (
        <div className="inspector-section">
          <div className="kv-title"><Cpu size={13} aria-hidden="true" /><span>{t('Token usage')}</span></div>
          {contextLimit > 0 && (
            <>
              <div className="kv-row">
                <span>{t('Context window')}</span>
                <code>{latestInput.toLocaleString()} / {contextLimit.toLocaleString()} ({contextPct}%)</code>
              </div>
              <div className="meter-track" aria-hidden="true">
                <div className="meter-fill" style={{ width: `${contextPct}%` }} />
              </div>
            </>
          )}
          <div className="kv-row"><span>{t('Input tokens')}</span><code>{usage.input.toLocaleString()}</code></div>
          <div className="kv-row"><span>{t('Output tokens')}</span><code>{usage.output.toLocaleString()}</code></div>
          <div className="kv-row"><span>{t('Cached input')}</span><code>{usage.cached.toLocaleString()} ({cachePct}%)</code></div>
          {usage.reasoning > 0 && <div className="kv-row"><span>{t('Reasoning tokens')}</span><code>{usage.reasoning.toLocaleString()}</code></div>}
        </div>
      )}

      <div className="inspector-section">
        <div className="kv-title"><span>{t('Core capabilities')} ({capabilities.length})</span></div>
        <div className="cap-pills">
          {capabilities.map((cap) => <span className="cap-pill" key={cap}>{cap}</span>)}
        </div>
      </div>
    </div>
  )
}

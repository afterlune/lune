import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { ChevronDown, ChevronRight, Folder, FolderOpen, Plus, SquarePen, X } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import type { SessionSummary } from './harness-types'
import { btnIcon, btnPlain, cn } from './ui'

export interface WorkspaceTreeSession {
  id: string
  title: string
  archived: boolean
  updatedAt: number
  taskStatus?: string
  executionId?: string
  queueCount?: number
}

export interface WorkspaceTreeItem {
  id: string
  path: string
  name: string
  activity?: string
  sessions: WorkspaceTreeSession[]
}

const FOLDED_PREVIEW_LIMIT = 5
const TREE_STATE_KEY = 'lune:workspace-tree:v1'

interface WorkspaceTreeState {
  expandedIds: string[]
  showAllIds: string[]
}

function readTreeState(): WorkspaceTreeState {
  try {
    const saved = JSON.parse(window.localStorage.getItem(TREE_STATE_KEY) || 'null') as Partial<WorkspaceTreeState> | null
    return {
      expandedIds: Array.isArray(saved?.expandedIds) ? saved.expandedIds.filter((id): id is string => typeof id === 'string') : [],
      showAllIds: Array.isArray(saved?.showAllIds) ? saved.showAllIds.filter((id): id is string => typeof id === 'string') : [],
    }
  } catch {
    return { expandedIds: [], showAllIds: [] }
  }
}

function sameIds(left: string[], right: string[]): boolean {
  return left.length === right.length && left.every((id, index) => id === right[index])
}

function workspaceLabel(item: { name?: string; path: string }): string {
  return item.name?.trim() || item.path.split(/[\\/]/).filter(Boolean).at(-1) || item.path
}

function sortSessionsByTime<T>(items: T[], getTs: (item: T) => number): T[] {
  return [...items].sort((a, b) => getTs(b) - getTs(a))
}

function taskStatusLabel(status?: string): string {
  switch (status) {
    case 'running': return 'Running'
    case 'queued': return 'Queued'
    case 'paused': return 'Paused'
    default: return ''
  }
}

export function WorkspaceTree({
  workspaces,
  activeWorkspaceId,
  activeSessionId,
  activeSessions,
  listLoading,
  moreSessions,
  onLoadMore,
  working,
  search,
  showArchived,
  onOpenWorkspacePicker,
  onSelectWorkspace,
  onSelectSession,
  onNewSessionInWorkspace,
  onCloseWorkspace,
  renderActiveSessionActions,
}: {
  workspaces: WorkspaceTreeItem[]
  activeWorkspaceId: string
  activeSessionId: string
  activeSessions: SessionSummary[]
  listLoading: boolean
  moreSessions: boolean
  onLoadMore: () => void
  working: boolean
  search: string
  showArchived: boolean
  onOpenWorkspacePicker: () => void
  onSelectWorkspace: (workspaceId: string) => void
  onSelectSession: (workspaceId: string, sessionId: string) => void
  onNewSessionInWorkspace: (workspaceId: string) => void
  onCloseWorkspace?: (workspaceId: string) => void
  renderActiveSessionActions: (session: SessionSummary) => ReactNode
}) {
  const { t } = useTranslation()
  const [expandedIds, setExpandedIds] = useState<string[]>(() => {
    const saved = readTreeState().expandedIds
    return activeWorkspaceId && !saved.includes(activeWorkspaceId) ? [...saved, activeWorkspaceId] : saved
  })
  const [showAllIds, setShowAllIds] = useState<string[]>(() => readTreeState().showAllIds)
  const hasSeenWorkspaceList = useRef(workspaces.length > 0)
  if (workspaces.length > 0) hasSeenWorkspaceList.current = true

  // Workspace expansion belongs to the tree, not the current selection.
  useEffect(() => {
    // Desktop workspaces load asynchronously. An initial empty list is not a
    // signal that persisted IDs have been removed.
    if (!hasSeenWorkspaceList.current) return
    const available = new Set(workspaces.map((workspace) => workspace.id))
    setExpandedIds((prev) => {
      const next = prev.filter((id) => available.has(id))
      if (activeWorkspaceId && available.has(activeWorkspaceId) && !next.includes(activeWorkspaceId)) next.push(activeWorkspaceId)
      return sameIds(prev, next) ? prev : next
    })
    setShowAllIds((prev) => {
      const next = prev.filter((id) => available.has(id))
      return sameIds(prev, next) ? prev : next
    })
  }, [workspaces, activeWorkspaceId])

  useEffect(() => {
    try {
      window.localStorage.setItem(TREE_STATE_KEY, JSON.stringify({ expandedIds, showAllIds }))
    } catch {
      // The tree remains usable when browser storage is unavailable.
    }
  }, [expandedIds, showAllIds])

  const searchTerm = search.trim().toLowerCase()
  const sortedActiveSessions = useMemo(
    () => sortSessionsByTime(activeSessions, (s) => Date.parse(s.updatedAt) || 0),
    [activeSessions],
  )

  const toggleExpand = (id: string) => {
    setExpandedIds((prev) => (prev.includes(id) ? prev.filter((item) => item !== id) : [...prev, id]))
  }

  return (
    <div className="session-list min-h-0 flex-1 overflow-x-hidden overflow-y-auto pr-0.5" aria-busy={listLoading}>
      <section className="workspace-list grid gap-0.5" aria-label={t('Workspaces')}>
        <div className="workspace-list-heading flex select-none items-center justify-between px-2.5 pt-0.5 pb-1 text-[11.5px] font-medium text-ink-muted">
          <span>{t('Workspaces')}</span>
          <button
            type="button"
            className={cn('icon-button', btnIcon)}
            aria-label={t('Open workspace')}
            title={t('Open workspace')}
            onClick={onOpenWorkspacePicker}
          >
            <Plus size={16} />
          </button>
        </div>

        {!workspaces.length && (
          <button
            type="button"
            className="workspace-row-btn flex min-h-[33px] w-full cursor-pointer items-center gap-2 rounded-[9px] border-0 bg-transparent px-2.5 py-[5px] text-left text-[13.5px] text-ink hover:bg-hover"
            onClick={onOpenWorkspacePicker}
          >
            <FolderOpen size={15} className="shrink-0 text-ink-secondary" />
            <span className="min-w-0 flex-1 truncate">{t('Choose a workspace')}</span>
          </button>
        )}

        {workspaces.map((ws) => {
          const isActive = ws.id === activeWorkspaceId
          const isExpanded = expandedIds.includes(ws.id) || Boolean(searchTerm)
          const name = workspaceLabel(ws)

          const nonActiveFiltered = isActive
            ? []
            : sortSessionsByTime(
                ws.sessions.filter((s) =>
                  (showArchived || !s.archived) &&
                  (!searchTerm || (s.title || '').toLowerCase().includes(searchTerm) || s.id.toLowerCase().includes(searchTerm)),
                ),
                (s) => s.updatedAt || 0,
              )
          const showAllForWs = showAllIds.includes(ws.id) || Boolean(searchTerm)
          const visibleNonActive = showAllForWs ? nonActiveFiltered : nonActiveFiltered.slice(0, FOLDED_PREVIEW_LIMIT)

          return (
            <div className="workspace-branch grid gap-0.5" key={ws.id} data-workspace-id={ws.id}>
              <div
                className={cn(
                  'workspace-row group/ws flex items-center rounded-[9px] transition-[background] duration-120 hover:bg-hover focus-within:bg-hover',
                  isActive && 'selected bg-active',
                )}
              >
                <button
                  type="button"
                  className="workspace-chevron ml-1 inline-grid h-5 w-5 shrink-0 cursor-pointer place-items-center rounded-[5px] border-0 bg-transparent p-0 text-ink-muted hover:bg-hover hover:text-ink"
                  aria-label={t('Toggle workspace')}
                  aria-expanded={isExpanded}
                  title={t('Toggle workspace')}
                  onClick={(e) => {
                    e.stopPropagation()
                    toggleExpand(ws.id)
                  }}
                >
                  {isExpanded ? <ChevronDown size={13} aria-hidden="true" /> : <ChevronRight size={13} aria-hidden="true" />}
                </button>
                <button
                  type="button"
                  className={cn(
                    'workspace-row-btn flex min-h-[32px] min-w-0 flex-1 cursor-pointer items-center gap-1.5 rounded-[9px] border-0 bg-transparent pr-1.5 pl-1 py-[4px] text-left text-[13.5px] text-ink',
                    isActive && 'font-medium',
                  )}
                  aria-pressed={isActive}
                  title={ws.path}
                  onClick={() => {
                    if (!isActive) onSelectWorkspace(ws.id)
                    else setExpandedIds((prev) => (prev.includes(ws.id) ? prev : [...prev, ws.id]))
                  }}
                >
                  <Folder size={14} className={cn('shrink-0', isActive ? 'text-ink' : 'text-ink-secondary')} aria-hidden="true" />
                  <span className="min-w-0 flex-1 truncate">{name}</span>
                  {ws.activity && (
                    <span
                      className="workspace-activity inline-flex shrink-0 items-center gap-1 text-[11px] font-normal text-ink-muted"
                      title={t(ws.activity)}
                    >
                      <i className="h-1.5 w-1.5 rounded-full bg-ink animate-[pulse_1.5s_infinite]" aria-hidden="true" />
                    </span>
                  )}
                </button>
                <div className="workspace-row-actions mr-1 flex items-center gap-0.5 opacity-0 transition-opacity duration-120 group-hover/ws:opacity-100 group-focus-within/ws:opacity-100">
                  <button
                    type="button"
                    className="inline-grid h-6 w-6 cursor-pointer place-items-center rounded-[6px] border-0 bg-transparent text-ink-muted hover:bg-active hover:text-ink"
                    aria-label={t('New conversation in {{name}}', { name })}
                    title={t('New conversation')}
                    disabled={working}
                    onClick={(e) => {
                      e.stopPropagation()
                      onNewSessionInWorkspace(ws.id)
                    }}
                  >
                    <SquarePen size={12.5} aria-hidden="true" />
                  </button>
                  {onCloseWorkspace && (
                    <button
                      type="button"
                      className="workspace-close-btn inline-grid h-6 w-6 cursor-pointer place-items-center rounded-[6px] border-0 bg-transparent text-ink-muted hover:bg-active hover:text-ink"
                      aria-label={`${t('Close')} ${name}`}
                      title={t('Close workspace')}
                      onClick={(e) => {
                        e.stopPropagation()
                        onCloseWorkspace(ws.id)
                      }}
                    >
                      <X size={13} aria-hidden="true" />
                    </button>
                  )}
                </div>
              </div>

              {isExpanded && (
                <div className="workspace-tree-children ml-[14px] grid gap-[1px] border-l border-line-subtle pl-2 py-0.5">
                  {isActive ? (
                    <>
                      {sortedActiveSessions.map((session) => {
                        const preview = session.title || `${t('Conversation')} ${session.id.slice(0, 8)}`
                        const selected = session.id === activeSessionId
                        const taskStatus = taskStatusLabel(ws.sessions.find((item) => item.id === session.id)?.taskStatus)
                        return (
                          <div
                            key={session.id}
                            className={cn(
                              'session-row group/srow relative my-[1px] flex items-center rounded-[9px] transition-[background,box-shadow] duration-120 hover:bg-hover focus-within:bg-hover',
                              selected && 'selected bg-canvas shadow-[inset_0_0_0_1px_var(--border-subtle),0_1px_2px_light-dark(rgb(0_0_0/.04),rgb(0_0_0/.2))] before:absolute before:top-[7px] before:bottom-[7px] before:left-1 before:w-[2.5px] before:rounded-full before:bg-ink',
                            )}
                          >
                            <button
                              type="button"
                              className={cn(
                                'session-item m-0 min-h-[32px] min-w-0 flex-1 cursor-pointer truncate rounded-[9px] border-0 bg-transparent py-1 pr-2 text-left text-[13px] text-ink',
                                selected ? 'pl-3 font-medium' : 'px-2',
                              )}
                              disabled={working}
                              aria-current={selected ? 'true' : undefined}
                              onClick={() => onSelectSession(ws.id, session.id)}
                            >
                              <span className="block truncate">{preview}{session.archived ? t(' (archived)') : ''}</span>
                            </button>
                            {taskStatus && <span className="mr-2 shrink-0 text-[10.5px] text-ink-muted" title={t(taskStatus)}>{t(taskStatus)}</span>}
                            {renderActiveSessionActions(session)}
                          </div>
                        )
                      })}
                      {moreSessions && (
                        <button
                          type="button"
                          className={cn(btnPlain, 'sidebar-load-more mt-0.5 w-full justify-start px-2 text-[12px]')}
                          disabled={working || listLoading}
                          onClick={onLoadMore}
                        >
                          {t('Load more')}
                        </button>
                      )}
                      {!sortedActiveSessions.length && (
                        <div className="empty-sessions px-2 py-2.5 text-[12px] text-ink-muted">
                          {t(listLoading ? 'Loading conversations…' : searchTerm ? 'No matching conversations.' : 'No conversations yet.')}
                        </div>
                      )}
                    </>
                  ) : (
                    <>
                      {visibleNonActive.map((session) => {
                        const preview = session.title || `${t('Conversation')} ${session.id.slice(0, 8)}`
                        const taskStatus = taskStatusLabel(session.taskStatus)
                        return (
                          <div
                            key={session.id}
                            className="session-row group/srow relative my-[1px] flex items-center rounded-[9px] transition-[background] duration-120 hover:bg-hover focus-within:bg-hover"
                          >
                            <button
                              type="button"
                              className="session-item m-0 min-h-[30px] min-w-0 flex-1 cursor-pointer truncate rounded-[9px] border-0 bg-transparent px-2 py-1 text-left text-[13px] text-ink-secondary hover:text-ink"
                              disabled={working}
                              onClick={() => onSelectSession(ws.id, session.id)}
                            >
                              <span className="block truncate">{preview}{session.archived ? t(' (archived)') : ''}</span>
                            </button>
                            {taskStatus && <span className="mr-2 shrink-0 text-[10.5px] text-ink-muted" title={t(taskStatus)}>{t(taskStatus)}</span>}
                          </div>
                        )
                      })}
                      {!showAllForWs && nonActiveFiltered.length > FOLDED_PREVIEW_LIMIT && (
                        <button
                          type="button"
                          className={cn(btnPlain, 'w-full justify-start px-2 py-1 text-[11.5px] text-ink-muted')}
                          onClick={() => setShowAllIds((prev) => [...prev, ws.id])}
                        >
                          {t('Show all ({{count}})', { count: nonActiveFiltered.length })}
                        </button>
                      )}
                      {!nonActiveFiltered.length && (
                        <div className="px-2 py-1.5 text-[12px] text-ink-muted">
                          {t(searchTerm ? 'No matching conversations.' : 'No conversations yet.')}
                        </div>
                      )}
                    </>
                  )}
                </div>
              )}
            </div>
          )
        })}
      </section>
    </div>
  )
}

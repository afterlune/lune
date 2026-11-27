import { useEffect, useMemo, useRef, useState } from 'react'
import { CornerDownLeft, File, Folder, MessageSquare, Search, Settings, Sparkles } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import type { SessionSummary } from './harness-types'
import { backdropOverlay, badgeMono, cn } from './ui'
import { filterMentionItems, type MentionItem } from './workspace-mentions'

export interface PaletteActionItem {
  id: string
  label: string
  hint?: string
  section: 'Actions' | 'Settings'
  run: () => void
}

export interface PaletteWorkspaceItem {
  id: string
  name: string
  path: string
  active: boolean
}

type FlatPaletteItem =
  | { kind: 'action'; key: string; section: 'Actions' | 'Settings'; action: PaletteActionItem }
  | { kind: 'session'; key: string; session: SessionSummary }
  | { kind: 'workspace'; key: string; workspace: PaletteWorkspaceItem }
  | { kind: 'file'; key: string; file: MentionItem }

export function CommandPalette({
  open,
  onClose,
  sessions,
  activeSessionId,
  onSelectSession,
  workspaces,
  onSelectWorkspace,
  files,
  filesLoading,
  onOpenFile,
  actions,
}: {
  open: boolean
  onClose: () => void
  sessions: SessionSummary[]
  activeSessionId: string
  onSelectSession: (id: string) => void
  workspaces: PaletteWorkspaceItem[]
  onSelectWorkspace?: (id: string) => void
  files: MentionItem[]
  filesLoading: boolean
  onOpenFile: (path: string) => void
  actions: PaletteActionItem[]
}) {
  const { t } = useTranslation()
  const inputRef = useRef<HTMLInputElement | null>(null)
  const listRef = useRef<HTMLDivElement | null>(null)
  const [query, setQuery] = useState('')
  const [selectedIndex, setSelectedIndex] = useState(0)

  useEffect(() => {
    if (!open) return
    setQuery('')
    setSelectedIndex(0)
    const id = window.requestAnimationFrame(() => inputRef.current?.focus())
    return () => window.cancelAnimationFrame(id)
  }, [open])

  const q = query.trim().toLowerCase()

  const flatItems = useMemo<FlatPaletteItem[]>(() => {
    const result: FlatPaletteItem[] = []

    const matchingActions = actions.filter((a) =>
      a.section === 'Actions' && (!q || t(a.label).toLowerCase().includes(q) || a.label.toLowerCase().includes(q) || (a.hint || '').toLowerCase().includes(q)),
    ).slice(0, 6)
    for (const action of matchingActions) {
      result.push({ kind: 'action', key: `act:${action.id}`, section: 'Actions', action })
    }

    const matchingSessions = sessions
      .filter((s) => !s.archived && (!q || (s.title || `Conversation ${s.id.slice(0, 8)}`).toLowerCase().includes(q) || s.id.toLowerCase().includes(q)))
      .slice(0, 6)
    for (const session of matchingSessions) {
      result.push({ kind: 'session', key: `sess:${session.id}`, session })
    }

    if (onSelectWorkspace) {
      const matchingWorkspaces = workspaces
        .filter((w) => !q || w.name.toLowerCase().includes(q) || w.path.toLowerCase().includes(q))
        .slice(0, 5)
      for (const ws of matchingWorkspaces) {
        result.push({ kind: 'workspace', key: `ws:${ws.id}`, workspace: ws })
      }
    }

    const matchingFiles = filterMentionItems(files, q, 8)
    for (const file of matchingFiles) {
      result.push({ kind: 'file', key: `file:${file.path}`, file })
    }

    const matchingSettings = actions.filter((a) =>
      a.section === 'Settings' && (!q || t(a.label).toLowerCase().includes(q) || a.label.toLowerCase().includes(q) || (a.hint || '').toLowerCase().includes(q)),
    ).slice(0, 6)
    for (const action of matchingSettings) {
      result.push({ kind: 'action', key: `set:${action.id}`, section: 'Settings', action })
    }

    return result
  }, [actions, sessions, workspaces, onSelectWorkspace, files, q, t])

  useEffect(() => {
    setSelectedIndex((current) => (flatItems.length ? Math.min(current, flatItems.length - 1) : 0))
  }, [flatItems.length, query])

  useEffect(() => {
    if (!open || !listRef.current) return
    const activeEl = listRef.current.querySelector('.palette-item.active')
    if (activeEl instanceof HTMLElement) activeEl.scrollIntoView({ block: 'nearest' })
  }, [open, selectedIndex])

  if (!open) return null

  const runItem = (item: FlatPaletteItem | undefined) => {
    if (!item) return
    onClose()
    if (item.kind === 'action') item.action.run()
    else if (item.kind === 'session') onSelectSession(item.session.id)
    else if (item.kind === 'workspace') onSelectWorkspace?.(item.workspace.id)
    else if (item.kind === 'file') onOpenFile(item.file.path)
  }

  const sections: Array<{ title: string; items: Array<{ item: FlatPaletteItem; index: number }> }> = []
  const bySection = new Map<string, Array<{ item: FlatPaletteItem; index: number }>>()
  flatItems.forEach((item, index) => {
    const title = item.kind === 'action'
      ? item.section
      : item.kind === 'session'
        ? 'Conversations'
        : item.kind === 'workspace'
          ? 'Workspaces'
          : 'Files'
    let list = bySection.get(title)
    if (!list) {
      list = []
      bySection.set(title, list)
      sections.push({ title, items: list })
    }
    list.push({ item, index })
  })

  return (
    <div
      className={cn('command-palette-backdrop', backdropOverlay, 'z-50 items-start pt-[12vh]')}
      onMouseDown={(event) => {
        if (event.target === event.currentTarget) onClose()
      }}
    >
      <section
        className="command-palette flex w-[min(600px,calc(100vw-24px))] max-h-[min(520px,74dvh)] flex-col overflow-hidden rounded-[14px] border border-line bg-[var(--bg-glass-strong)] backdrop-blur-2xl shadow-elev-lg animate-[arrive_.14s_ease_both]"
        role="dialog"
        aria-modal="true"
        aria-label={t('Quick switcher')}
        onKeyDown={(event) => {
          if (event.key === 'Escape') {
            event.preventDefault()
            event.stopPropagation()
            onClose()
            return
          }
          if (event.key === 'ArrowDown' && flatItems.length > 0) {
            event.preventDefault()
            setSelectedIndex((i) => (i + 1) % flatItems.length)
            return
          }
          if (event.key === 'ArrowUp' && flatItems.length > 0) {
            event.preventDefault()
            setSelectedIndex((i) => (i - 1 + flatItems.length) % flatItems.length)
            return
          }
          if (event.key === 'Enter' && flatItems.length > 0) {
            event.preventDefault()
            runItem(flatItems[selectedIndex] ?? flatItems[0])
          }
        }}
      >
        <div className="palette-input-row flex items-center gap-2.5 border-b border-line-subtle px-3.5 py-2.5 text-ink-muted">
          <Search size={16} aria-hidden="true" />
          <input
            ref={inputRef}
            type="text"
            className="min-w-0 flex-1 border-0 bg-transparent text-[14px] text-ink outline-none placeholder:text-ink-faint"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder={t('Jump to conversation, file, workspace, or setting…')}
            aria-label={t('Quick switcher')}
          />
          <kbd className={cn('palette-kbd', badgeMono, 'px-1.5 py-0.5 text-[10.5px]')}>Esc</kbd>
        </div>

        <div className="palette-list flex-1 overflow-y-auto p-1.5" ref={listRef} role="listbox">
          {sections.length === 0 ? (
            <div className="palette-empty px-3 py-8 text-center text-[13px] text-ink-muted">
              {filesLoading ? t('Indexing workspace files…') : t('No matching results.')}
            </div>
          ) : (
            sections.map((section) => (
              <div className="palette-group mb-1.5 last:mb-0" key={section.title}>
                <div className="palette-group-label px-2.5 pt-1.5 pb-1 text-[11px] font-semibold tracking-[0.02em] text-ink-muted">
                  {t(section.title)}
                </div>
                {section.items.map(({ item, index }) => {
                  const active = index === selectedIndex
                  return (
                    <button
                      key={item.key}
                      type="button"
                      role="option"
                      aria-selected={active}
                      className={cn(
                        'palette-item flex w-full cursor-pointer items-center gap-2.5 rounded-[8px] border-0 px-2.5 py-[7px] text-left text-[13px] transition-[background,color] duration-100',
                        active ? 'active bg-hover text-ink' : 'bg-transparent text-ink-secondary hover:bg-hover hover:text-ink',
                      )}
                      onMouseEnter={() => setSelectedIndex(index)}
                      onClick={() => runItem(item)}
                    >
                      <span className="palette-item-icon grid h-5 w-5 shrink-0 place-items-center text-ink-muted" aria-hidden="true">
                        {item.kind === 'action' ? (
                          item.section === 'Settings' ? <Settings size={14} /> : <Sparkles size={14} />
                        ) : item.kind === 'session' ? (
                          <MessageSquare size={14} />
                        ) : item.kind === 'workspace' ? (
                          <Folder size={14} />
                        ) : item.file.kind === 'directory' ? (
                          <Folder size={14} />
                        ) : (
                          <File size={14} />
                        )}
                      </span>
                      <span className="palette-item-title min-w-0 flex-1 truncate font-medium text-ink">
                        {item.kind === 'action'
                          ? t(item.action.label)
                          : item.kind === 'session'
                            ? (item.session.title || `${t('Conversation')} ${item.session.id.slice(0, 8)}`)
                            : item.kind === 'workspace'
                              ? item.workspace.name
                              : item.file.name}
                      </span>
                      <span className="palette-item-meta max-w-[44%] truncate font-mono text-[11.5px] text-ink-muted">
                        {item.kind === 'action'
                          ? (item.action.hint ? t(item.action.hint) : '')
                          : item.kind === 'session'
                            ? (item.session.id === activeSessionId ? t('Current') : '')
                            : item.kind === 'workspace'
                              ? (item.workspace.active ? t('Active') : item.workspace.path)
                              : item.file.path}
                      </span>
                      {active && <CornerDownLeft size={13} className="palette-enter-icon shrink-0 text-ink-muted" aria-hidden="true" />}
                    </button>
                  )
                })}
              </div>
            ))
          )}
        </div>
      </section>
    </div>
  )
}

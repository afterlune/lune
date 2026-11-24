import { useEffect, useMemo, useRef, useState } from 'react'
import { CornerDownLeft, File, Folder, MessageSquare, Search, Settings, Sparkles } from 'lucide-react'
import { useTranslate } from './language'
import type { SessionSummary } from './harness-types'
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
  const t = useTranslate()
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
      className="command-palette-backdrop"
      onMouseDown={(event) => {
        if (event.target === event.currentTarget) onClose()
      }}
    >
      <section
        className="command-palette"
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
        <div className="palette-input-row">
          <Search size={16} aria-hidden="true" />
          <input
            ref={inputRef}
            type="text"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder={t('Jump to conversation, file, workspace, or setting…')}
            aria-label={t('Quick switcher')}
          />
          <kbd className="palette-kbd">Esc</kbd>
        </div>

        <div className="palette-list" ref={listRef} role="listbox">
          {sections.length === 0 ? (
            <div className="palette-empty">
              {filesLoading ? t('Indexing workspace files…') : t('No matching results.')}
            </div>
          ) : (
            sections.map((section) => (
              <div className="palette-group" key={section.title}>
                <div className="palette-group-label">{t(section.title)}</div>
                {section.items.map(({ item, index }) => {
                  const active = index === selectedIndex
                  return (
                    <button
                      key={item.key}
                      type="button"
                      role="option"
                      aria-selected={active}
                      className={`palette-item${active ? ' active' : ''}`}
                      onMouseEnter={() => setSelectedIndex(index)}
                      onClick={() => runItem(item)}
                    >
                      <span className="palette-item-icon" aria-hidden="true">
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
                      <span className="palette-item-title">
                        {item.kind === 'action'
                          ? t(item.action.label)
                          : item.kind === 'session'
                            ? (item.session.title || `${t('Conversation')} ${item.session.id.slice(0, 8)}`)
                            : item.kind === 'workspace'
                              ? item.workspace.name
                              : item.file.name}
                      </span>
                      <span className="palette-item-meta">
                        {item.kind === 'action'
                          ? (item.action.hint ? t(item.action.hint) : '')
                          : item.kind === 'session'
                            ? (item.session.id === activeSessionId ? t('Current') : '')
                            : item.kind === 'workspace'
                              ? (item.workspace.active ? t('Active') : item.workspace.path)
                              : item.file.path}
                      </span>
                      {active && <CornerDownLeft size={13} className="palette-enter-icon" aria-hidden="true" />}
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

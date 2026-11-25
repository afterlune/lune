import { useEffect, useRef, useState } from 'react'
import { AtSign, ChevronDown, ChevronRight, File, Folder, LockKeyhole, RefreshCw, Search, X } from 'lucide-react'
import { MarkdownContent } from './markdown'
import { useTranslation } from 'react-i18next'
import type { RuntimeClient } from './runtime'
import { btnIcon, btnPlain, btnSubtleIcon, cn } from './ui'

interface DirectoryItem { name: string; kind: 'directory' | 'file' }
interface DirectoryPage { items: DirectoryItem[]; more: boolean; nextOffset: number }
export interface WorkspaceInfo { name: string; path: string }

export function WorkspaceFiles({
  client,
  workspace,
  visible,
  close,
  initialFile,
  onInsertPath,
  embedded = false,
}: {
  client: RuntimeClient
  workspace: WorkspaceInfo | null
  visible: boolean
  close?: () => void
  initialFile?: string
  onInsertPath?: (path: string) => void
  embedded?: boolean
}) {
  const { t } = useTranslation()
  const [file, setFile] = useState('')
  const [treeVisible, setTreeVisible] = useState(false)
  const [filter, setFilter] = useState('')
  const [treeRevision, setTreeRevision] = useState(0)
  const [preview, setPreview] = useState<{ text: string }>()
  const [error, setError] = useState('')
  const [version, setVersion] = useState(0)
  const previewRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    setFile('')
    setPreview(undefined)
    setError('')
    setFilter('')
    setTreeVisible(false)
    setTreeRevision((v) => v + 1)
  }, [workspace?.path])

  useEffect(() => {
    if (initialFile) {
      setFile(initialFile)
      setTreeVisible(false)
    }
  }, [initialFile])

  useEffect(() => {
    setError('')
    setPreview(undefined)
    if (!file) return
    let current = true
    void client.request<{ text: string }>('workspace.readFile', { path: file }).then(
      value => { if (current) setPreview(value) },
      (err: Error) => { if (current) setError(err.message) },
    )
    return () => { current = false }
  }, [client, file, version])

  useEffect(() => {
    if (visible && file) previewRef.current?.focus()
  }, [visible, file])

  function open(path: string) {
    setFile(path)
    setTreeVisible(false)
  }

  const parts = file.split('/')
  const content = (
    <>
      <header className={cn(
        'files-header flex items-center gap-2 border-b border-line-subtle px-3.5',
        embedded ? 'h-[42px] bg-sidebar' : 'h-[52px] bg-canvas',
      )}>
        <button
          className={cn(btnPlain, 'flex-none gap-[7px] aria-[pressed=true]:bg-active aria-[pressed=true]:text-ink')}
          aria-pressed={!file}
          onClick={() => setFile('')}
        >
          <Folder size={15} />
          <span>{t('Files')}</span>
        </button>
        {file && (
          <span className="files-tab min-w-0 truncate rounded-[7px] bg-muted px-2.5 py-1 font-mono text-[12px] text-ink" title={file}>
            {parts.at(-1)}
          </span>
        )}
        <span className="files-spacer flex-1" />
        {file && onInsertPath && (
          <button
            type="button"
            className={cn('icon-button', btnIcon)}
            aria-label={t('Reference in prompt')}
            title={t('Reference in prompt')}
            onClick={() => onInsertPath(file)}
          >
            <AtSign size={15} />
          </button>
        )}
        <button
          type="button"
          className={cn('icon-button', btnIcon)}
          aria-label={t('Refresh')}
          title={t('Refresh')}
          onClick={() => {
            setTreeRevision((v) => v + 1)
            if (file) setVersion((v) => v + 1)
          }}
        >
          <RefreshCw size={14} />
        </button>
        {file && (
          <button
            className={cn('icon-button', btnIcon)}
            aria-expanded={treeVisible}
            aria-label={t(treeVisible ? 'Hide file tree' : 'Show file tree')}
            title={t(treeVisible ? 'Hide file tree' : 'Show file tree')}
            onClick={() => setTreeVisible(!treeVisible)}
          >
            <Folder size={15} />
          </button>
        )}
        {!embedded && close && (
          <button className={cn('icon-button', btnIcon)} aria-label={t('Close files')} title={t('Close files')} onClick={close}>
            <X size={16} />
          </button>
        )}
      </header>
      <div className="files-path flex min-h-[36px] items-center justify-between gap-2 border-b border-line-subtle px-3.5 font-mono text-[11.5px] text-ink-muted" title={workspace?.path}>
        <span className="min-w-0 truncate">{[workspace?.name, file].filter(Boolean).join(' / ') || '/'}</span>
        {file && <span className="file-readonly inline-flex items-center gap-1 whitespace-nowrap"><LockKeyhole size={12} />{t('Read only')}</span>}
      </div>
      {(!file || treeVisible) && (
        <div className="files-filter flex items-center gap-[7px] border-b border-line-subtle px-3 py-1.5 text-ink-muted">
          <Search size={13} aria-hidden="true" />
          <input
            className="min-w-0 flex-1 border-0 bg-transparent text-[12.5px] text-ink outline-none placeholder:text-ink-faint"
            value={filter}
            onChange={(event) => setFilter(event.target.value)}
            placeholder={t('Filter loaded files…')}
            aria-label={t('Filter loaded files…')}
          />
          {filter && (
            <button type="button" className={btnSubtleIcon} aria-label={t('Dismiss')} onClick={() => setFilter('')}>
              <X size={12} />
            </button>
          )}
        </div>
      )}
      <div className={cn('files-body relative min-h-0 flex-1', file && treeVisible ? 'split grid grid-cols-[minmax(0,1fr)_minmax(160px,40%)]' : 'flex flex-col')}>
        {file && (
          <div ref={previewRef} className="file-preview min-h-0 min-w-0 flex-1 overflow-auto bg-canvas px-[22px] py-5" tabIndex={0}>
            {error ? (
              <div role="alert" className="file-message px-2 py-1.5 text-[13px] text-ink-muted">
                {t(error)}{' '}
                <button className={btnPlain} onClick={() => setVersion(value => value + 1)}>{t('Retry')}</button>
              </div>
            ) : preview ? (
              /\.m(?:d|arkdown)$/i.test(file) ? (
                <div className="markdown-body"><MarkdownContent text={preview.text} safePreview /></div>
              ) : (
                <pre className="m-0 min-w-full w-max font-mono text-[13px] leading-[1.65] text-ink">{preview.text}</pre>
              )
            ) : (
              <p className="file-message px-2 py-1.5 text-[13px] text-ink-muted" role="status">{t('Loading…')}</p>
            )}
          </div>
        )}
        <div
          className={cn('workspace-tree min-h-0 min-w-0 overflow-auto p-2.5', file && treeVisible && 'border-l border-line-subtle')}
          hidden={Boolean(file && !treeVisible)}
        >
          <Directory
            key={workspace?.path || 'root'}
            client={client}
            path=""
            active={visible && (!file || treeVisible)}
            selected={file}
            filter={filter.trim().toLowerCase()}
            revision={treeRevision}
            open={open}
            onInsertPath={onInsertPath}
          />
        </div>
      </div>
    </>
  )

  if (embedded) {
    return (
      <div
        className="workspace-files embedded z-[3] flex h-full min-h-0 min-w-0 flex-1 flex-col bg-sidebar"
        onKeyDown={(event) => { if (event.key === 'Escape' && close) close() }}
      >
        {content}
      </div>
    )
  }

  return (
    <aside
      hidden={!visible}
      className="workspace-files z-[3] flex min-h-0 min-w-0 flex-col border-l border-line-subtle bg-sidebar"
      aria-label={t('Files')}
      onKeyDown={event => { if (event.key === 'Escape' && close) close() }}
    >
      {content}
    </aside>
  )
}

function Directory({
  client,
  path,
  active,
  selected,
  filter,
  revision,
  open,
  onInsertPath,
  nested = false,
}: {
  client: RuntimeClient
  path: string
  active: boolean
  selected: string
  filter: string
  revision: number
  open: (path: string) => void
  onInsertPath?: (path: string) => void
  nested?: boolean
}) {
  const { t } = useTranslation()
  const [page, setPage] = useState<DirectoryPage>()
  const [expanded, setExpanded] = useState<Record<string, boolean>>({})
  const [offset, setOffset] = useState(0)
  const [version, setVersion] = useState(0)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    if (!active) return
    let current = true
    setBusy(true)
    setError('')
    void client.request<DirectoryPage>('workspace.listDirectory', { path, offset }).then(
      value => {
        if (!current) return
        setPage(previous => offset && previous ? {
          ...value, items: [...new Map([...previous.items, ...value.items].map(item => [item.name, item])).values()],
        } : value)
        setBusy(false)
      },
      (err: Error) => { if (current) { setError(err.message); setBusy(false) } },
    )
    return () => { current = false }
  }, [client, path, active, offset, version, revision])

  const items = (page?.items ?? []).filter((item) => !filter || item.kind === 'directory' || item.name.toLowerCase().includes(filter))

  return (
    <ul className={cn('directory-level m-0 list-none p-0', nested && 'pl-4')} aria-busy={busy}>
      {items.map(item => {
        const target = path ? path + '/' + item.name : item.name
        const folder = item.kind === 'directory'
        const isExpanded = Boolean(expanded[item.name])
        const Chevron = isExpanded ? ChevronDown : ChevronRight
        const isSelected = target === selected
        return (
          <li key={item.name}>
            <div className="tree-row group/row flex items-center gap-0.5">
              <button
                className={cn(
                  'flex min-h-[31px] min-w-0 flex-1 cursor-pointer items-center gap-2 rounded-[7px] border-0 px-2 py-1 text-left text-[13px] text-ink transition-[background] duration-120 hover:bg-hover',
                  isSelected ? 'selected bg-active font-medium' : 'bg-transparent',
                )}
                aria-expanded={folder ? isExpanded : undefined}
                aria-current={isSelected ? 'true' : undefined}
                onClick={() => folder ? setExpanded(value => ({ ...value, [item.name]: !isExpanded })) : open(target)}
              >
                {folder
                  ? <><Chevron size={13} className="shrink-0" /><Folder size={15} className="tree-folder shrink-0 text-ink-secondary" /></>
                  : <File size={15} className="tree-file ml-[13px] shrink-0 text-ink-muted" />}
                <span className="min-w-0 flex-1 truncate" title={item.name}>{item.name}</span>
              </button>
              {!folder && onInsertPath && (
                <button
                  type="button"
                  className="tree-ref-btn inline-grid h-6 w-6 cursor-pointer place-items-center rounded-[6px] border-0 bg-transparent text-ink-muted opacity-0 transition-[opacity,background,color] duration-120 hover:bg-hover hover:text-ink group-hover/row:opacity-100 group-focus-within/row:opacity-100"
                  title={t('Reference in prompt')}
                  aria-label={`${t('Reference in prompt')}: ${target}`}
                  onClick={() => onInsertPath(target)}
                >
                  <AtSign size={12} />
                </button>
              )}
            </div>
            {folder && Object.hasOwn(expanded, item.name) && (
              <div hidden={!isExpanded}>
                <Directory
                  client={client}
                  path={target}
                  active={active && isExpanded}
                  selected={selected}
                  filter={filter}
                  revision={revision}
                  open={open}
                  onInsertPath={onInsertPath}
                  nested
                />
              </div>
            )}
          </li>
        )
      })}
      {error && <li className="file-message px-2 py-1.5 text-[13px] text-ink-muted" role="alert">{t(error)} <button className={btnPlain} onClick={() => setVersion(value => value + 1)}>{t('Retry')}</button></li>}
      {busy && !page && <li className="file-message px-2 py-1.5 text-[13px] text-ink-muted">{t('Loading…')}</li>}
      {page?.items.length === 0 && !busy && !error && <li className="file-message px-2 py-1.5 text-[13px] text-ink-muted">{t('Empty folder.')}</li>}
      {page?.more && <li><button className={btnPlain} disabled={busy || Boolean(error)} onClick={() => setOffset(page.nextOffset)}>{t('Load more')}</button></li>}
    </ul>
  )
}

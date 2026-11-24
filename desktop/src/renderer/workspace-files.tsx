import { useEffect, useRef, useState } from 'react'
import { AtSign, ChevronDown, ChevronRight, File, Folder, LockKeyhole, RefreshCw, Search, X } from 'lucide-react'
import { MarkdownContent } from './markdown'
import { useTranslate } from './language'
import type { RuntimeClient } from './runtime'

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
  const t = useTranslate()
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
      <header className="files-header">
        <button className="plain-button" aria-pressed={!file} onClick={() => setFile('')}>
          <Folder size={15} />
          <span>{t('Files')}</span>
        </button>
        {file && <span className="files-tab" title={file}>{parts.at(-1)}</span>}
        <span className="files-spacer" />
        {file && onInsertPath && (
          <button
            type="button"
            className="icon-button"
            aria-label={t('Reference in prompt')}
            title={t('Reference in prompt')}
            onClick={() => onInsertPath(file)}
          >
            <AtSign size={15} />
          </button>
        )}
        <button
          type="button"
          className="icon-button"
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
            className="icon-button"
            aria-expanded={treeVisible}
            aria-label={t(treeVisible ? 'Hide file tree' : 'Show file tree')}
            title={t(treeVisible ? 'Hide file tree' : 'Show file tree')}
            onClick={() => setTreeVisible(!treeVisible)}
          >
            <Folder size={15} />
          </button>
        )}
        {!embedded && close && (
          <button className="icon-button" aria-label={t('Close files')} title={t('Close files')} onClick={close}>
            <X size={16} />
          </button>
        )}
      </header>
      <div className="files-path" title={workspace?.path}>
        <span>{[workspace?.name, file].filter(Boolean).join(' / ') || '/'}</span>
        {file && <span className="file-readonly"><LockKeyhole size={12} />{t('Read only')}</span>}
      </div>
      {(!file || treeVisible) && (
        <div className="files-filter">
          <Search size={13} aria-hidden="true" />
          <input
            value={filter}
            onChange={(event) => setFilter(event.target.value)}
            placeholder={t('Filter loaded files…')}
            aria-label={t('Filter loaded files…')}
          />
          {filter && (
            <button type="button" className="subtle-icon" aria-label={t('Dismiss')} onClick={() => setFilter('')}>
              <X size={12} />
            </button>
          )}
        </div>
      )}
      <div className={file && treeVisible ? 'files-body split' : 'files-body'}>
        {file && (
          <div ref={previewRef} className="file-preview" tabIndex={0}>
            {error ? (
              <div role="alert" className="file-message">
                {t(error)}{' '}
                <button className="plain-button" onClick={() => setVersion(value => value + 1)}>{t('Retry')}</button>
              </div>
            ) : preview ? (
              /\.m(?:d|arkdown)$/i.test(file) ? (
                <div className="markdown-body"><MarkdownContent text={preview.text} safePreview /></div>
              ) : (
                <pre>{preview.text}</pre>
              )
            ) : (
              <p className="file-message" role="status">{t('Loading…')}</p>
            )}
          </div>
        )}
        <div className="workspace-tree" hidden={Boolean(file && !treeVisible)}>
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
      <div className="workspace-files embedded" onKeyDown={(event) => { if (event.key === 'Escape' && close) close() }}>
        {content}
      </div>
    )
  }

  return (
    <aside
      hidden={!visible}
      className="workspace-files"
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
}: {
  client: RuntimeClient
  path: string
  active: boolean
  selected: string
  filter: string
  revision: number
  open: (path: string) => void
  onInsertPath?: (path: string) => void
}) {
  const t = useTranslate()
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
    <ul className="directory-level" aria-busy={busy}>
      {items.map(item => {
        const target = path ? path + '/' + item.name : item.name
        const folder = item.kind === 'directory'
        const isExpanded = Boolean(expanded[item.name])
        const Chevron = isExpanded ? ChevronDown : ChevronRight
        return (
          <li key={item.name}>
            <div className="tree-row">
              <button
                className={target === selected ? 'selected' : ''}
                aria-expanded={folder ? isExpanded : undefined}
                aria-current={target === selected ? 'true' : undefined}
                onClick={() => folder ? setExpanded(value => ({ ...value, [item.name]: !isExpanded })) : open(target)}
              >
                {folder ? <><Chevron size={13} /><Folder size={15} className="tree-folder" /></> : <File size={15} className="tree-file" />}
                <span title={item.name}>{item.name}</span>
              </button>
              {!folder && onInsertPath && (
                <button
                  type="button"
                  className="tree-ref-btn"
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
                />
              </div>
            )}
          </li>
        )
      })}
      {error && <li className="file-message" role="alert">{t(error)} <button className="plain-button" onClick={() => setVersion(value => value + 1)}>{t('Retry')}</button></li>}
      {busy && !page && <li className="file-message">{t('Loading…')}</li>}
      {page?.items.length === 0 && !busy && !error && <li className="file-message">{t('Empty folder.')}</li>}
      {page?.more && <li><button className="plain-button" disabled={busy || Boolean(error)} onClick={() => setOffset(page.nextOffset)}>{t('Load more')}</button></li>}
    </ul>
  )
}

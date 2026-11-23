import { useEffect, useRef, useState } from 'react'
import { ChevronDown, ChevronRight, File, Folder, LockKeyhole, X } from 'lucide-react'
import ReactMarkdown from 'react-markdown'
import { useTranslate } from './language'
import type { RuntimeClient } from './runtime'

interface DirectoryItem { name: string; kind: 'directory' | 'file' }
interface DirectoryPage { items: DirectoryItem[]; more: boolean; nextOffset: number }
export interface WorkspaceInfo { name: string; path: string }

export function WorkspaceFiles({ client, workspace, visible, close }: {
  client: RuntimeClient; workspace: WorkspaceInfo | null; visible: boolean; close: () => void
}) {
  const t = useTranslate()
  const [file, setFile] = useState('')
  const [treeVisible, setTreeVisible] = useState(false)
  const [preview, setPreview] = useState<{ text: string }>()
  const [error, setError] = useState('')
  const [version, setVersion] = useState(0)
  const previewRef = useRef<HTMLDivElement>(null)
  useEffect(() => {
    setError('')
    setPreview(undefined)
    if (!file) return
    let current = true
    void client.request<{ text: string }>('workspace.readFile', { path: file }).then(
      value => { if (current) setPreview(value) },
      (error: Error) => { if (current) setError(error.message) },
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
  return <aside hidden={!visible} className="workspace-files" aria-label={t('Files')} onKeyDown={event => {
    if (event.key === 'Escape') close()
  }}>
    <header className="files-header">
      <button className="plain-button" aria-pressed={!file} onClick={() => setFile('')}><Folder size={16} />{t('Files')}</button>
      {file && <span className="files-tab" title={file}>{parts.at(-1)}</span>}
      <span className="files-spacer" />
      {file && <button className="icon-button" aria-expanded={treeVisible} aria-label={t(treeVisible ? 'Hide file tree' : 'Show file tree')}
        title={t(treeVisible ? 'Hide file tree' : 'Show file tree')} onClick={() => setTreeVisible(!treeVisible)}><Folder size={16} /></button>}
      <button className="icon-button" aria-label={t('Close files')} title={t('Close files')} onClick={close}><X size={16} /></button>
    </header>
    <div className="files-path" title={workspace?.path}>
      <span>{[workspace?.name, file].filter(Boolean).join(' / ')}</span>
      {file && <span className="file-readonly"><LockKeyhole size={12} />{t('Read only')}</span>}
    </div>
    <div className={file && treeVisible ? 'files-body split' : 'files-body'}>
      {file && <div ref={previewRef} className="file-preview" tabIndex={0}>
        {error ? <div role="alert" className="file-message">{t(error)} <button className="plain-button" onClick={() => setVersion(value => value + 1)}>{t('Retry')}</button></div>
          : preview ? /\.m(?:d|arkdown)$/i.test(file)
            ? <div className="markdown-body"><ReactMarkdown skipHtml components={{
                img: ({ alt }) => <span>{alt}</span>,
                a: ({ children }) => <span>{children}</span>,
              }}>{preview.text}</ReactMarkdown></div>
            : <pre>{preview.text}</pre>
          : <p className="file-message" role="status">{t('Loading…')}</p>}
      </div>}
      <div className="workspace-tree" hidden={Boolean(file && !treeVisible)}>
        <Directory client={client} path="" active={visible && (!file || treeVisible)} selected={file} open={open} />
      </div>
    </div>
  </aside>
}

function Directory({ client, path, active, selected, open }: {
  client: RuntimeClient; path: string; active: boolean; selected: string; open: (path: string) => void
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
      (error: Error) => { if (current) { setError(error.message); setBusy(false) } },
    )
    return () => { current = false }
  }, [client, path, active, offset, version])

  return <ul className="directory-level" aria-busy={busy}>
    {page?.items.map(item => {
      const target = path ? path + '/' + item.name : item.name
      const folder = item.kind === 'directory'
      const isExpanded = Boolean(expanded[item.name])
      const Chevron = isExpanded ? ChevronDown : ChevronRight
      return <li key={item.name}>
        <button className={target === selected ? 'selected' : ''} aria-expanded={folder ? isExpanded : undefined}
          aria-current={target === selected ? 'true' : undefined}
          onClick={() => folder ? setExpanded(value => ({ ...value, [item.name]: !isExpanded })) : open(target)}>
          {folder ? <><Chevron size={13} /><Folder size={15} className="tree-folder" /></> : <File size={15} className="tree-file" />}
          <span title={item.name}>{item.name}</span>
        </button>
        {folder && Object.hasOwn(expanded, item.name) && <div hidden={!isExpanded}>
          <Directory client={client} path={target} active={active && isExpanded} selected={selected} open={open} />
        </div>}
      </li>
    })}
    {error && <li className="file-message" role="alert">{t(error)} <button className="plain-button" onClick={() => setVersion(value => value + 1)}>{t('Retry')}</button></li>}
    {busy && !page && <li className="file-message">{t('Loading…')}</li>}
    {page?.items.length === 0 && !busy && !error && <li className="file-message">{t('Empty folder.')}</li>}
    {page?.more && <li><button className="plain-button" disabled={busy || Boolean(error)} onClick={() => setOffset(page.nextOffset)}>{t('Load more')}</button></li>}
  </ul>
}

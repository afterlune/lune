import { useCallback, useEffect, useRef, useState } from 'react'
import { Terminal as XTerm, type ITheme } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import { Plus, RotateCw, Trash2, X } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { cn, btnPlain, btnSubtleIcon } from './ui'
import { createTerminalTransport, type TerminalTarget, type TerminalTransport } from './terminal-transport'

interface TerminalTabItem {
  id: string
  num: number
  runKey: number
  status: 'connecting' | 'running' | 'exited' | 'error'
  exitCode?: number
  error?: string
}

function readXtermTheme(): ITheme {
  const dark =
    document.documentElement.dataset.theme === 'dark' ||
    (document.documentElement.dataset.theme === 'system' && window.matchMedia?.('(prefers-color-scheme: dark)').matches)
  const background = dark ? '#1f2023' : '#ffffff'
  const foreground = dark ? '#dcdce0' : '#0d0d0d'
  const cursor = foreground
  const selectionBackground = dark ? 'rgba(220, 220, 224, 0.22)' : 'rgba(13, 13, 13, 0.14)'

  if (dark) {
    return {
      background,
      foreground,
      cursor,
      cursorAccent: background,
      selectionBackground,
      black: '#2b2d33',
      red: '#e07b7b',
      green: '#6fb596',
      yellow: '#c8a774',
      blue: '#82aaff',
      magenta: '#c792ea',
      cyan: '#89ddff',
      white: '#dcdce0',
      brightBlack: '#686872',
      brightRed: '#eb9696',
      brightGreen: '#8ecab0',
      brightYellow: '#dcc090',
      brightBlue: '#9ec0ff',
      brightMagenta: '#d8b0f0',
      brightCyan: '#a8e7ff',
      brightWhite: '#ececf0',
    }
  }
  return {
    background,
    foreground,
    cursor,
    cursorAccent: background,
    selectionBackground,
    black: '#18181b',
    red: '#b42318',
    green: '#2e6f52',
    yellow: '#8a6732',
    blue: '#1d4ed8',
    magenta: '#7e22ce',
    cyan: '#0e7490',
    white: '#e4e4e7',
    brightBlack: '#52525b',
    brightRed: '#d92d20',
    brightGreen: '#3b8d68',
    brightYellow: '#a17a3d',
    brightBlue: '#2563eb',
    brightMagenta: '#9333ea',
    brightCyan: '#0891b2',
    brightWhite: '#f4f4f5',
  }
}

function TerminalInstance({
  tab,
  target,
  active,
  clearToken,
  onStatusChange,
}: {
  tab: TerminalTabItem
  target: TerminalTarget
  active: boolean
  clearToken: number
  onStatusChange: (id: string, patch: Partial<TerminalTabItem>) => void
}) {
  const hostRef = useRef<HTMLDivElement | null>(null)
  const termRef = useRef<XTerm | null>(null)
  const fitRef = useRef<FitAddon | null>(null)
  const transportRef = useRef<TerminalTransport | null>(null)
  const onStatusRef = useRef(onStatusChange)
  onStatusRef.current = onStatusChange

  const syncSize = useCallback(() => {
    const host = hostRef.current
    const term = termRef.current
    const fit = fitRef.current
    if (!host || !term || !fit) return
    if (host.clientWidth < 40 || host.clientHeight < 40) return
    try {
      fit.fit()
      transportRef.current?.resize(term.cols, term.rows)
    } catch { /* ignore fit errors during hidden transitions */ }
  }, [])

  useEffect(() => {
    if (clearToken > 0) {
      termRef.current?.clear()
    }
  }, [clearToken])

  useEffect(() => {
    const host = hostRef.current
    if (!host) return

    let disposed = false
    const term = new XTerm({
      cursorBlink: true,
      cursorStyle: 'block',
      scrollback: 5000,
      fontSize: 13,
      lineHeight: 1.35,
      fontFamily: "ui-monospace, 'SFMono-Regular', 'JetBrains Mono', 'Cascadia Code', Menlo, Consolas, monospace",
      theme: readXtermTheme(),
      allowProposedApi: false,
    })
    const fit = new FitAddon()
    term.loadAddon(fit)
    term.open(host)
    termRef.current = term
    fitRef.current = fit

    term.attachCustomKeyEventHandler((event) => {
      if (event.type !== 'keydown') return true
      if (event.ctrlKey && event.shiftKey && event.key.toLowerCase() === 'c') {
        const selected = term.getSelection()
        if (selected && navigator.clipboard?.writeText) {
          void navigator.clipboard.writeText(selected).catch(() => undefined)
        }
        return false
      }
      if (event.ctrlKey && event.shiftKey && event.key.toLowerCase() === 'v') {
        if (navigator.clipboard?.readText) {
          void navigator.clipboard.readText().then((text) => {
            if (text && !disposed) transportRef.current?.write(text)
          }).catch(() => undefined)
        }
        return false
      }
      return true
    })

    try {
      if (host.clientWidth >= 40 && host.clientHeight >= 40) fit.fit()
    } catch { /* ignore initial zero-size */ }

    const initialCols = Math.max(20, term.cols || 80)
    const initialRows = Math.max(5, term.rows || 24)
    onStatusRef.current(tab.id, { status: 'connecting', exitCode: undefined, error: undefined })

    const inputDispose = term.onData((data) => {
      transportRef.current?.write(data)
    })

    const observer = new ResizeObserver(() => {
      syncSize()
    })
    observer.observe(host)

    const updateTheme = () => {
      term.options.theme = readXtermTheme()
    }
    const attrObserver = new MutationObserver(updateTheme)
    attrObserver.observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] })
    const mq = window.matchMedia?.('(prefers-color-scheme: dark)')
    mq?.addEventListener?.('change', updateTheme)
    updateTheme()

    void createTerminalTransport(target, initialCols, initialRows)
      .then((transport) => {
        if (disposed) {
          transport.close()
          return
        }
        transportRef.current = transport
        onStatusRef.current(tab.id, { status: 'running' })
        transport.onData((bytes) => {
          if (!disposed) term.write(bytes)
        })
        transport.onExit((code) => {
          if (disposed) return
          transportRef.current = null
          onStatusRef.current(tab.id, { status: 'exited', exitCode: code })
        })
        transport.onError((message) => {
          if (disposed) return
          onStatusRef.current(tab.id, { status: 'error', error: message })
        })
        syncSize()
        term.focus()
      })
      .catch((error: unknown) => {
        if (disposed) return
        const msg = error instanceof Error ? error.message : String(error)
        onStatusRef.current(tab.id, { status: 'error', error: msg })
      })

    return () => {
      disposed = true
      observer.disconnect()
      attrObserver.disconnect()
      mq?.removeEventListener?.('change', updateTheme)
      inputDispose.dispose()
      transportRef.current?.close()
      transportRef.current = null
      fitRef.current = null
      termRef.current = null
      term.dispose()
    }
  }, [tab.id, tab.runKey, target, syncSize])

  useEffect(() => {
    if (!active) return
    const id = window.requestAnimationFrame(() => {
      syncSize()
      termRef.current?.focus()
    })
    return () => window.cancelAnimationFrame(id)
  }, [active, syncSize])

  return (
    <div
      className={cn(
        'terminal-instance relative min-h-0 flex-1 overflow-hidden px-2.5 py-2',
        active ? 'flex flex-col' : 'hidden',
      )}
      onMouseDown={() => termRef.current?.focus()}
    >
      <div ref={hostRef} className="terminal-xterm-host h-full w-full min-h-0 flex-1" />
    </div>
  )
}

export function TerminalPanel({
  target,
  visible,
}: {
  target: TerminalTarget
  visible: boolean
}) {
  const { t } = useTranslation()
  const nextNumRef = useRef(2)
  const [panelState, setPanelState] = useState<{ tabs: TerminalTabItem[]; activeId: string }>({
    tabs: [{ id: 'term-1', num: 1, runKey: 1, status: 'connecting' }],
    activeId: 'term-1',
  })
  const { tabs, activeId } = panelState
  const panelStateRef = useRef(panelState)
  panelStateRef.current = panelState
  const [clearTokens, setClearTokens] = useState<Record<string, number>>({})

  const handleStatusChange = useCallback((id: string, patch: Partial<TerminalTabItem>) => {
    setPanelState((prev) => ({
      ...prev,
      tabs: prev.tabs.map((item) => (item.id === id ? { ...item, ...patch } : item)),
    }))
  }, [])

  const addTab = () => {
    const num = nextNumRef.current++
    const id = `term-${num}-${Date.now()}`
    setPanelState((prev) => ({
      tabs: [...prev.tabs, { id, num, runKey: 1, status: 'connecting' }],
      activeId: id,
    }))
  }

  const closeTab = (id: string) => {
    const current = panelStateRef.current
    if (current.tabs.length <= 1) {
      const num = nextNumRef.current++
      const nextId = `term-${num}-${Date.now()}`
      setPanelState({
        tabs: [{ id: nextId, num, runKey: 1, status: 'connecting' }],
        activeId: nextId,
      })
      return
    }
    const idx = current.tabs.findIndex((item) => item.id === id)
    if (idx < 0) return
    const filtered = current.tabs.filter((item) => item.id !== id)
    const fallback = filtered[Math.max(0, idx - 1)] ?? filtered[0]
    setPanelState({
      tabs: filtered,
      activeId: current.activeId === id ? (fallback?.id ?? current.activeId) : current.activeId,
    })
  }

  const activeTab = tabs.find((item) => item.id === activeId) ?? tabs[0]

  const restartActive = () => {
    if (!activeTab) return
    setPanelState((prev) => ({
      ...prev,
      tabs: prev.tabs.map((item) => (
        item.id === activeTab.id
          ? { ...item, runKey: item.runKey + 1, status: 'connecting', exitCode: undefined, error: undefined }
          : item
      )),
    }))
  }

  const clearActive = () => {
    if (!activeTab) return
    setClearTokens((prev) => ({ ...prev, [activeTab.id]: (prev[activeTab.id] ?? 0) + 1 }))
  }

  return (
    <div className="terminal-panel flex h-full min-h-0 flex-1 flex-col bg-canvas">
      <div className="terminal-toolbar flex h-9 shrink-0 items-center gap-1 border-b border-line-subtle bg-subtle px-2">
        <div className="terminal-tabs flex min-w-0 flex-1 items-center gap-1 overflow-x-auto" role="tablist">
          {tabs.map((item) => {
            const isActive = activeTab?.id === item.id
            const dotCls =
              item.status === 'running'
                ? 'bg-ok'
                : item.status === 'error'
                  ? 'bg-danger'
                  : item.status === 'exited'
                    ? 'bg-ink-faint'
                    : 'bg-warn'
            return (
              <div
                key={item.id}
                className={cn(
                  'terminal-tab group inline-flex h-[26px] shrink-0 items-center gap-1.5 rounded-[7px] px-2 text-[12px] transition-[background,color] duration-120',
                  isActive ? 'bg-canvas font-medium text-ink shadow-[inset_0_0_0_1px_var(--border-subtle)]' : 'text-ink-secondary hover:bg-hover hover:text-ink',
                )}
              >
                <button
                  type="button"
                  role="tab"
                  aria-selected={isActive}
                  className="inline-flex cursor-pointer items-center gap-1.5 border-0 bg-transparent p-0 text-inherit"
                  onClick={() => setPanelState((prev) => ({ ...prev, activeId: item.id }))}
                >
                  <span className={cn('h-1.5 w-1.5 rounded-full', dotCls)} aria-hidden="true" />
                  <span>{t('Terminal')} {item.num}</span>
                </button>
                <button
                  type="button"
                  className={cn(btnSubtleIcon, 'h-4 w-4 text-ink-muted hover:text-ink')}
                  aria-label={`${t('Close terminal')} ${item.num}`}
                  title={t('Close terminal')}
                  onClick={() => closeTab(item.id)}
                >
                  <X size={11} />
                </button>
              </div>
            )
          })}
          <button
            type="button"
            className={cn(btnSubtleIcon, 'h-6 w-6 shrink-0')}
            aria-label={t('New terminal')}
            title={t('New terminal')}
            onClick={addTab}
          >
            <Plus size={14} />
          </button>
        </div>

        <div className="terminal-actions flex shrink-0 items-center gap-1">
          {activeTab?.status === 'exited' && (
            <button type="button" className={cn(btnPlain, 'h-6 min-h-0 px-2 text-[11.5px]')} onClick={restartActive}>
              <RotateCw size={12} aria-hidden="true" />
              <span>{t('Restart terminal')}</span>
            </button>
          )}
          <button
            type="button"
            className={cn(btnSubtleIcon, 'h-6 w-6')}
            aria-label={t('Clear terminal')}
            title={t('Clear terminal')}
            onClick={clearActive}
          >
            <Trash2 size={13} />
          </button>
        </div>
      </div>

      {activeTab?.status === 'connecting' && (
        <div className="terminal-status border-b border-line-subtle bg-subtle px-3 py-1 text-[11.5px] text-ink-muted" role="status">
          {t('Connecting to terminal…')}
        </div>
      )}
      {activeTab?.status === 'exited' && (
        <div className="terminal-status flex items-center justify-between border-b border-line-subtle bg-subtle px-3 py-1 text-[11.5px] text-ink-secondary" role="status">
          <span>{t('Exited')} ({activeTab.exitCode ?? 0})</span>
          <button type="button" className="cursor-pointer border-0 bg-transparent text-[11.5px] font-medium text-ink underline" onClick={restartActive}>
            {t('Restart terminal')}
          </button>
        </div>
      )}
      {activeTab?.status === 'error' && (
        <div className="terminal-status flex items-center justify-between border-b border-danger-border bg-danger-bg px-3 py-1 text-[11.5px] text-danger" role="alert">
          <span className="truncate">{activeTab.error || t('Terminal connection failed.')}</span>
          <button type="button" className="cursor-pointer border-0 bg-transparent text-[11.5px] font-medium underline" onClick={restartActive}>
            {t('Restart terminal')}
          </button>
        </div>
      )}

      <div className="terminal-viewport relative flex min-h-0 flex-1 flex-col overflow-hidden">
        {tabs.map((item) => (
          <TerminalInstance
            key={`${item.id}:${item.runKey}`}
            tab={item}
            target={target}
            active={visible && activeTab?.id === item.id}
            clearToken={clearTokens[item.id] ?? 0}
            onStatusChange={handleStatusChange}
          />
        ))}
      </div>
    </div>
  )
}

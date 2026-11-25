import { useState } from 'react'
import { ChevronDown, Monitor, Moon, Sun } from 'lucide-react'
import { isThemePreference, windowColors, type ThemePreference } from '../shared/theme'
import { useTranslation } from 'react-i18next'

const storageKey = 'lune:appearance'
const systemDark = window.matchMedia('(prefers-color-scheme: dark)')

export async function loadTheme(): Promise<ThemePreference> {
  try {
    const stored = window.luneDesktop ? await window.luneDesktop.getTheme() : localStorage.getItem(storageKey)
    return isThemePreference(stored) ? stored : 'light'
  } catch {
    return 'light'
  }
}

export function applyTheme(preference: ThemePreference): void {
  document.documentElement.dataset.theme = preference
  updateThemeColor()
}

function updateThemeColor(): void {
  const theme = document.documentElement.dataset.theme
  const dark = theme === 'dark' || (theme !== 'light' && systemDark.matches)
  document.querySelector('meta[name="theme-color"]')?.setAttribute('content', dark ? windowColors.dark : windowColors.light)
}

systemDark.addEventListener('change', updateThemeColor)

export function ThemePicker({ value, onChange }: { value: ThemePreference; onChange: (value: ThemePreference) => void }) {
  const { t } = useTranslation()
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState(false)
  const Icon = value === 'system' ? Monitor : value === 'dark' ? Moon : Sun

  async function choose(next: string) {
    if (!isThemePreference(next)) return
    setError(false)
    if (window.luneDesktop) {
      setSaving(true)
      try {
        await window.luneDesktop.setTheme(next)
      } catch {
        setError(true)
        return
      } finally {
        setSaving(false)
      }
    } else {
      try { localStorage.setItem(storageKey, next) } catch { /* keep the choice for this page */ }
    }
    applyTheme(next)
    onChange(next)
  }

  return <div className="appearance relative flex">
    <label
      className="theme-picker inline-flex h-[32px] cursor-pointer items-center gap-1.5 rounded-[8px] border border-line bg-canvas px-2 text-ink-secondary transition-[border-color,background-color] duration-150 hover:border-line-strong focus-within:border-line-strong"
      title={t('Appearance')}
    >
      <Icon size={15} className="shrink-0" aria-hidden="true" />
      <select
        className="min-w-0 flex-1 appearance-none border-0 bg-transparent text-[12.5px] font-medium text-ink outline-none cursor-pointer"
        aria-label={t('Appearance')}
        value={value}
        disabled={saving}
        onChange={(event) => void choose(event.currentTarget.value)}
      >
        <option value="system">{t('System')}</option>
        <option value="light">{t('Light')}</option>
        <option value="dark">{t('Dark')}</option>
      </select>
      <ChevronDown size={13} className="select-chevron shrink-0 text-ink-muted pointer-events-none" aria-hidden="true" />
    </label>
    {error && (
      <span
        className="theme-error absolute top-[calc(100%+4px)] right-0 z-[6] w-max max-w-[220px] rounded-[7px] border border-danger-border bg-canvas px-2.5 py-1.5 text-[12px] text-danger"
        role="alert"
      >
        Could not save appearance.
      </span>
    )}
  </div>
}

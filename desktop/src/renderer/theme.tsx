import { useState } from 'react'
import { Monitor, Moon, Sun } from 'lucide-react'
import { isThemePreference, windowColors, type ThemePreference } from '../shared/theme'

const storageKey = 'lune:appearance'
const systemDark = window.matchMedia('(prefers-color-scheme: dark)')

export async function loadTheme(): Promise<ThemePreference> {
  try {
    const stored = window.luneDesktop ? await window.luneDesktop.getTheme() : localStorage.getItem(storageKey)
    return isThemePreference(stored) ? stored : 'system'
  } catch {
    return 'system'
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

  return <div className="appearance">
    <label className="theme-picker" title="Appearance">
      <Icon size={15} aria-hidden="true" />
      <select aria-label="Appearance" value={value} disabled={saving} onChange={(event) => void choose(event.currentTarget.value)}>
        <option value="system">System</option>
        <option value="light">Light</option>
        <option value="dark">Dark</option>
      </select>
    </label>
    {error && <span className="theme-error" role="alert">Could not save appearance.</span>}
  </div>
}

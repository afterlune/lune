import { useSyncExternalStore } from 'react'
import { useTranslation } from 'react-i18next'
import { ChevronDown, Languages } from 'lucide-react'
import {
  bindHostLanguage, getLanguageSaveFailed, normalizeLanguage,
  setAppLanguage, subscribeLanguageSaveFailed, type AppLanguage,
} from './i18n'

export type { AppLanguage as Language }

export function useHostLanguage() {
  return bindHostLanguage
}

export function useLanguage() {
  const { i18n } = useTranslation()
  const language = normalizeLanguage(i18n.resolvedLanguage ?? i18n.language)
  return { language, setLanguage: setAppLanguage }
}

export function LanguagePicker() {
  const { t } = useTranslation()
  const { language, setLanguage } = useLanguage()
  const saveFailed = useSyncExternalStore(subscribeLanguageSaveFailed, getLanguageSaveFailed)

  return (
    <label
      className="theme-picker web-language inline-flex h-[32px] cursor-pointer items-center gap-1.5 rounded-[8px] border border-line bg-canvas px-2 text-ink-secondary transition-[border-color,background-color] duration-150 hover:border-line-strong focus-within:border-line-strong"
      title={t(saveFailed ? 'Could not save interface language.' : 'Interface language')}
    >
      <Languages size={15} className="shrink-0" aria-hidden="true" />
      <select
        className="min-w-0 appearance-none border-0 bg-transparent text-[12.5px] font-medium text-ink outline-none cursor-pointer"
        aria-label={t('Interface language')}
        value={language}
        onChange={(event) => setLanguage(event.target.value as AppLanguage)}
      >
        <option value="zh" lang="zh-CN">简体中文</option>
        <option value="en" lang="en">English</option>
      </select>
      <ChevronDown size={13} className="select-chevron shrink-0 text-ink-muted pointer-events-none" aria-hidden="true" />
    </label>
  )
}

import i18n from 'i18next'
import { initReactI18next } from 'react-i18next'
import en from './locales/en.json'
import zh from './locales/zh.json'

export type AppLanguage = 'en' | 'zh'
type SaveLanguageFn = (language: AppLanguage) => Promise<unknown>

const PREFERENCE_KEY = 'lune:language'

let saveFn: SaveLanguageFn | null = null
let saveQueue = Promise.resolve()
let saveFailed = false
const saveFailedListeners = new Set<() => void>()

export function normalizeLanguage(raw?: string | null): AppLanguage {
  return raw?.toLowerCase().startsWith('zh') ? 'zh' : 'en'
}

function detectInitialLanguage(): AppLanguage {
  try {
    const saved = localStorage.getItem(PREFERENCE_KEY)
    if (saved === 'zh' || saved === 'en') return saved
  } catch { /* browser storage may be disabled */ }
  return typeof navigator !== 'undefined' && navigator.language.toLowerCase().startsWith('zh') ? 'zh' : 'en'
}

function syncHtmlLang(lng?: string): void {
  if (typeof document === 'undefined') return
  const normalized = normalizeLanguage(lng)
  document.documentElement.lang = normalized === 'zh' ? 'zh-CN' : 'en'
}

function setSaveFailed(next: boolean): void {
  if (saveFailed === next) return
  saveFailed = next
  for (const listener of saveFailedListeners) listener()
}

if (!i18n.isInitialized) {
  const initialLng = detectInitialLanguage()
  void i18n.use(initReactI18next).init({
    resources: {
      en: { translation: en },
      zh: { translation: zh },
    },
    lng: initialLng,
    fallbackLng: 'en',
    supportedLngs: ['en', 'zh'],
    keySeparator: false,
    nsSeparator: false,
    returnEmptyString: false,
    interpolation: { escapeValue: false },
  })
  syncHtmlLang(initialLng)
  i18n.on('languageChanged', (lng) => syncHtmlLang(lng))
}

export function setAppLanguage(next: AppLanguage): void {
  const normalized = normalizeLanguage(next)
  try { localStorage.setItem(PREFERENCE_KEY, normalized) } catch { /* keep the current page selection */ }
  void i18n.changeLanguage(normalized)
  const persist = saveFn
  if (persist) {
    saveQueue = saveQueue.then(async () => {
      try {
        await persist(normalized)
        setSaveFailed(false)
      } catch {
        setSaveFailed(true)
      }
    })
  }
}

export function bindHostLanguage(saved: string | undefined, save: SaveLanguageFn): void {
  saveFn = save
  if (saved === 'zh' || saved === 'en') {
    try { localStorage.setItem(PREFERENCE_KEY, saved) } catch { /* ignore */ }
    if (normalizeLanguage(i18n.resolvedLanguage ?? i18n.language) !== saved) {
      void i18n.changeLanguage(saved)
    }
  }
}

export function subscribeLanguageSaveFailed(listener: () => void): () => void {
  saveFailedListeners.add(listener)
  return () => { saveFailedListeners.delete(listener) }
}

export function getLanguageSaveFailed(): boolean {
  return saveFailed
}

export default i18n

import type { ThemePreference } from './theme.js'

export type UiPreferences = {
  theme: ThemePreference
  language: 'en' | 'zh'
  bodyFontSize: number
  codeFontSize: number
  runningEnter: 'queue' | 'steer'
  timelineDetailsExpanded: boolean
  usageDensity: 'comfortable' | 'compact'
}

export const defaultUiPreferences: UiPreferences = {
  theme: 'system',
  language: 'en',
  bodyFontSize: 14,
  codeFontSize: 12,
  runningEnter: 'queue',
  timelineDetailsExpanded: false,
  usageDensity: 'comfortable',
}

export function normalizeUiPreferences(value: unknown, base: UiPreferences = defaultUiPreferences): UiPreferences {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return { ...base }
  const raw = value as Record<string, unknown>
  const allowed = ['theme', 'language', 'bodyFontSize', 'codeFontSize', 'runningEnter', 'timelineDetailsExpanded', 'usageDensity']
  if (Object.keys(raw).some((key) => !allowed.includes(key))) throw new Error('Unknown interface preference.')
  const result = { ...base }
  if (raw.theme !== undefined) {
    if (raw.theme !== 'system' && raw.theme !== 'light' && raw.theme !== 'dark') throw new Error('Invalid appearance preference.')
    result.theme = raw.theme
  }
  if (raw.language !== undefined) {
    if (raw.language !== 'en' && raw.language !== 'zh') throw new Error('Invalid language preference.')
    result.language = raw.language
  }
  if (raw.bodyFontSize !== undefined) {
    if (typeof raw.bodyFontSize !== 'number' || !Number.isInteger(raw.bodyFontSize) || raw.bodyFontSize < 12 || raw.bodyFontSize > 18) throw new Error('Body text size must be between 12 and 18 pixels.')
    result.bodyFontSize = raw.bodyFontSize
  }
  if (raw.codeFontSize !== undefined) {
    if (typeof raw.codeFontSize !== 'number' || !Number.isInteger(raw.codeFontSize) || raw.codeFontSize < 10 || raw.codeFontSize > 16) throw new Error('Code text size must be between 10 and 16 pixels.')
    result.codeFontSize = raw.codeFontSize
  }
  if (raw.runningEnter !== undefined) {
    if (raw.runningEnter !== 'queue' && raw.runningEnter !== 'steer') throw new Error('Invalid Enter behavior.')
    result.runningEnter = raw.runningEnter
  }
  if (raw.timelineDetailsExpanded !== undefined) {
    if (typeof raw.timelineDetailsExpanded !== 'boolean') throw new Error('Invalid timeline detail preference.')
    result.timelineDetailsExpanded = raw.timelineDetailsExpanded
  }
  if (raw.usageDensity !== undefined) {
    if (raw.usageDensity !== 'comfortable' && raw.usageDensity !== 'compact') throw new Error('Invalid usage display density.')
    result.usageDensity = raw.usageDensity
  }
  return result
}

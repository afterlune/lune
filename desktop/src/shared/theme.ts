export type ThemePreference = 'system' | 'light' | 'dark'

export const windowColors = { light: '#f6f7f8', dark: '#17191f' } as const

export function isThemePreference(value: unknown): value is ThemePreference {
  return value === 'system' || value === 'light' || value === 'dark'
}

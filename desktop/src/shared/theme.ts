export type ThemePreference = 'system' | 'light' | 'dark'

export const windowColors = { light: '#ffffff', dark: '#131315' } as const

export function isThemePreference(value: unknown): value is ThemePreference {
  return value === 'system' || value === 'light' || value === 'dark'
}

/** Frontend view of internal/settings (identical JSON). */
export type ThemeSetting = 'system' | 'dark' | 'light'
export type ResolvedTheme = 'dark' | 'light'

export interface CsvDefaults {
  delimiter: string
  header: boolean
  quote: string
  /** "empty", "\\N" or "custom" (then nullText). */
  null: string
  nullText: string
  crlf: boolean
  bom: boolean
}

export interface Settings {
  version: number
  theme: ThemeSetting
  editorFontSize: number
  gridPageSize: number
  editorMaxRows: number
  csvExport: CsvDefaults
  confirmCloseDirtyQuery: boolean
}

export const DEFAULT_SETTINGS: Settings = {
  version: 1,
  theme: 'system',
  editorFontSize: 13,
  gridPageSize: 300,
  editorMaxRows: 1000,
  csvExport: { delimiter: ',', header: true, quote: '"', null: 'empty', nullText: '', crlf: false, bom: false },
  confirmCloseDirtyQuery: false,
}

export const LIMITS = {
  fontSize: [9, 32],
  pageSize: [10, 5000],
  maxRows: [1, 50000],
} as const

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

/** Options of the SQL editor's Format command. */
export interface SqlFormatSettings {
  keywordCase: 'upper' | 'lower' | 'preserve'
  /** Spaces per indent level: 2 or 4. */
  indent: number
}

export interface Settings {
  version: number
  theme: ThemeSetting
  editorFontSize: number
  gridPageSize: number
  editorMaxRows: number
  csvExport: CsvDefaults
  confirmCloseDirtyQuery: boolean
  sqlFormat: SqlFormatSettings
  /** Check for, and install, new releases in the background. */
  autoUpdate: boolean
  /** Close a session's connection after this many idle minutes (it reconnects on use); 0 = never. */
  idleDisconnectMinutes: number
}

export const DEFAULT_SETTINGS: Settings = {
  version: 1,
  theme: 'system',
  editorFontSize: 13,
  gridPageSize: 300,
  editorMaxRows: 1000,
  csvExport: { delimiter: ',', header: true, quote: '"', null: 'empty', nullText: '', crlf: false, bom: false },
  confirmCloseDirtyQuery: false,
  sqlFormat: { keywordCase: 'upper', indent: 2 },
  autoUpdate: true,
  idleDisconnectMinutes: 0,
}

export const LIMITS = {
  fontSize: [9, 32],
  pageSize: [10, 5000],
  maxRows: [1, 50000],
} as const

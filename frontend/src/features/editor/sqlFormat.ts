import type { Kind } from '../connections/types'

/** Keyword case of formatted SQL (settings: sqlFormat.keywordCase). */
export type KeywordCase = 'upper' | 'lower' | 'preserve'

export interface SqlFormatOptions {
  keywordCase: KeywordCase
  /** Spaces per indent level (2 or 4). */
  tabWidth: number
}

export type FormatResult = { ok: true; text: string } | { ok: false; reason: string }

/** The part of the sql-formatter module this file uses (tests inject a fake). */
export interface FormatterModule {
  format(text: string, opts: { language: string; keywordCase: KeywordCase; tabWidth: number }): string
}

/** sql-formatter's dialect for a connection kind. */
export const FORMAT_DIALECT: Record<Kind, string> = {
  postgres: 'postgresql',
  mysql: 'mysql',
  mariadb: 'mariadb',
  sqlite: 'sqlite',
}

/** A MySQL client DELIMITER command: a line that starts with DELIMITER. */
const DELIMITER_LINE = /^[ \t]*delimiter[ \t]+\S/im

/** Whether text contains a DELIMITER command (the formatter would mangle the script). */
export function hasDelimiterCommand(text: string): boolean {
  return DELIMITER_LINE.test(text)
}

/** Loads sql-formatter on first use, so it stays out of the main bundle. */
export const loadFormatter = (): Promise<FormatterModule> => import('sql-formatter') as Promise<FormatterModule>

/**
 * Formats SQL in the connection's dialect. Scripts with a DELIMITER command
 * and text the library cannot parse are refused (ok: false, with a short
 * reason); the caller then leaves the text unchanged. A trailing newline of
 * the input is kept.
 */
export async function formatSql(text: string, kind: Kind, opts: SqlFormatOptions, load: () => Promise<FormatterModule> = loadFormatter): Promise<FormatResult> {
  if (!text.trim()) return { ok: true, text }
  if (hasDelimiterCommand(text)) {
    return { ok: false, reason: 'Scripts with a DELIMITER command cannot be formatted.' }
  }
  try {
    const lib = await load()
    const out = lib.format(text, {
      language: FORMAT_DIALECT[kind] ?? 'sql',
      keywordCase: opts.keywordCase,
      tabWidth: opts.tabWidth === 4 ? 4 : 2,
    })
    return { ok: true, text: /\n$/.test(text) && !/\n$/.test(out) ? `${out}\n` : out }
  } catch (err) {
    const first = (err instanceof Error ? err.message : String(err)).split('\n')[0].trim()
    return { ok: false, reason: first ? `Could not format the SQL: ${first}` : 'Could not format the SQL.' }
  }
}

/**
 * Maps a cursor offset in before to the matching place in after, where after
 * differs from before only in whitespace and letter case (formatting): the
 * cursor keeps the same number of non-whitespace characters before it.
 */
export function mapCursor(before: string, after: string, pos: number): number {
  let n = 0
  for (let i = 0; i < Math.min(pos, before.length); i++) if (!/\s/.test(before[i])) n++
  if (n === 0) return 0
  for (let i = 0; i < after.length; i++) {
    if (!/\s/.test(after[i])) {
      n--
      if (n === 0) return i + 1
    }
  }
  return after.length
}

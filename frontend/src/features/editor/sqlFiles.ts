import { SQLFileService } from '../../../bindings/lontar/internal/services'

/** A .sql file as the editor sees it (Go textfile.File). */
export interface SqlFile {
  path: string
  /** Base name (the tab title). */
  name: string
  /** Text with "\n" line endings. */
  text: string
  /** Modification time, Unix milliseconds. */
  modTime: number
  /** The file's line ending, restored on save. */
  eol: string
}

/** Codes of Go's textfile.FileError. */
export type FileErrorCode = 'too_large' | 'not_text' | 'not_found' | 'changed_on_disk'

/** The code of the structured file error carried by a rejected backend call, if any. */
export function fileErrorCode(err: unknown): FileErrorCode | null {
  if (typeof err !== 'object' || err === null) return null
  let cause = (err as { cause?: unknown }).cause
  if (typeof cause === 'string') {
    try {
      cause = JSON.parse(cause)
    } catch {
      return null
    }
  }
  if (typeof cause !== 'object' || cause === null || (cause as { kind?: unknown }).kind !== 'fileError') return null
  const code = (cause as { code?: unknown }).code
  return code === 'too_large' || code === 'not_text' || code === 'not_found' || code === 'changed_on_disk' ? code : null
}

/** Backend calls for .sql files (injectable for tests). */
export interface SqlFileApi {
  /** Shows the open dialog; null when cancelled. */
  open(): Promise<SqlFile | null>
  read(path: string): Promise<SqlFile>
  /**
   * Saves text; an empty path asks where first (name suggests the file
   * name) and resolves null when that is cancelled. expectedModTime != 0
   * refuses to overwrite a file changed on disk since (changed_on_disk).
   */
  save(path: string, name: string, text: string, eol: string, expectedModTime: number): Promise<SqlFile | null>
}

/** Typed wrapper around the generated SQLFileService bindings. */
export const sqlFileApi: SqlFileApi = {
  async open() {
    return (await SQLFileService.OpenSQLFile()) ?? null
  },
  async read(path) {
    const f = await SQLFileService.ReadSQLFile(path)
    if (!f) throw new Error('empty response')
    return f
  },
  async save(path, name, text, eol, expectedModTime) {
    return (await SQLFileService.SaveSQLFile(path, name, text, eol, expectedModTime)) ?? null
  },
}

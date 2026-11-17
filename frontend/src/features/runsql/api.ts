import { SQLRunService } from '../../../bindings/lontar/internal/services'

/** What Inspect found (Go sqlfile.Info). */
export interface SqlFileInfo {
  path: string
  name: string
  size: number
  /** 'pg_dump' | 'mysqldump' | 'mariadb-dump' | 'sqlite' | 'sql' */
  dumpType: string
  hasCopy: boolean
  /** Exact when exact, else extrapolated from the first 8 MiB. */
  statements: number
  exact: boolean
  warning: string
}

export interface RunOptions {
  singleTransaction: boolean
  continueOnError: boolean
}

export interface StatementInfo {
  line: number
  preview: string
}

export interface StatementError extends StatementInfo {
  message: string
  /** The whole statement ('' when it was too long to keep). */
  sql: string
}

export interface RunResult {
  run: number
  failed: number
  skipped: number
  rowsCopied: number
  errors: StatementError[]
  cancelled: boolean
  stopped: boolean
  rolledBack: boolean
  durationMs: number
}

export interface RunFileResult {
  needsConfirm: boolean
  writes: number
  writeStatements: StatementInfo[]
  result: RunResult | null
}

/** Payload of the "sqlfile:progress" event. */
export interface RunProgress {
  jobId: string
  phase: 'scan' | 'run'
  bytesRead: number
  totalBytes: number
  statements: number
  rowsCopied: number
}

export const RUN_PROGRESS_EVENT = 'sqlfile:progress'

/** Backend calls of the Run SQL file dialog (injectable for tests). */
export interface SqlRunApi {
  /** Shows the open dialog and inspects the file; null when cancelled. */
  pickAndInspect(sessionId: string): Promise<SqlFileInfo | null>
  inspect(sessionId: string, path: string): Promise<SqlFileInfo>
  runFile(jobId: string, sessionId: string, path: string, opts: RunOptions, confirmed: boolean): Promise<RunFileResult>
  cancel(jobId: string): Promise<boolean>
}

function result(r: RunResult | null | undefined): RunResult | null {
  return r ? { ...r, errors: r.errors ?? [] } : null
}

export const sqlRunApi: SqlRunApi = {
  async pickAndInspect(sessionId) {
    const info = await SQLRunService.PickAndInspect(sessionId)
    return (info as SqlFileInfo | null) ?? null
  },
  async inspect(sessionId, path) {
    const info = await SQLRunService.Inspect(sessionId, path)
    if (!info) throw new Error('empty response')
    return info as SqlFileInfo
  },
  async runFile(jobId, sessionId, path, opts, confirmed) {
    const r = await SQLRunService.RunFile(jobId, sessionId, path, opts, confirmed)
    if (!r) throw new Error('empty response')
    return {
      needsConfirm: r.needsConfirm,
      writes: r.writes,
      writeStatements: (r.writeStatements ?? []) as StatementInfo[],
      result: result(r.result as RunResult | null),
    }
  },
  cancel: (jobId) => SQLRunService.Cancel(jobId),
}

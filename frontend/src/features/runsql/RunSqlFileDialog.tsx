import { useEffect, useId, useRef, useState } from 'react'
import { errorMessage } from '../../lib/errors'
import { onEvent } from '../../lib/events'
import { newRequestId } from '../../stores/workspace'
import type { SafeMode } from '../edits/types'
import { formatBytes } from '../structure/types'
import {
  RUN_PROGRESS_EVENT,
  sqlRunApi,
  type RunOptions,
  type RunProgress,
  type RunResult,
  type SqlFileInfo,
  type SqlRunApi,
  type StatementError,
  type StatementInfo,
} from './api'

export interface RunSqlFileDialogProps {
  sessionId: string
  safeMode: SafeMode
  onClose(): void
  /** Called after a run that executed statements (refresh the schema). */
  onRan(): void
  /** Opens a failed statement in a new query tab. */
  onOpenStatement(sql: string): void
  api?: SqlRunApi
}

/** How errors are handled (Go sqlfile.Options). */
export type ErrorMode = 'stop' | 'transaction' | 'continue'

export function runOptions(mode: ErrorMode): RunOptions {
  return { singleTransaction: mode === 'transaction', continueOnError: mode === 'continue' }
}

const DUMP_LABELS: Record<string, string> = {
  pg_dump: 'pg_dump (PostgreSQL plain format)',
  mysqldump: 'mysqldump',
  'mariadb-dump': 'mariadb-dump',
  sqlite: 'sqlite3 .dump',
  sql: 'SQL script',
}

export function dumpLabel(type: string): string {
  return DUMP_LABELS[type] ?? type
}

/** Percent of the file read (0–100). */
export function percent(p: { bytesRead: number; totalBytes: number }): number {
  return p.totalBytes > 0 ? Math.min(100, Math.round((p.bytesRead / p.totalBytes) * 100)) : 0
}

type Phase =
  | { s: 'file'; busy: boolean }
  | { s: 'options'; info: SqlFileInfo }
  | { s: 'running'; info: SqlFileInfo; jobId: string; progress: RunProgress | null; cancelling: boolean }
  | { s: 'confirm'; info: SqlFileInfo; writes: number; statements: StatementInfo[] }
  | { s: 'done'; info: SqlFileInfo; result: RunResult }

const plural = (n: number, one: string, many = `${one}s`) => `${n.toLocaleString()} ${n === 1 ? one : many}`

/** The progress line under the bar. */
function progressText(p: RunProgress | null, cancelling: boolean): string {
  if (cancelling) return 'Cancelling…'
  if (!p) return 'Starting…'
  if (p.phase === 'scan') return `Checking the file for writes… ${percent(p)}%`
  const copied = p.rowsCopied > 0 ? ` · ${plural(p.rowsCopied, 'row')} copied` : ''
  return `${percent(p)}% · ${plural(p.statements, 'statement')}${copied}`
}

/**
 * Runs a .sql file (a dump) on the session, streaming: choose the file
 * (size, dump type and statement estimate are shown), choose how errors
 * are handled, confirm the writes on a "confirm" connection, follow the
 * progress (Cancel rolls a single transaction back) and read the result,
 * whose failed statements open in a query tab.
 */
export default function RunSqlFileDialog({ sessionId, safeMode, onClose, onRan, onOpenStatement, api = sqlRunApi }: RunSqlFileDialogProps) {
  const uid = useId()
  const id = (n: string) => `${uid}-${n}`
  const [path, setPath] = useState('')
  const [mode, setMode] = useState<ErrorMode>('stop')
  const [phase, setPhase] = useState<Phase>({ s: 'file', busy: false })
  const [error, setError] = useState<string | null>(null)
  const jobRef = useRef<string | null>(null)
  const running = phase.s === 'running'

  useEffect(
    () =>
      onEvent<RunProgress>(RUN_PROGRESS_EVENT, (p) => {
        if (p.jobId === jobRef.current) setPhase((ph) => (ph.s === 'running' ? { ...ph, progress: p } : ph))
      }),
    [],
  )

  async function inspect(pick: boolean) {
    setError(null)
    if (!pick && !path.trim()) {
      setError('Choose a SQL file.')
      return
    }
    setPhase({ s: 'file', busy: true })
    try {
      const info = pick ? await api.pickAndInspect(sessionId) : await api.inspect(sessionId, path.trim())
      if (!info) {
        setPhase({ s: 'file', busy: false })
        return
      }
      setPath(info.path)
      setPhase({ s: 'options', info })
    } catch (err) {
      setError(errorMessage(err))
      setPhase({ s: 'file', busy: false })
    }
  }

  async function run(info: SqlFileInfo, confirmed: boolean) {
    setError(null)
    const jobId = newRequestId('sqlfile')
    jobRef.current = jobId
    setPhase({ s: 'running', info, jobId, progress: null, cancelling: false })
    try {
      const res = await api.runFile(jobId, sessionId, info.path, runOptions(mode), confirmed)
      if (res.needsConfirm) {
        setPhase({ s: 'confirm', info, writes: res.writes, statements: res.writeStatements })
      } else if (res.result) {
        setPhase({ s: 'done', info, result: res.result })
        if (res.result.run + res.result.failed > 0) onRan()
      } else {
        setPhase({ s: 'options', info })
      }
    } catch (err) {
      setError(errorMessage(err))
      setPhase({ s: 'options', info })
    } finally {
      jobRef.current = null
    }
  }

  function cancel() {
    if (phase.s !== 'running') return
    setPhase({ ...phase, cancelling: true })
    void api.cancel(phase.jobId)
  }

  function open(e: StatementError) {
    onOpenStatement(e.sql || e.preview)
    onClose()
  }

  const info = phase.s === 'file' ? null : phase.info

  return (
    <div className="dialog-backdrop">
      <div
        className="dialog run-sql-dialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby={id('title')}
        onKeyDown={(e) => {
          if (e.key === 'Escape' && !running) {
            e.preventDefault()
            onClose()
          }
        }}
      >
        <h2 id={id('title')}>Run SQL file</h2>

        {phase.s === 'file' && (
          <div className="form-grid">
            <label htmlFor={id('path')}>File</label>
            <div className="path-field">
              <input
                id={id('path')}
                className="mono"
                placeholder="/path/to/dump.sql"
                value={path}
                onChange={(e) => setPath(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') void inspect(false)
                }}
              />
              <button type="button" disabled={phase.busy} onClick={() => void inspect(true)}>
                Browse…
              </button>
            </div>
            <span />
            <p className="hint">
              Runs pg_dump plain dumps (with COPY data), mysqldump and mariadb-dump files, sqlite3 .dump output and other SQL scripts, statement by
              statement. Files of any size are streamed.
            </p>
          </div>
        )}

        {info && (
          <dl className="run-sql-facts">
            <dt>File</dt>
            <dd className="mono" title={info.path}>
              {info.name}
            </dd>
            <dt>Size</dt>
            <dd>{formatBytes(info.size)}</dd>
            <dt>Type</dt>
            <dd>
              {dumpLabel(info.dumpType)}
              {info.hasCopy ? ' · COPY data' : ''}
            </dd>
            <dt>Statements</dt>
            <dd>{info.exact ? info.statements.toLocaleString() : `about ${info.statements.toLocaleString()}`}</dd>
          </dl>
        )}
        {info?.warning && (
          <div className="warning-box" role="note">
            {info.warning}
          </div>
        )}

        {phase.s === 'options' && (
          <fieldset className="plain-fieldset">
            <legend id={id('mode')} className="run-sql-legend">
              On errors
            </legend>
            <div className="field" role="radiogroup" aria-labelledby={id('mode')}>
              <label>
                <input type="radio" name={id('mode')} checked={mode === 'stop'} onChange={() => setMode('stop')} /> Stop at the first error (keep what
                ran)
              </label>
              <label>
                <input type="radio" name={id('mode')} checked={mode === 'transaction'} onChange={() => setMode('transaction')} /> Run in one
                transaction (roll everything back on an error or cancel)
              </label>
              <label>
                <input type="radio" name={id('mode')} checked={mode === 'continue'} onChange={() => setMode('continue')} /> Continue after errors (list
                them at the end)
              </label>
              {mode === 'transaction' && (
                <span className="hint">The file's own BEGIN/COMMIT are skipped. MySQL and MariaDB commit DDL statements immediately.</span>
              )}
            </div>
            {safeMode === 'readonly' && <p className="hint">Read-only safe mode: a file with any write statement is refused before anything runs.</p>}
          </fieldset>
        )}

        {phase.s === 'running' && (
          <div className="run-sql-progress" aria-live="polite">
            <progress aria-label="Progress" max={100} value={phase.progress ? percent(phase.progress) : undefined} />
            <span>{progressText(phase.progress, phase.cancelling)}</span>
          </div>
        )}

        {phase.s === 'confirm' && (
          <div className="warning-box" role="note">
            <p>
              This connection asks before writing. The file has {plural(phase.writes, 'statement')} that change data or schema
              {phase.writes > phase.statements.length ? `; the first ${phase.statements.length}:` : ':'}
            </p>
            <ol className="run-sql-list mono">
              {phase.statements.map((st, i) => (
                <li key={i}>
                  <span className="muted">{st.line}</span> {st.preview}
                </li>
              ))}
            </ol>
          </div>
        )}

        {phase.s === 'done' && <ResultView result={phase.result} onOpen={open} />}

        {error && (
          <p className="error" role="alert">
            {error}
          </p>
        )}

        <div className="dialog-actions">
          {phase.s === 'options' && (
            <button type="button" onClick={() => setPhase({ s: 'file', busy: false })}>
              Back
            </button>
          )}
          <span className="spacer" />
          {running ? (
            <button type="button" disabled={phase.cancelling} onClick={cancel}>
              Cancel run
            </button>
          ) : (
            <button type="button" onClick={onClose}>
              {phase.s === 'done' ? 'Close' : 'Cancel'}
            </button>
          )}
          {phase.s === 'file' && (
            <button type="button" className="primary" disabled={phase.busy} onClick={() => void inspect(false)}>
              Next
            </button>
          )}
          {phase.s === 'options' && (
            <button type="button" className="primary" onClick={() => void run(phase.info, false)}>
              Run
            </button>
          )}
          {phase.s === 'confirm' && (
            <button type="button" className="primary danger" onClick={() => void run(phase.info, true)}>
              Run anyway
            </button>
          )}
        </div>
      </div>
    </div>
  )
}

/** The status line of a finished run. */
export function resultStatus(r: RunResult): string {
  if (r.cancelled) return r.rolledBack ? 'Cancelled: everything was rolled back.' : 'Cancelled: the statements that ran before stay.'
  if (r.rolledBack) return 'Stopped at an error: everything was rolled back.'
  if (r.stopped) return 'Stopped at an error: the statements before it stay.'
  if (r.failed > 0) return 'Finished with errors.'
  return 'Finished.'
}

/** The summary of a finished run and its failed statements. */
function ResultView({ result: r, onOpen }: { result: RunResult; onOpen(e: StatementError): void }) {
  const parts = [plural(r.run, 'statement') + ' ran']
  if (r.failed > 0) parts.push(`${r.failed.toLocaleString()} failed`)
  if (r.skipped > 0) parts.push(`${r.skipped.toLocaleString()} skipped`)
  if (r.rowsCopied > 0) parts.push(`${plural(r.rowsCopied, 'row')} copied`)
  const secs = (r.durationMs / 1000).toFixed(1)
  const cls = r.cancelled ? '' : r.failed === 0 ? 'ok' : 'error'
  return (
    <div className="run-sql-result">
      <p className={cls} role="status">
        {resultStatus(r)} {parts.join(', ')} in {secs} s.
      </p>
      {r.errors.length > 0 && (
        <>
          <p className="hint">
            {r.failed > r.errors.length ? `The first ${r.errors.length} errors` : 'Errors'}; click one to open the statement in a new query tab.
          </p>
          <ul className="run-sql-errors" aria-label="Errors">
            {r.errors.map((e, i) => (
              <li key={i}>
                <button type="button" className="run-sql-error" onClick={() => onOpen(e)} disabled={!e.sql && !e.preview}>
                  <span className="run-sql-where">{e.line > 0 ? `Line ${e.line}` : 'File'}</span>
                  {e.preview && <span className="mono run-sql-stmt">{e.preview}</span>}
                  <span className="run-sql-msg">{e.message}</span>
                </button>
              </li>
            ))}
          </ul>
        </>
      )}
    </div>
  )
}

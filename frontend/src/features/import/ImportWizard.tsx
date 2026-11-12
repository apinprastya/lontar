import { useEffect, useId, useRef, useState } from 'react'
import { errorMessage } from '../../lib/errors'
import { onEvent } from '../../lib/events'
import { newRequestId } from '../../stores/workspace'
import { dialogApi } from '../connections/dialogApi'
import type { SafeMode } from '../edits/types'
import { filesApi, type FilesApi, type ImportFailure, type ImportMapping, type ImportOptions, type ImportPreview } from '../export/api'
import { DELIMITERS } from '../settings/SettingsDialog'

export interface ImportWizardProps {
  sessionId: string
  schema: string
  table: string
  safeMode: SafeMode
  onClose(): void
  /** Called after a successful import (to reload the table). */
  onImported(rows: number): void
  api?: FilesApi
  pickFile?(title: string): Promise<string>
}

export const IMPORT_PROGRESS_EVENT = 'import:progress'

type Phase =
  | { s: 'file' }
  | { s: 'previewing' }
  | { s: 'map'; preview: ImportPreview }
  | { s: 'running'; preview: ImportPreview; jobId: string; rows: number }
  | { s: 'confirm'; preview: ImportPreview }
  | { s: 'failed'; preview: ImportPreview; failure: ImportFailure | null; message: string }
  | { s: 'done'; rows: number }

const DEFAULT_OPTIONS: ImportOptions = { delimiter: ',', header: true, quote: '"', encoding: 'utf-8', nullMarker: '', emptyAsNull: false, skip: 0 }

/** CSV column index → target column ('' = ignored). */
export function mappingByColumn(preview: ImportPreview, mapping: ImportMapping[]): string[] {
  return preview.headers.map((_, i) => mapping.find((m) => m.source === i)?.column ?? '')
}

/**
 * CSV import into the current table: choose the file and how to read it,
 * check the preview (first 50 rows) and the column mapping (matched by
 * header name; remap or ignore columns), then import in one transaction.
 * A failing row rolls everything back and is reported with its line.
 */
export default function ImportWizard({
  sessionId,
  schema,
  table,
  safeMode,
  onClose,
  onImported,
  api = filesApi,
  pickFile = (t) => dialogApi.openFile(t, [{ displayName: 'CSV files (*.csv, *.tsv, *.txt)', pattern: '*.csv;*.tsv;*.txt' }, { displayName: 'All files', pattern: '*' }]),
}: ImportWizardProps) {
  const uid = useId()
  const [path, setPath] = useState('')
  const [opts, setOpts] = useState<ImportOptions>(DEFAULT_OPTIONS)
  const [phase, setPhase] = useState<Phase>({ s: 'file' })
  const [targets, setTargets] = useState<string[]>([])
  const [error, setError] = useState<string | null>(null)
  const jobRef = useRef<string | null>(null)
  const readOnly = safeMode === 'readonly'

  useEffect(
    () =>
      onEvent<{ jobId: string; rows: number }>(IMPORT_PROGRESS_EVENT, (p) => {
        if (p.jobId === jobRef.current) setPhase((ph) => (ph.s === 'running' ? { ...ph, rows: p.rows } : ph))
      }),
    [],
  )

  const set = <K extends keyof ImportOptions>(k: K, v: ImportOptions[K]) => setOpts((o) => ({ ...o, [k]: v }))

  async function browse() {
    setError(null)
    try {
      const p = await pickFile(`Import into ${table}`)
      if (p) setPath(p)
    } catch (err) {
      setError(errorMessage(err))
    }
  }

  async function loadPreview() {
    if (!path.trim()) {
      setError('Choose a CSV file.')
      return
    }
    setError(null)
    setPhase({ s: 'previewing' })
    try {
      const preview = await api.previewImport(sessionId, path.trim(), opts, schema, table)
      setTargets(mappingByColumn(preview, preview.mapping))
      setPhase({ s: 'map', preview })
    } catch (err) {
      setError(errorMessage(err))
      setPhase({ s: 'file' })
    }
  }

  function mapping(): ImportMapping[] {
    return targets.flatMap((column, source) => (column ? [{ source, column }] : []))
  }

  const duplicates = targets.filter((t, i) => t && targets.indexOf(t) !== i)

  async function runImport(preview: ImportPreview, confirmed: boolean) {
    const jobId = newRequestId('import')
    jobRef.current = jobId
    setPhase({ s: 'running', preview, jobId, rows: 0 })
    try {
      const res = await api.runImport(jobId, sessionId, { path: path.trim(), options: opts, schema, table, mapping: mapping(), batchSize: 500 }, confirmed)
      if (res.needsConfirm) setPhase({ s: 'confirm', preview })
      else if (res.ok) {
        setPhase({ s: 'done', rows: res.rows })
        onImported(res.rows)
      } else setPhase({ s: 'failed', preview, failure: res.failure, message: res.failure?.message ?? 'Import failed' })
    } catch (err) {
      setPhase({ s: 'failed', preview, failure: null, message: errorMessage(err) })
    } finally {
      jobRef.current = null
    }
  }

  const id = (n: string) => `${uid}-${n}`
  const preview = phase.s === 'map' || phase.s === 'running' || phase.s === 'confirm' || phase.s === 'failed' ? phase.preview : null
  const running = phase.s === 'running'
  const mapped = mapping()

  return (
    <div className="dialog-backdrop">
      <div
        className="dialog import-wizard"
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
        <h2 id={id('title')}>
          Import CSV into {schema ? `${schema}.` : ''}
          {table}
        </h2>
        {readOnly && (
          <p className="error" role="alert">
            This connection is in read-only safe mode; importing is not allowed.
          </p>
        )}

        {!preview && phase.s !== 'done' && (
          <div className="form-grid">
            <label htmlFor={id('path')}>File</label>
            <div className="path-field">
              <input id={id('path')} className="mono" placeholder="/path/to/data.csv" value={path} onChange={(e) => setPath(e.target.value)} />
              <button type="button" onClick={() => void browse()}>
                Browse…
              </button>
            </div>
            <label htmlFor={id('delim')}>Delimiter</label>
            <select id={id('delim')} value={opts.delimiter} onChange={(e) => set('delimiter', e.target.value)}>
              {DELIMITERS.map((d) => (
                <option key={d.label} value={d.value}>
                  {d.label}
                </option>
              ))}
            </select>
            <label htmlFor={id('quote')}>Quote</label>
            <select id={id('quote')} value={opts.quote} onChange={(e) => set('quote', e.target.value)}>
              <option value={'"'}>Double quote (")</option>
              <option value="'">Single quote (')</option>
            </select>
            <label htmlFor={id('null')}>NULL marker</label>
            <input id={id('null')} className="mono" placeholder="none (e.g. \N or NULL)" value={opts.nullMarker} onChange={(e) => set('nullMarker', e.target.value)} />
            <label htmlFor={id('skip')}>Rows to skip</label>
            <input id={id('skip')} type="number" min={0} value={opts.skip} onChange={(e) => set('skip', Math.max(0, Number(e.target.value) || 0))} />
            <span />
            <div className="checks">
              <label>
                <input type="checkbox" checked={opts.header} onChange={(e) => set('header', e.target.checked)} /> First row is a header
              </label>
              <label>
                <input type="checkbox" checked={opts.emptyAsNull} onChange={(e) => set('emptyAsNull', e.target.checked)} /> Empty fields are NULL
              </label>
              <span className="hint">UTF-8 only (a byte order mark is skipped).</span>
            </div>
          </div>
        )}

        {preview && (
          <>
            <p className="hint">
              Map each CSV column to a column of {table}. Columns matched by header name are pre-selected; unmapped table columns get their
              default. Showing the first {preview.rows.length} rows.
            </p>
            <div className="import-preview">
              <table className="structure-table">
                <thead>
                  <tr>
                    {preview.headers.map((h, i) => (
                      <th key={i}>
                        <div className="import-head">{h}</div>
                        <select
                          aria-label={`Target for ${h}`}
                          value={targets[i] ?? ''}
                          disabled={running}
                          onChange={(e) => setTargets((t) => t.map((x, j) => (j === i ? e.target.value : x)))}
                        >
                          <option value="">— ignore —</option>
                          {preview.columns.map((c) => (
                            <option key={c.name} value={c.name}>
                              {c.name} ({c.type})
                            </option>
                          ))}
                        </select>
                      </th>
                    ))}
                  </tr>
                </thead>
                <tbody>
                  {preview.rows.map((r, ri) => (
                    <tr key={ri}>
                      {preview.headers.map((_, ci) => {
                        const v = r[ci]
                        return (
                          <td key={ci} className={v === null || v === undefined ? 'muted' : 'mono'}>
                            {v === null ? 'NULL' : v === undefined ? '' : v}
                          </td>
                        )
                      })}
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            {duplicates.length > 0 && <p className="error">Column {duplicates[0]} is mapped more than once.</p>}
          </>
        )}

        <div className="export-status" aria-live="polite">
          {phase.s === 'previewing' && <span>Reading the file…</span>}
          {phase.s === 'running' && (
            <span>
              <span className="spinner" aria-hidden /> {phase.rows.toLocaleString()} rows inserted… (committed at the end)
            </span>
          )}
          {phase.s === 'done' && <span className="ok">Imported {phase.rows.toLocaleString()} rows.</span>}
          {phase.s === 'failed' && (
            <div className="error" role="alert">
              {phase.failure && phase.failure.line > 0 && (
                <strong>
                  Line {phase.failure.line}
                  {phase.failure.column ? `, column ${phase.failure.column}` : ''}:{' '}
                </strong>
              )}
              {phase.message} {phase.failure ? 'Nothing was imported.' : ''}
            </div>
          )}
          {error && (
            <span className="error" role="alert">
              {error}
            </span>
          )}
        </div>

        {phase.s === 'confirm' && (
          <div className="warning-box" role="note">
            This connection asks before writing. Import the file into {table}?
          </div>
        )}

        <div className="dialog-actions">
          {preview && !running && (
            <button type="button" onClick={() => setPhase({ s: 'file' })}>
              Back
            </button>
          )}
          <span className="spacer" />
          {running ? (
            <button type="button" onClick={() => void api.cancelImport((phase as { jobId: string }).jobId)}>
              Cancel import
            </button>
          ) : (
            <button type="button" onClick={onClose}>
              {phase.s === 'done' ? 'Close' : 'Cancel'}
            </button>
          )}
          {!preview && phase.s !== 'done' && (
            <button type="button" className="primary" disabled={readOnly || phase.s === 'previewing'} onClick={() => void loadPreview()}>
              Preview
            </button>
          )}
          {preview && phase.s !== 'confirm' && (
            <button type="button" className="primary" disabled={running || readOnly || mapped.length === 0 || duplicates.length > 0} onClick={() => void runImport(preview, false)}>
              Import
            </button>
          )}
          {phase.s === 'confirm' && (
            <button type="button" className="primary danger" onClick={() => void runImport(phase.preview, true)}>
              Import anyway
            </button>
          )}
        </div>
      </div>
    </div>
  )
}

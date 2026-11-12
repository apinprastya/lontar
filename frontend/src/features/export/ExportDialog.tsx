import { useEffect, useId, useMemo, useRef, useState } from 'react'
import { errorMessage } from '../../lib/errors'
import { onEvent } from '../../lib/events'
import { useSettings } from '../../stores/settings'
import { newRequestId } from '../../stores/workspace'
import { dialogApi } from '../connections/dialogApi'
import { selectionToCSV, type SelectionSpec } from '../grid/cells'
import { DELIMITERS } from '../settings/SettingsDialog'
import type { Cell, FetchRequest, ResultColumn } from '../workspace/types'
import { exportColumns, filesApi, type ExportFormat, type ExportOptions, type FilesApi } from './api'

/** What is exported. */
export type ExportSource =
  | { type: 'table'; schema: string; table: string; request: FetchRequest }
  | { type: 'query'; sql: string; columns: ResultColumn[]; rows: Cell[][]; title: string }
  | { type: 'selection'; columns: ResultColumn[]; rows: Cell[][]; spec: SelectionSpec; title: string }

export interface ExportDialogProps {
  sessionId: string
  source: ExportSource
  onClose(): void
  api?: FilesApi
  pickSave?(title: string, filename: string): Promise<string>
}

type Phase = { s: 'edit' } | { s: 'running'; jobId: string; rows: number } | { s: 'done'; rows: number; path: string } | { s: 'error'; message: string }

export const PROGRESS_EVENT = 'export:progress'

const EXT: Record<ExportFormat | 'tsv', string> = { csv: 'csv', json: 'json', sql: 'sql', tsv: 'tsv' }

function sourceTitle(src: ExportSource): string {
  return src.type === 'table' ? src.table : src.title
}

/** A file name from the title: letters, digits, dot, dash and underscore. */
export function safeFileName(title: string, ext: string): string {
  const base = title.replace(/[^\p{L}\p{N}._-]+/gu, '_').replace(/^_+|_+$/g, '') || 'export'
  return `${base}.${ext}`
}

/**
 * Export dialog: CSV / JSON / SQL INSERT for a whole table (with the tab's
 * filters and sort, streamed by the backend) or a query result (the loaded
 * rows, or all rows by running a read-only statement again); CSV/TSV for a
 * grid selection. Progress, cancel, and the file written atomically.
 */
export default function ExportDialog({ sessionId, source, onClose, api = filesApi, pickSave = (t, f) => dialogApi.saveFile(t, f) }: ExportDialogProps) {
  const uid = useId()
  const csvDefaults = useSettings((s) => s.settings.csvExport)
  const selection = source.type === 'selection'
  const [format, setFormat] = useState<ExportFormat | 'tsv'>(selection ? 'tsv' : 'csv')
  const [opts, setOpts] = useState<ExportOptions>(() => ({
    format: 'csv',
    csv: { ...csvDefaults },
    json: { ndjson: false, numbersAsStrings: false, bytes: 'hex' },
    sql: { table: source.type === 'table' ? source.table : 'query_result', schema: '', batchSize: 100, createTable: false },
  }))
  const [allRows, setAllRows] = useState(false)
  const [rerun, setRerun] = useState<{ ok: boolean; reason: string } | null>(null)
  const [path, setPath] = useState('')
  const [phase, setPhase] = useState<Phase>({ s: 'edit' })
  const [pickError, setPickError] = useState<string | null>(null)
  const jobRef = useRef<string | null>(null)
  const title = sourceTitle(source)

  // Can the query run again ("all rows")? Only read-only statements.
  useEffect(() => {
    if (source.type !== 'query') return
    let live = true
    api.checkQuery(sessionId, source.sql).then(
      () => live && setRerun({ ok: true, reason: '' }),
      (err) => live && setRerun({ ok: false, reason: errorMessage(err) }),
    )
    return () => {
      live = false
    }
  }, [api, sessionId, source])

  useEffect(
    () =>
      onEvent<{ jobId: string; rows: number }>(PROGRESS_EVENT, (p) => {
        if (p.jobId === jobRef.current) setPhase((ph) => (ph.s === 'running' ? { ...ph, rows: p.rows } : ph))
      }),
    [],
  )

  const truncated = useMemo(
    () => source.type !== 'table' && source.rows.some((r) => r.some((c) => c.trunc)),
    [source],
  )
  const setCsv = <K extends keyof ExportOptions['csv']>(k: K, v: ExportOptions['csv'][K]) => setOpts((o) => ({ ...o, csv: { ...o.csv, [k]: v } }))
  const setJson = <K extends keyof ExportOptions['json']>(k: K, v: ExportOptions['json'][K]) => setOpts((o) => ({ ...o, json: { ...o.json, [k]: v } }))
  const setSql = <K extends keyof ExportOptions['sql']>(k: K, v: ExportOptions['sql'][K]) => setOpts((o) => ({ ...o, sql: { ...o.sql, [k]: v } }))

  async function browse() {
    setPickError(null)
    try {
      const p = await pickSave(`Export ${title}`, safeFileName(title, EXT[format]))
      if (p) setPath(p)
    } catch (err) {
      setPickError(errorMessage(err))
    }
  }

  async function run() {
    const target = path.trim()
    if (!target) {
      setPickError('Choose where to save the file.')
      return
    }
    const jobId = newRequestId('export')
    jobRef.current = jobId
    setPhase({ s: 'running', jobId, rows: 0 })
    try {
      if (source.type === 'selection') {
        const tsv = format === 'tsv'
        const text = selectionToCSV(source.columns, source.rows, source.spec, {
          delimiter: tsv ? '\t' : opts.csv.delimiter,
          quote: opts.csv.quote || '"',
          header: opts.csv.header,
          nullText: opts.csv.null === 'custom' ? opts.csv.nullText : opts.csv.null === '\\N' ? '\\N' : '',
          crlf: opts.csv.crlf,
        })
        await api.saveText(target, (opts.csv.bom ? '﻿' : '') + text)
        setPhase({ s: 'done', rows: Math.max(0, text.split(/\r?\n/).length - 1 - (opts.csv.header ? 1 : 0)), path: target })
        return
      }
      const options: ExportOptions = { ...opts, format: format as ExportFormat }
      const base = { jobId, sessionId, path: target, options }
      const res =
        source.type === 'table'
          ? await api.export({ ...base, source: 'table', table: { ...source.request, offset: 0, limit: 0 } })
          : allRows
            ? await api.export({ ...base, source: 'query', sql: source.sql })
            : await api.export({ ...base, source: 'rows', columns: exportColumns(source.columns), rows: source.rows })
      setPhase({ s: 'done', rows: res.rows, path: res.path })
    } catch (err) {
      setPhase({ s: 'error', message: errorMessage(err) })
    } finally {
      jobRef.current = null
    }
  }

  function cancel() {
    if (phase.s === 'running') {
      void api.cancelExport(phase.jobId)
      return
    }
    onClose()
  }

  const id = (n: string) => `${uid}-${n}`
  const running = phase.s === 'running'
  const formats: { value: ExportFormat | 'tsv'; label: string }[] = selection
    ? [
        { value: 'tsv', label: 'TSV' },
        { value: 'csv', label: 'CSV' },
      ]
    : [
        { value: 'csv', label: 'CSV' },
        { value: 'json', label: 'JSON' },
        { value: 'sql', label: 'SQL INSERT' },
      ]
  const csvLike = format === 'csv' || format === 'tsv'

  return (
    <div className="dialog-backdrop">
      <form
        className="dialog export-dialog"
        noValidate
        role="dialog"
        aria-modal="true"
        aria-labelledby={id('title')}
        onSubmit={(e) => {
          e.preventDefault()
          if (!running) void run()
        }}
        onKeyDown={(e) => {
          if (e.key === 'Escape' && !running) {
            e.preventDefault()
            onClose()
          }
        }}
      >
        <h2 id={id('title')}>
          Export {source.type === 'selection' ? 'selection of ' : ''}
          {title}
        </h2>
        <fieldset disabled={running || phase.s === 'done'} className="plain-fieldset">
          <div className="form-grid">
            <span id={id('fmt')}>Format</span>
            <div className="segmented" role="radiogroup" aria-labelledby={id('fmt')}>
              {formats.map((f) => (
                <label key={f.value}>
                  <input type="radio" name={id('fmt')} checked={format === f.value} onChange={() => setFormat(f.value)} />
                  {f.label}
                </label>
              ))}
            </div>

            {source.type === 'table' && (
              <>
                <span>Rows</span>
                <span className="hint">
                  All rows of {source.schema ? `${source.schema}.` : ''}
                  {source.table}
                  {source.request.filters.length || source.request.rawWhere ? ' matching the filters' : ''}
                  {source.request.sort.length ? ', in the current sort order' : ''}
                </span>
              </>
            )}
            {source.type === 'query' && (
              <>
                <span id={id('rows')}>Rows</span>
                <div className="field" role="radiogroup" aria-labelledby={id('rows')}>
                  <label>
                    <input type="radio" name={id('rows')} checked={!allRows} onChange={() => setAllRows(false)} /> The {source.rows.length} loaded rows
                  </label>
                  <label title={rerun && !rerun.ok ? rerun.reason : undefined}>
                    <input type="radio" name={id('rows')} checked={allRows} disabled={!rerun?.ok} onChange={() => setAllRows(true)} /> All rows (runs the
                    statement again)
                  </label>
                  {rerun && !rerun.ok && <span className="hint">{rerun.reason}</span>}
                  {allRows && (
                    <div className="warning-box" role="note">
                      <p>This statement will run again on a new connection, read-only:</p>
                      <pre className="mono">{source.sql.trim()}</pre>
                    </div>
                  )}
                  {!allRows && truncated && <span className="hint warning-text">Some loaded values were cut for display; choose All rows to export them in full.</span>}
                </div>
              </>
            )}
            {source.type === 'selection' && truncated && (
              <>
                <span />
                <span className="hint warning-text">Some values were cut for display and are exported as shown.</span>
              </>
            )}

            {csvLike && (
              <>
                {format === 'csv' && (
                  <>
                    <label htmlFor={id('delim')}>Delimiter</label>
                    <select id={id('delim')} value={opts.csv.delimiter} onChange={(e) => setCsv('delimiter', e.target.value)}>
                      {DELIMITERS.map((d) => (
                        <option key={d.label} value={d.value}>
                          {d.label}
                        </option>
                      ))}
                    </select>
                    <label htmlFor={id('quote')}>Quote</label>
                    <select id={id('quote')} value={opts.csv.quote} onChange={(e) => setCsv('quote', e.target.value)}>
                      <option value={'"'}>Double quote (")</option>
                      <option value="'">Single quote (')</option>
                    </select>
                  </>
                )}
                <label htmlFor={id('null')}>NULL as</label>
                <div className="field inline-fields">
                  <select id={id('null')} value={opts.csv.null} onChange={(e) => setCsv('null', e.target.value)}>
                    <option value="empty">Empty field</option>
                    <option value="\N">\N</option>
                    <option value="custom">Custom text</option>
                  </select>
                  {opts.csv.null === 'custom' && <input aria-label="NULL text" value={opts.csv.nullText} onChange={(e) => setCsv('nullText', e.target.value)} />}
                </div>
                <label htmlFor={id('eol')}>Line endings</label>
                <select id={id('eol')} value={opts.csv.crlf ? 'crlf' : 'lf'} onChange={(e) => setCsv('crlf', e.target.value === 'crlf')}>
                  <option value="lf">LF (\n)</option>
                  <option value="crlf">CRLF (\r\n)</option>
                </select>
                <span />
                <div className="checks">
                  <label>
                    <input type="checkbox" checked={opts.csv.header} onChange={(e) => setCsv('header', e.target.checked)} /> Header row
                  </label>
                  <label>
                    <input type="checkbox" checked={opts.csv.bom} onChange={(e) => setCsv('bom', e.target.checked)} /> UTF-8 BOM (for Excel)
                  </label>
                </div>
              </>
            )}

            {format === 'json' && (
              <>
                <label htmlFor={id('shape')}>Layout</label>
                <select id={id('shape')} value={opts.json.ndjson ? 'ndjson' : 'array'} onChange={(e) => setJson('ndjson', e.target.value === 'ndjson')}>
                  <option value="array">Array of objects</option>
                  <option value="ndjson">NDJSON (one object per line)</option>
                </select>
                <label htmlFor={id('bytes')}>Binary values</label>
                <select id={id('bytes')} value={opts.json.bytes} onChange={(e) => setJson('bytes', e.target.value as 'hex' | 'base64')}>
                  <option value="hex">Hex</option>
                  <option value="base64">Base64</option>
                </select>
                <span />
                <div className="checks">
                  <label>
                    <input type="checkbox" checked={opts.json.numbersAsStrings} onChange={(e) => setJson('numbersAsStrings', e.target.checked)} /> All numbers as
                    strings
                  </label>
                  <span className="hint">Big integers and decimals are always strings, so they stay exact.</span>
                </div>
              </>
            )}

            {format === 'sql' && (
              <>
                <label htmlFor={id('target')}>Insert into</label>
                <input id={id('target')} className="mono" value={opts.sql.table} onChange={(e) => setSql('table', e.target.value)} />
                <label htmlFor={id('batch')}>Rows per INSERT</label>
                <input id={id('batch')} type="number" min={1} max={10000} value={opts.sql.batchSize} onChange={(e) => setSql('batchSize', Number(e.target.value))} />
                {source.type === 'table' && (
                  <>
                    <span />
                    <label className="checks">
                      <input type="checkbox" checked={opts.sql.createTable} onChange={(e) => setSql('createTable', e.target.checked)} /> Add CREATE TABLE
                    </label>
                  </>
                )}
              </>
            )}

            <label htmlFor={id('path')}>Save to</label>
            <div className="field">
              <div className="path-field">
                <input id={id('path')} className="mono" placeholder={`/path/to/${safeFileName(title, EXT[format])}`} value={path} onChange={(e) => setPath(e.target.value)} />
                <button type="button" onClick={() => void browse()}>
                  Browse…
                </button>
              </div>
              {pickError && <span className="hint">{pickError}</span>}
            </div>
          </div>
        </fieldset>

        <div className="export-status" aria-live="polite">
          {phase.s === 'running' && (
            <span>
              <span className="spinner" aria-hidden /> {phase.rows.toLocaleString()} rows written…
            </span>
          )}
          {phase.s === 'done' && (
            <span className="ok">
              Exported {phase.rows.toLocaleString()} rows to <span className="mono">{phase.path}</span>
            </span>
          )}
          {phase.s === 'error' && (
            <span className="error" role="alert">
              {phase.message}
            </span>
          )}
        </div>

        <div className="dialog-actions">
          <span className="spacer" />
          <button type="button" onClick={cancel}>
            {running ? 'Cancel export' : phase.s === 'done' ? 'Close' : 'Cancel'}
          </button>
          {phase.s !== 'done' && (
            <button type="submit" className="primary" disabled={running}>
              Export
            </button>
          )}
        </div>
      </form>
    </div>
  )
}

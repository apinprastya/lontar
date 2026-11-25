import { useEffect, useId, useRef, useState, type FormEvent, type ReactNode } from 'react'
import { copyText } from '../../lib/clipboard'
import type { ColumnDraft, FKDraft, IndexDraft, IndexPartDraft } from '../../stores/structureEdits'
import type { Caps, ChangeFailure, StructureStatement } from './editTypes'

/** The statements as one script (display and copy). */
export function statementsText(statements: StructureStatement[]): string {
  return statements.map((s) => s.sql.trim() + ';').join('\n\n')
}

/** A modal dialog shell: Escape cancels. */
function Dialog({ title, className, onCancel, onSubmit, children, alert = false }: { title: string; className?: string; onCancel(): void; onSubmit?(): void; children: ReactNode; alert?: boolean }) {
  const uid = useId()
  const submit = (e: FormEvent) => {
    e.preventDefault()
    onSubmit?.()
  }
  return (
    <div className="dialog-backdrop">
      <form
        className={`dialog ${className ?? ''}`}
        role={alert ? 'alertdialog' : 'dialog'}
        aria-modal="true"
        aria-labelledby={`${uid}-t`}
        onSubmit={submit}
        onKeyDown={(e) => {
          if (e.key === 'Escape') {
            e.preventDefault()
            onCancel()
          }
        }}
      >
        <h2 id={`${uid}-t`}>{title}</h2>
        {children}
      </form>
    </div>
  )
}

function FailureText({ failure }: { failure: ChangeFailure | null }) {
  if (!failure) return null
  return (
    <p className="field-error selectable" role="alert">
      {failure.message}
    </p>
  )
}

/** The DDL Apply runs (exact: DDL has no bound parameters). */
export function StructurePreviewDialog({
  statements,
  transactional,
  applying,
  onApply,
  onClose,
}: {
  statements: StructureStatement[]
  transactional: boolean
  applying: boolean
  onApply(): void
  onClose(): void
}) {
  const [copied, setCopied] = useState(false)
  const ref = useRef<HTMLButtonElement>(null)
  useEffect(() => ref.current?.focus(), [])
  const text = statementsText(statements)
  return (
    <Dialog title="Apply structure changes?" className="preview-dialog" onCancel={onClose} onSubmit={onApply}>
      <p className="muted">
        {statements.length} statement{statements.length === 1 ? '' : 's'} will run{transactional ? ' in one transaction: if one fails, nothing changes.' : '.'}
      </p>
      {!transactional && (
        <p className="banner warning" role="note">
          MySQL and MariaDB commit each DDL statement. If one fails, the statements before it stay applied.
        </p>
      )}
      <pre className="preview-sql selectable" data-testid="structure-sql">
        {text}
      </pre>
      <div className="dialog-actions">
        <button
          type="button"
          onClick={() =>
            void copyText(text)
              .then(() => setCopied(true))
              .catch(() => {})
          }
        >
          {copied ? 'Copied' : 'Copy'}
        </button>
        <span className="spacer" />
        <button type="button" onClick={onClose} disabled={applying}>
          Cancel
        </button>
        <button ref={ref} type="submit" className="primary" disabled={applying}>
          {applying ? 'Applying…' : 'Apply'}
        </button>
      </div>
    </Dialog>
  )
}

const NO_METHOD = ''

/** A new index: name, key parts (columns or expressions), unique, method, predicate. */
export function IndexDialog({
  caps,
  columns,
  table,
  onAdd,
  onCancel,
}: {
  caps: Caps
  columns: ColumnDraft[]
  table: string
  onAdd(x: Omit<IndexDraft, 'key' | 'original' | 'dropped'>): void
  onCancel(): void
}) {
  const first = columns[0]?.key ?? ''
  const [parts, setParts] = useState<IndexPartDraft[]>([{ column: first, expr: '', desc: false, length: 0 }])
  const [unique, setUnique] = useState(false)
  const [method, setMethod] = useState(NO_METHOD)
  const [predicate, setPredicate] = useState('')
  const colName = (key: string) => columns.find((c) => c.key === key)?.name ?? ''
  const suggested = `${table || 't'}_${parts.map((p) => (p.column ? colName(p.column) : 'expr')).join('_')}_${unique ? 'key' : 'idx'}`
  const [name, setName] = useState('')
  const finalName = name.trim() || suggested
  const valid = parts.length > 0 && parts.every((p) => (p.column ? true : p.expr.trim() !== ''))
  const update = (i: number, p: Partial<IndexPartDraft>) => setParts((ps) => ps.map((x, j) => (j === i ? { ...x, ...p } : x)))

  return (
    <Dialog title="Add index" className="struct-dialog" onCancel={onCancel} onSubmit={() => valid && onAdd({ name: finalName, parts, unique, method, predicate: predicate.trim() })}>
      <div className="form-grid">
        <label htmlFor="ix-name">Name</label>
        <input id="ix-name" value={name} placeholder={suggested} onChange={(e) => setName(e.target.value)} autoFocus spellCheck={false} />
        <span>Unique</span>
        <label className="check">
          <input type="checkbox" checked={unique} onChange={(e) => setUnique(e.target.checked)} /> Unique values only
        </label>
        {caps.indexMethods.length > 0 && (
          <>
            <label htmlFor="ix-method">Method</label>
            <select id="ix-method" value={method} onChange={(e) => setMethod(e.target.value)}>
              <option value={NO_METHOD}>Default</option>
              {caps.indexMethods.map((m) => (
                <option key={m} value={m}>
                  {m}
                </option>
              ))}
            </select>
          </>
        )}
      </div>
      <h3 className="struct-subhead">Key parts</h3>
      <ul className="struct-parts">
        {parts.map((p, i) => (
          <li key={i}>
            <select aria-label={`Key part ${i + 1}`} value={p.column} onChange={(e) => update(i, { column: e.target.value })}>
              {columns.map((c) => (
                <option key={c.key} value={c.key}>
                  {c.name || '(unnamed)'}
                </option>
              ))}
              {caps.expressionIndexes && <option value="">(expression)</option>}
            </select>
            {!p.column && <input aria-label={`Expression of key part ${i + 1}`} value={p.expr} placeholder="lower(email)" onChange={(e) => update(i, { expr: e.target.value })} spellCheck={false} />}
            {caps.indexPrefix && p.column && (
              <input
                aria-label={`Prefix length of key part ${i + 1}`}
                type="number"
                min={0}
                className="struct-narrow"
                value={p.length || ''}
                placeholder="length"
                onChange={(e) => update(i, { length: Math.max(0, Number(e.target.value) || 0) })}
              />
            )}
            <label className="check">
              <input type="checkbox" aria-label={`Descending key part ${i + 1}`} checked={p.desc} onChange={(e) => update(i, { desc: e.target.checked })} /> DESC
            </label>
            <button type="button" aria-label={`Remove key part ${i + 1}`} disabled={parts.length === 1} onClick={() => setParts((ps) => ps.filter((_, j) => j !== i))}>
              Remove
            </button>
          </li>
        ))}
      </ul>
      <button type="button" onClick={() => setParts((ps) => [...ps, { column: first, expr: '', desc: false, length: 0 }])}>
        Add key part
      </button>
      {caps.partialIndexes && (
        <div className="form-grid struct-gap">
          <label htmlFor="ix-where">Where</label>
          <input id="ix-where" value={predicate} placeholder="Optional: a partial index condition" onChange={(e) => setPredicate(e.target.value)} spellCheck={false} />
        </div>
      )}
      <div className="dialog-actions">
        <span className="spacer" />
        <button type="button" onClick={onCancel}>
          Cancel
        </button>
        <button type="submit" className="primary" disabled={!valid}>
          Add index
        </button>
      </div>
    </Dialog>
  )
}

const ACTIONS = ['', 'NO ACTION', 'RESTRICT', 'CASCADE', 'SET NULL', 'SET DEFAULT']

/** A new foreign key: column pairs to a referenced table. */
export function ForeignKeyDialog({
  columns,
  schema,
  table,
  tables,
  loadColumns,
  onAdd,
  onCancel,
}: {
  columns: ColumnDraft[]
  schema: string
  /** This table's name (a self-reference uses the draft's columns). */
  table: string
  tables: string[]
  loadColumns(table: string): Promise<string[]>
  onAdd(x: Omit<FKDraft, 'key' | 'original' | 'dropped'>): void
  onCancel(): void
}) {
  const targets = [...new Set([...(table ? [table] : []), ...tables])]
  const [refTable, setRefTable] = useState(targets.find((t) => t !== table) ?? targets[0] ?? '')
  const [refCols, setRefCols] = useState<string[]>([])
  const [pairs, setPairs] = useState<{ column: string; ref: string }[]>([{ column: columns[0]?.key ?? '', ref: '' }])
  const [name, setName] = useState('')
  const [onUpdate, setOnUpdate] = useState('')
  const [onDelete, setOnDelete] = useState('')
  const self = refTable === table

  useEffect(() => {
    let live = true
    if (self) {
      setRefCols(columns.map((c) => c.name))
      return
    }
    if (!refTable) return
    void loadColumns(refTable).then((cols) => {
      if (live) setRefCols(cols)
    })
    return () => {
      live = false
    }
  }, [refTable, self, columns, loadColumns])

  const resolved = pairs.map((p) => ({ ...p, ref: p.ref || refCols[0] || '' }))
  const valid = refTable !== '' && resolved.every((p) => p.column && p.ref)
  const update = (i: number, p: Partial<{ column: string; ref: string }>) => setPairs((ps) => ps.map((x, j) => (j === i ? { ...x, ...p } : x)))

  return (
    <Dialog
      title="Add foreign key"
      className="struct-dialog"
      onCancel={onCancel}
      onSubmit={() =>
        valid &&
        onAdd({ name: name.trim(), columns: resolved.map((p) => p.column), refSchema: schema, refTable, refColumns: resolved.map((p) => p.ref), onUpdate, onDelete })
      }
    >
      <div className="form-grid">
        <label htmlFor="fk-name">Name</label>
        <input id="fk-name" value={name} placeholder="Optional" onChange={(e) => setName(e.target.value)} autoFocus spellCheck={false} />
        <label htmlFor="fk-table">References</label>
        <select id="fk-table" value={refTable} onChange={(e) => setRefTable(e.target.value)}>
          {targets.map((t) => (
            <option key={t} value={t}>
              {t}
              {t === table ? ' (this table)' : ''}
            </option>
          ))}
        </select>
      </div>
      <h3 className="struct-subhead">Columns</h3>
      <ul className="struct-parts">
        {resolved.map((p, i) => (
          <li key={i}>
            <select aria-label={`Column ${i + 1}`} value={p.column} onChange={(e) => update(i, { column: e.target.value })}>
              {columns.map((c) => (
                <option key={c.key} value={c.key}>
                  {c.name || '(unnamed)'}
                </option>
              ))}
            </select>
            <span aria-hidden="true">→</span>
            <select aria-label={`Referenced column ${i + 1}`} value={p.ref} onChange={(e) => update(i, { ref: e.target.value })}>
              {refCols.map((c) => (
                <option key={c} value={c}>
                  {c}
                </option>
              ))}
            </select>
            <button type="button" aria-label={`Remove column ${i + 1}`} disabled={pairs.length === 1} onClick={() => setPairs((ps) => ps.filter((_, j) => j !== i))}>
              Remove
            </button>
          </li>
        ))}
      </ul>
      <button type="button" onClick={() => setPairs((ps) => [...ps, { column: columns[0]?.key ?? '', ref: '' }])}>
        Add column
      </button>
      <div className="form-grid struct-gap">
        <label htmlFor="fk-update">On update</label>
        <select id="fk-update" value={onUpdate} onChange={(e) => setOnUpdate(e.target.value)}>
          {ACTIONS.map((a) => (
            <option key={a} value={a}>
              {a || 'Default'}
            </option>
          ))}
        </select>
        <label htmlFor="fk-delete">On delete</label>
        <select id="fk-delete" value={onDelete} onChange={(e) => setOnDelete(e.target.value)}>
          {ACTIONS.map((a) => (
            <option key={a} value={a}>
              {a || 'Default'}
            </option>
          ))}
        </select>
      </div>
      <div className="dialog-actions">
        <span className="spacer" />
        <button type="button" onClick={onCancel}>
          Cancel
        </button>
        <button type="submit" className="primary" disabled={!valid}>
          Add foreign key
        </button>
      </div>
    </Dialog>
  )
}

/** Drop table confirmation with the statement (and CASCADE on PostgreSQL). */
export function DropTableDialog({
  table,
  statements,
  cascade,
  canCascade,
  loading,
  busy,
  failure,
  onCascade,
  onConfirm,
  onCancel,
}: {
  table: string
  statements: StructureStatement[]
  cascade: boolean
  canCascade: boolean
  loading: boolean
  busy: boolean
  failure: ChangeFailure | null
  onCascade(on: boolean): void
  onConfirm(): void
  onCancel(): void
}) {
  return (
    <Dialog title={`Drop table ${table}?`} className="confirm-dialog" alert onCancel={onCancel} onSubmit={onConfirm}>
      <p>The table and all its rows are deleted. This cannot be undone.</p>
      {canCascade && (
        <label className="check">
          <input type="checkbox" checked={cascade} onChange={(e) => onCascade(e.target.checked)} /> Also drop objects that depend on it (CASCADE)
        </label>
      )}
      {statements.length > 0 && (
        <pre className="preview-sql selectable" data-testid="drop-sql">
          {statementsText(statements)}
        </pre>
      )}
      <FailureText failure={failure} />
      <div className="dialog-actions">
        <span className="spacer" />
        <button type="button" onClick={onCancel} disabled={busy}>
          Cancel
        </button>
        <button type="submit" className="danger" disabled={busy || loading || statements.length === 0} autoFocus>
          {busy ? 'Dropping…' : 'Drop table'}
        </button>
      </div>
    </Dialog>
  )
}

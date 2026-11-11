import { useEffect, useLayoutEffect, useRef, useState, type KeyboardEvent, type MutableRefObject } from 'react'
import type { Kind } from '../connections/types'
import type { Cell, ResultColumn } from '../workspace/types'
import { cellEditText } from '../../stores/pending'
import type { EditValue } from './types'

export type EditorKind = 'text' | 'number' | 'integer' | 'bool' | 'date' | 'datetime' | 'time' | 'json' | 'enum' | 'bytes'

/** Picks the editor for a column type (case-insensitive). */
export function editorKind(column: Pick<ResultColumn, 'type' | 'enumValues'>, engine: Kind): EditorKind {
  const t = column.type.toLowerCase().trim()
  if (column.enumValues && column.enumValues.length > 0) return 'enum'
  if (t.endsWith(']') || t.startsWith('_')) return 'text' // PostgreSQL arrays
  if (/^bool/.test(t) || ((engine === 'mysql' || engine === 'mariadb') && /^tinyint\(1\)/.test(t))) return 'bool'
  if (/json/.test(t)) return 'json'
  if (/bytea|blob|binary|^bit\b/.test(t)) return 'bytes'
  if (/interval|point|inet|cidr/.test(t)) return 'text'
  if (/^date$/.test(t)) return 'date'
  if (/timestamp|datetime/.test(t)) return 'datetime'
  if (/^time\b/.test(t)) return 'time'
  if (/int|serial|^year/.test(t)) return 'integer'
  if (/numeric|decimal|real|double|float|money/.test(t)) return 'number'
  return 'text'
}

const INTEGER = /^[-+]?\d+$/
const NUMBER = /^[-+]?(\d+\.?\d*|\.\d+)([eE][-+]?\d+)?$/
const DATE = /^\d{4}-\d{2}-\d{2}$/
const DATETIME = /^\d{4}-\d{2}-\d{2}([ T]\d{2}:\d{2}(:\d{2}(\.\d{1,9})?)?)?(Z|[+-]\d{2}(:?\d{2})?)?$/i
const TIME = /^\d{2}:\d{2}(:\d{2}(\.\d{1,9})?)?(Z|[+-]\d{2}(:?\d{2})?)?$/i
const HEX = /^(0x)?([0-9a-f]{2})*$/i

/** Validation message for typed text, or null when it is acceptable. */
export function validateText(kind: EditorKind, text: string): string | null {
  const s = text.trim()
  switch (kind) {
    case 'integer':
      return INTEGER.test(s) ? null : 'Enter a whole number'
    case 'number':
      return NUMBER.test(s) || /^[-+]?(nan|infinity|inf)$/i.test(s) ? null : 'Enter a number'
    case 'date':
      return DATE.test(s) ? null : 'Use YYYY-MM-DD'
    case 'datetime':
      return DATETIME.test(s) ? null : 'Use YYYY-MM-DD HH:MM:SS (optionally .ffffff and a zone)'
    case 'time':
      return TIME.test(s) ? null : 'Use HH:MM:SS'
    case 'bool':
      return /^(true|false)$/i.test(s) ? null : 'Use true or false'
    case 'bytes':
      return HEX.test(s.replace(/\s+/g, '')) ? null : 'Enter hex bytes (pairs of 0-9, a-f)'
    case 'json':
      try {
        JSON.parse(text)
        return null
      } catch {
        return 'Not valid JSON'
      }
  }
  return null
}

/** Converts accepted editor text into a staged value. */
export function toEditValue(kind: EditorKind, text: string): EditValue {
  if (kind === 'bytes') return { kind: 'bytes', hex: text.replace(/\s+/g, '').replace(/^0x/i, '').toLowerCase() }
  if (kind === 'bool') return { kind: 'value', text: text.trim().toLowerCase() }
  if (kind === 'json') return { kind: 'value', text }
  if (kind === 'text' || kind === 'enum') return { kind: 'value', text }
  return { kind: 'value', text: text.trim() }
}

/** Text an editor starts with: the staged value, else the original. */
export function initialText(staged: EditValue | undefined, cell: Cell | undefined): string {
  if (staged) {
    if (staged.kind === 'value') return staged.text
    if (staged.kind === 'bytes') return staged.hex
    return ''
  }
  return cellEditText(cell)
}

/** Text needs the multi-line editor (newlines or long). */
export function isMultiline(kind: EditorKind, text: string): boolean {
  return kind === 'json' || (kind === 'text' && (text.includes('\n') || text.length > 120))
}

export interface CellEditorProps {
  column: ResultColumn
  engine: Kind
  /** Starting text. */
  text: string
  /** Replace the text with a typed character (grid typing). */
  typed?: string
  allowDefault?: boolean
  onCommit(v: EditValue): void
  onCancel(): void
  /** Commit when the user clicks outside the editor (grid overlay). */
  commitOnOutsideClick?: boolean
  /** Lets the grid forward keys that reached it while the editor opened. */
  handle?: MutableRefObject<EditorHandle | null>
}

/** Imperative access to an open editor. */
export interface EditorHandle {
  /** Appends typed text and refocuses the field. */
  type(text: string): void
  commit(): void
  cancel(): void
}

/** Type-aware single-value editor (grid overlay and row detail). */
export function CellEditor({ column, engine, text, typed, allowDefault = true, onCommit, onCancel, commitOnOutsideClick, handle }: CellEditorProps) {
  const kind = editorKind(column, engine)
  const [value, setValue] = useState(typed ?? (kind === 'bool' && text === '' ? 'true' : text))
  const [multi, setMulti] = useState(() => isMultiline(kind, typed ?? text))
  const ref = useRef<HTMLInputElement & HTMLTextAreaElement & HTMLSelectElement>(null)
  const root = useRef<HTMLDivElement>(null)
  const done = useRef(false)
  const error = validateText(kind, value)

  useLayoutEffect(() => {
    const el = ref.current
    if (!el) return
    el.focus()
    if (typed === undefined && typeof el.select === 'function' && el.tagName !== 'SELECT') el.select()
    // The grid takes focus back after the click that opened the editor:
    // take it again (without touching what was typed meanwhile).
    const t = setTimeout(() => {
      if (!done.current && root.current && !root.current.contains(document.activeElement)) ref.current?.focus()
    }, 30)
    return () => clearTimeout(t)
  }, [multi])

  // Latest commit/cancel for the outside-click listener.
  const outside = useRef<() => void>(() => {})
  outside.current = () => (validateText(kind, value) ? finish(null) : finish(toEditValue(kind, value)))
  useEffect(() => {
    if (!commitOnOutsideClick) return
    const onDown = (e: MouseEvent) => {
      if (root.current && e.target instanceof Node && !root.current.contains(e.target)) outside.current()
    }
    document.addEventListener('mousedown', onDown, true)
    return () => document.removeEventListener('mousedown', onDown, true)
  }, [commitOnOutsideClick])

  const finish = (v: EditValue | null) => {
    if (done.current) return
    done.current = true
    if (v) onCommit(v)
    else onCancel()
  }
  const commit = () => {
    if (!error) finish(toEditValue(kind, value))
  }
  if (handle) {
    handle.current = {
      type: (t) => {
        setValue((v) => v + t)
        ref.current?.focus()
      },
      commit: () => outside.current(),
      cancel: () => finish(null),
    }
  }
  useEffect(
    () => () => {
      if (handle) handle.current = null
    },
    [handle],
  )
  const onKey = (e: KeyboardEvent) => {
    e.stopPropagation()
    if (e.key === 'Escape') {
      e.preventDefault()
      finish(null)
    } else if (e.key === 'Enter' && (!multi || e.ctrlKey || e.metaKey)) {
      e.preventDefault()
      commit()
    }
  }
  const common = {
    ref,
    'aria-label': `Edit ${column.name}`,
    'aria-invalid': error ? true : undefined,
    onKeyDown: onKey,
  }
  const keep = (e: { preventDefault(): void }) => e.preventDefault() // keep focus in the field

  let field
  if (kind === 'enum' || kind === 'bool') {
    const options = kind === 'bool' ? ['true', 'false'] : [...(column.enumValues ?? [])]
    if (value !== '' && !options.includes(value)) options.unshift(value)
    field = (
      <select {...common} className="cell-editor-input" value={value} onChange={(e) => setValue(e.target.value)}>
        {options.map((o) => (
          <option key={o} value={o}>
            {o}
          </option>
        ))}
      </select>
    )
  } else if (multi) {
    field = <textarea {...common} className="cell-editor-input mono" rows={6} value={value} onChange={(e) => setValue(e.target.value)} spellCheck={false} />
  } else {
    field = (
      <input
        {...common}
        className="cell-editor-input mono"
        value={value}
        inputMode={kind === 'integer' || kind === 'number' ? 'decimal' : undefined}
        placeholder={kind === 'bytes' ? 'hex, e.g. 00ff10' : kind === 'datetime' ? 'YYYY-MM-DD HH:MM:SS' : kind === 'date' ? 'YYYY-MM-DD' : undefined}
        onChange={(e) => {
          setValue(e.target.value)
          if (kind === 'text' && e.target.value.length > 120) setMulti(true)
        }}
      />
    )
  }

  return (
    <div className="cell-editor" data-kind={kind} ref={root}>
      {field}
      <div className="cell-editor-bar">
        {error ? <span className="field-error" role="alert">{error}</span> : <span className="hint">{multi ? 'Ctrl/⌘+Enter to save' : 'Enter to save'} · Esc to cancel</span>}
        <span className="spacer" />
        {kind === 'text' && !multi && (
          <button type="button" className="link-button" onMouseDown={keep} onClick={() => setMulti(true)}>
            Expand
          </button>
        )}
        {column.nullable && (
          <button type="button" onMouseDown={keep} onClick={() => finish({ kind: 'null' })}>
            NULL
          </button>
        )}
        {allowDefault && (
          <button type="button" onMouseDown={keep} onClick={() => finish({ kind: 'default' })}>
            Default
          </button>
        )}
        <button type="button" className="primary" onMouseDown={keep} disabled={Boolean(error)} onClick={commit}>
          Save
        </button>
      </div>
    </div>
  )
}

/**
 * Staged (uncommitted) grid edits of one table tab, as immutable data with
 * pure update functions. Existing rows are keyed by their original primary
 * key cells, so an edit to a key column still finds the row on commit.
 */
import type { ChangeFailure, ChangeSet, EditValue } from '../features/edits/types'
import type { Cell, ResultColumn } from '../features/workspace/types'

export interface RowEdit {
  /** Original key cells (as fetched). */
  pk: Record<string, Cell>
  /** Original cells of the edited columns. */
  old: Record<string, Cell>
  set: Record<string, EditValue>
}

export interface InsertRow {
  id: number
  values: Record<string, EditValue>
}

export interface PendingData {
  edits: Record<string, RowEdit>
  inserts: InsertRow[]
  /** Row key → original key cells. */
  deletes: Record<string, Record<string, Cell>>
}

export interface Pending extends PendingData {
  undo: PendingData[]
  redo: PendingData[]
  nextId: number
}

/** Most undo steps kept. */
export const UNDO_LIMIT = 200

export const emptyPending: Pending = { edits: {}, inserts: [], deletes: {}, undo: [], redo: [], nextId: 1 }

const data = (p: PendingData): PendingData => ({ edits: p.edits, inserts: p.inserts, deletes: p.deletes })

/** Records a new state as one undo step. */
function commitStep(p: Pending, next: PendingData, nextId = p.nextId): Pending {
  return { ...next, undo: [...p.undo, data(p)].slice(-UNDO_LIMIT), redo: [], nextId }
}

/** The key columns of a page. */
export function pkColumns(columns: ResultColumn[]): number[] {
  return columns.flatMap((c, i) => (c.isPK ? [i] : []))
}

/** Identity of a fetched row (null when it cannot be keyed). */
export function rowKey(columns: ResultColumn[], row: Cell[]): string | null {
  const pk = pkColumns(columns)
  if (pk.length === 0) return null
  const cells = pk.map((i) => row[i])
  if (cells.some((c) => !c || c.k === 'null' || c.trunc)) return null
  return JSON.stringify(cells.map((c) => [c.k, c.v ?? null]))
}

function pkCells(columns: ResultColumn[], row: Cell[]): Record<string, Cell> {
  const out: Record<string, Cell> = {}
  for (const i of pkColumns(columns)) out[columns[i].name] = row[i]
  return out
}

/** Text to start editing a cell with. */
export function cellEditText(cell: Cell | undefined): string {
  if (!cell || cell.k === 'null') return ''
  if (cell.k === 'bool') return cell.v ? 'true' : 'false'
  return String(cell.v ?? '')
}

/** Display cell for a staged value. */
export function editValueCell(v: EditValue): Cell {
  switch (v.kind) {
    case 'null':
      return { k: 'null' }
    case 'default':
      return { k: 'str', v: 'DEFAULT' }
    case 'bytes':
      return { k: 'bytes', v: v.hex.toLowerCase(), len: Math.floor(v.hex.length / 2) }
    default:
      return { k: 'str', v: v.text }
  }
}

/** Whether a staged value equals the original cell (a no-op edit). */
export function sameValue(v: EditValue, cell: Cell | undefined): boolean {
  if (!cell) return false
  switch (v.kind) {
    case 'null':
      return cell.k === 'null'
    case 'default':
      return false
    case 'bytes':
      return cell.k === 'bytes' && !cell.trunc && String(cell.v ?? '') === v.hex.toLowerCase()
    default:
      return cell.k !== 'null' && cell.k !== 'bytes' && cellEditText(cell) === v.text
  }
}

/** A target row: a fetched row (page index) or a staged insert. */
export type RowRef = { type: 'row'; index: number } | { type: 'insert'; id: number }

export interface CellChange {
  ref: RowRef
  column: string
  value: EditValue
}

/**
 * Stages several cell values as one undo step. Truncated originals and
 * unkeyed rows are skipped (the caller should not offer them).
 */
export function setCells(p: Pending, columns: ResultColumn[], rows: Cell[][], changes: CellChange[]): Pending {
  let edits = p.edits
  let inserts = p.inserts
  let changed = false
  for (const ch of changes) {
    const ci = columns.findIndex((c) => c.name === ch.column)
    if (ci < 0) continue
    if (ch.ref.type === 'insert') {
      const id = ch.ref.id
      inserts = inserts.map((r) => (r.id === id ? { ...r, values: { ...r.values, [ch.column]: ch.value } } : r))
      changed = true
      continue
    }
    const row = rows[ch.ref.index]
    if (!row) continue
    const key = rowKey(columns, row)
    const orig = row[ci]
    if (!key || orig?.trunc || p.deletes[key]) continue
    const cur = edits[key] ?? { pk: pkCells(columns, row), old: {}, set: {} }
    const set = { ...cur.set }
    const old = { ...cur.old }
    if (sameValue(ch.value, orig)) {
      delete set[ch.column]
      delete old[ch.column]
    } else {
      set[ch.column] = ch.value
      old[ch.column] = orig
    }
    edits = { ...edits }
    if (Object.keys(set).length === 0) delete edits[key]
    else edits[key] = { ...cur, set, old }
    changed = true
  }
  return changed ? commitStep(p, { edits, inserts, deletes: p.deletes }) : p
}

/** Stages a new row (values default to the column defaults). */
export function insertRow(p: Pending, values: Record<string, EditValue> = {}): { pending: Pending; id: number } {
  const id = p.nextId
  return { pending: commitStep(p, { ...data(p), inserts: [...p.inserts, { id, values }] }, id + 1), id }
}

/**
 * Marks fetched rows for deletion and drops staged inserts. Rows already
 * marked are unmarked when every selected row is marked (a toggle).
 */
export function deleteRows(p: Pending, columns: ResultColumn[], rows: Cell[][], refs: RowRef[]): Pending {
  const keys = refs.flatMap((r) => {
    if (r.type !== 'row' || !rows[r.index]) return []
    const k = rowKey(columns, rows[r.index])
    return k ? [{ k, row: rows[r.index] }] : []
  })
  const dropIds = new Set(refs.flatMap((r) => (r.type === 'insert' ? [r.id] : [])))
  if (keys.length === 0 && dropIds.size === 0) return p
  const deletes = { ...p.deletes }
  const unmark = keys.length > 0 && dropIds.size === 0 && keys.every(({ k }) => deletes[k])
  for (const { k, row } of keys) {
    if (unmark) delete deletes[k]
    else deletes[k] = pkCells(columns, row)
  }
  return commitStep(p, { edits: p.edits, inserts: p.inserts.filter((r) => !dropIds.has(r.id)), deletes })
}

export function undo(p: Pending): Pending {
  const prev = p.undo[p.undo.length - 1]
  if (!prev) return p
  return { ...prev, undo: p.undo.slice(0, -1), redo: [...p.redo, data(p)], nextId: p.nextId }
}

export function redo(p: Pending): Pending {
  const next = p.redo[p.redo.length - 1]
  if (!next) return p
  return { ...next, undo: [...p.undo, data(p)], redo: p.redo.slice(0, -1), nextId: p.nextId }
}

/** Number of changes: edited rows (not deleted), inserted rows, deleted rows. */
export function pendingCount(p: PendingData): number {
  const edited = Object.keys(p.edits).filter((k) => !p.deletes[k]).length
  return edited + p.inserts.length + Object.keys(p.deletes).length
}

export function isDirty(p: PendingData): boolean {
  return pendingCount(p) > 0
}

/** Which staged change each change-set entry came from (for failures). */
export interface ChangeRefs {
  updates: string[]
  inserts: number[]
  deletes: string[]
}

/** The structured change set sent to Go (deletes win over edits). */
export function buildChangeSet(schema: string, table: string, p: PendingData): { cs: ChangeSet; refs: ChangeRefs } {
  const refs: ChangeRefs = { updates: [], inserts: [], deletes: [] }
  const cs: ChangeSet = { schema, table, updates: [], inserts: [], deletes: [] }
  for (const [k, pk] of Object.entries(p.deletes)) {
    cs.deletes.push({ pk })
    refs.deletes.push(k)
  }
  for (const [k, e] of Object.entries(p.edits)) {
    if (p.deletes[k]) continue
    cs.updates.push({ pk: e.pk, set: e.set, old: e.old })
    refs.updates.push(k)
  }
  for (const r of p.inserts) {
    cs.inserts.push({ values: r.values })
    refs.inserts.push(r.id)
  }
  return { cs, refs }
}

/** The staged change a failure points at. */
export function failedChange(refs: ChangeRefs, f: ChangeFailure | null): { key?: string; insertId?: number } | null {
  if (!f || f.index < 0) return null
  if (f.op === 'update' && refs.updates[f.index] !== undefined) return { key: refs.updates[f.index] }
  if (f.op === 'delete' && refs.deletes[f.index] !== undefined) return { key: refs.deletes[f.index] }
  if (f.op === 'insert' && refs.inserts[f.index] !== undefined) return { insertId: refs.inserts[f.index] }
  return null
}

export type RowState = 'normal' | 'inserted' | 'deleted'

/** A grid row with its staged values and visual state. */
export interface DisplayRow {
  ref: RowRef
  cells: Cell[]
  state: RowState
  /** Per column: a staged value differs from the original. */
  modified: boolean[]
  /** Per column: the staged value is DEFAULT. */
  defaults: boolean[]
  /** Per column: cannot be edited (truncated original, unkeyed row). */
  locked: boolean[]
  key: string | null
}

/** Fetched rows (with staged values) followed by staged inserts. */
export function displayRows(columns: ResultColumn[], rows: Cell[][], p: PendingData): DisplayRow[] {
  const out: DisplayRow[] = rows.map((row, index) => {
    const key = rowKey(columns, row)
    const e = key ? p.edits[key] : undefined
    const cells = columns.map((c, i) => (e?.set[c.name] ? editValueCell(e.set[c.name]) : row[i]))
    return {
      ref: { type: 'row', index },
      cells,
      state: key && p.deletes[key] ? 'deleted' : 'normal',
      modified: columns.map((c) => Boolean(e?.set[c.name])),
      defaults: columns.map((c) => e?.set[c.name]?.kind === 'default'),
      locked: columns.map((_, i) => !key || Boolean(row[i]?.trunc)),
      key,
    }
  })
  for (const r of p.inserts) {
    out.push({
      ref: { type: 'insert', id: r.id },
      cells: columns.map((c) => (r.values[c.name] ? editValueCell(r.values[c.name]) : { k: 'str', v: 'DEFAULT' })),
      state: 'inserted',
      modified: columns.map((c) => Boolean(r.values[c.name])),
      defaults: columns.map((c) => !r.values[c.name] || r.values[c.name].kind === 'default'),
      locked: columns.map(() => false),
      key: null,
    })
  }
  return out
}

/** Staged value of a display cell, if any. */
export function stagedValue(p: PendingData, d: DisplayRow, column: string): EditValue | undefined {
  if (d.ref.type === 'insert') {
    const id = d.ref.id
    return p.inserts.find((r) => r.id === id)?.values[column]
  }
  return d.key ? p.edits[d.key]?.set[column] : undefined
}

/** Values for a duplicate of a row (key columns left to their defaults). */
export function duplicateValues(columns: ResultColumn[], d: DisplayRow, p: PendingData): Record<string, EditValue> {
  const values: Record<string, EditValue> = {}
  columns.forEach((c, i) => {
    if (c.isPK) return
    const staged = stagedValue(p, d, c.name)
    if (staged) {
      values[c.name] = staged
      return
    }
    // Inserted rows only have staged values; the rest are column defaults.
    if (d.ref.type === 'insert' || d.defaults[i]) return
    const cell = d.cells[i]
    if (!cell || cell.trunc) return
    if (cell.k === 'null') values[c.name] = { kind: 'null' }
    else if (cell.k === 'bytes') values[c.name] = { kind: 'bytes', hex: String(cell.v ?? '') }
    else values[c.name] = { kind: 'value', text: cellEditText(cell) }
  })
  return values
}

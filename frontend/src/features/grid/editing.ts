/**
 * Wiring between the data grid / row detail and the workspace store's edit
 * actions. Shared by table tabs and editable query results: both stage
 * changes by tab ID (see EditTarget in stores/workspace.ts).
 */
import type { Kind } from '../connections/types'
import { initialText } from '../edits/editors'
import type { EditValue } from '../edits/types'
import { stagedValue, type CellChange, type DisplayRow, type PendingData, type RowRef } from '../../stores/pending'
import type { Cell, ResultColumn } from '../workspace/types'
import type { GridEditing } from './DataGrid'
import type { RowDetailEditing } from './RowDetail'

/** The store actions editing needs (a subset of WorkspaceState). */
export interface EditActions {
  stageCells(id: string, changes: CellChange[]): void
  markDeleted(id: string, refs: RowRef[]): void
  duplicateRow(id: string, ref: RowRef): void
  addRow(id: string): void
  undoEdit(id: string): void
  redoEdit(id: string): void
  requestCommit(id: string): Promise<void>
}

/** An editable set of rows: the fetched rows and their staged changes. */
export interface Editable {
  /** Tab ID the store actions take. */
  id: string
  columns: ResultColumn[]
  /** Fetched rows, as RowRef indexes refer to them. */
  rows: Cell[][]
  pending: PendingData
  failed: { key?: string; insertId?: number } | null
}

/** Cell changes for TSV pasted at (row, col), clipped to the grid. */
export function pasteChanges(columns: ResultColumn[], rows: DisplayRow[], row: number, col: number, values: readonly (readonly string[])[]): CellChange[] {
  const out: CellChange[] = []
  values.forEach((line, dr) => {
    const d = rows[row + dr]
    if (!d || d.state === 'deleted') return
    line.forEach((text, dc) => {
      const c = col + dc
      const column = columns[c]
      if (!column || d.locked[c]) return
      out.push({ ref: d.ref, column: column.name, value: { kind: 'value', text } })
    })
  })
  return out
}

/** Text the cell editor starts with for display cell (row, col). */
function editText(e: Editable, d: DisplayRow | undefined, col: number): string {
  const column = e.columns[col]
  if (!d || !column) return ''
  const orig = d.ref.type === 'row' ? e.rows[d.ref.index]?.[col] : undefined
  return initialText(stagedValue(e.pending, d, column.name), orig)
}

/**
 * Grid callbacks for e. rows are the grid's rows in display order (a
 * sorted result keeps its RowRefs, so changes land on the right row).
 */
export function gridEditing(e: Editable, rows: DisplayRow[], kind: Kind, s: EditActions): GridEditing {
  const change = (row: number, col: number, value: EditValue): CellChange[] => {
    const d = rows[row]
    const column = e.columns[col]
    return d && column && !d.locked[col] && d.state !== 'deleted' ? [{ ref: d.ref, column: column.name, value }] : []
  }
  return {
    engine: kind,
    rowStates: rows,
    failed: e.failed,
    editText: (row, col) => editText(e, rows[row], col),
    onCommitCell: (row, col, value) => s.stageCells(e.id, change(row, col, value)),
    onSetCells: (items, value) => s.stageCells(e.id, items.flatMap(([col, row]) => change(row, col, value))),
    onDeleteRows: (rs) => s.markDeleted(e.id, rs.flatMap((r) => (rows[r] ? [rows[r].ref] : []))),
    onDuplicateRow: (row) => rows[row] && s.duplicateRow(e.id, rows[row].ref),
    onInsertRow: () => s.addRow(e.id),
    onUndo: () => s.undoEdit(e.id),
    onRedo: () => s.redoEdit(e.id),
    onCommit: () => void s.requestCommit(e.id),
    onPaste: (row, col, values) => s.stageCells(e.id, pasteChanges(e.columns, rows, row, col, values)),
  }
}

/** Row detail editing for display row d (null: nothing selected). */
export function detailEditing(e: Editable, d: DisplayRow | undefined, kind: Kind, s: Pick<EditActions, 'stageCells'>): RowDetailEditing | null {
  if (!d) return null
  return {
    engine: kind,
    state: d,
    editText: (col) => editText(e, d, col),
    onCommit: (col, value) => {
      const column = e.columns[col]
      if (column) s.stageCells(e.id, [{ ref: d.ref, column: column.name, value }])
    },
  }
}

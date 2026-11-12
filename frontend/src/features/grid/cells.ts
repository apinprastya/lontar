import type { Cell, ResultColumn, SortSpec } from '../workspace/types'

/** Longest text drawn in a grid cell (the row detail shows everything). */
export const MAX_CELL_CHARS = 300

/** Text for a bytes cell: "<binary N bytes>". */
export function binaryLabel(cell: Cell): string {
  const n = cell.len ?? 0
  return `<binary ${n.toLocaleString('en-US')} ${n === 1 ? 'byte' : 'bytes'}>`
}

/** Full text of a cell (used for copy and the row detail). NULL → "NULL". */
export function cellText(cell: Cell | undefined): string {
  if (!cell || cell.k === 'null') return 'NULL'
  if (cell.k === 'bytes') return binaryLabel(cell)
  return String(cell.v ?? '')
}

/** Single-line text for a grid cell: newlines flattened, long text cut. */
export function cellDisplay(cell: Cell | undefined): string {
  const s = cellText(cell)
  if (!cell || cell.k === 'null' || cell.k === 'bytes') return s
  const flat = s.replace(/\r?\n|\r/g, '↵ ')
  return flat.length > MAX_CELL_CHARS ? flat.slice(0, MAX_CELL_CHARS) + '…' : flat
}

export function isNull(cell: Cell | undefined): boolean {
  return !cell || cell.k === 'null'
}

export function isNumeric(cell: Cell | undefined): boolean {
  return cell?.k === 'num'
}

/** Value for TSV copy: NULL becomes an empty field; tabs/newlines become spaces. */
export function cellCopyText(cell: Cell | undefined): string {
  if (isNull(cell)) return ''
  if (cell?.k === 'bytes') return String(cell.v ?? '')
  return cellText(cell).replace(/[\t\r\n]+/g, ' ')
}

/** A rectangular selection in grid coordinates (column/row indexes). */
export interface SelectionSpec {
  /** Cell range: x/y are the top-left column/row. */
  range?: { x: number; y: number; width: number; height: number }
  /** Whole rows. */
  rows?: number[]
  /** Whole columns. */
  columns?: number[]
}

/**
 * Tab-separated text for the selection (one line per row). Whole-column
 * selections include a header line with the column names.
 */
export function selectionToTSV(columns: ResultColumn[], rows: Cell[][], sel: SelectionSpec): string {
  const { colIdx, rowIdx, header } = selectionIndexes(columns, rows, sel)
  if (colIdx.length === 0 || rowIdx.length === 0) return ''
  const lines = rowIdx.map((r) => colIdx.map((c) => cellCopyText(rows[r][c])).join('\t'))
  if (header) lines.unshift(colIdx.map((c) => columns[c].name).join('\t'))
  return lines.join('\n')
}

/** The column and row indexes a selection covers (the copy logic's rules). */
export function selectionIndexes(columns: ResultColumn[], rows: Cell[][], sel: SelectionSpec): { colIdx: number[]; rowIdx: number[]; header: boolean } {
  let colIdx: number[] = []
  let rowIdx: number[] = []
  let header = false
  if (sel.rows && sel.rows.length > 0) {
    rowIdx = sel.rows
    colIdx = columns.map((_, i) => i)
  } else if (sel.columns && sel.columns.length > 0) {
    colIdx = sel.columns
    rowIdx = rows.map((_, i) => i)
    header = true
  } else if (sel.range) {
    const { x, y, width, height } = sel.range
    colIdx = Array.from({ length: width }, (_, i) => x + i)
    rowIdx = Array.from({ length: height }, (_, i) => y + i)
  }
  colIdx = colIdx.filter((c) => c >= 0 && c < columns.length)
  rowIdx = rowIdx.filter((r) => r >= 0 && r < rows.length)
  return { colIdx, rowIdx, header }
}

export interface SelectionCSVOptions {
  delimiter: string
  quote: string
  header: boolean
  /** How NULL is written. */
  nullText: string
  crlf: boolean
}

/**
 * The selection as CSV/TSV for "Export selection…": the same cells as the
 * TSV copy, but values are kept intact (quoted when they contain the
 * delimiter, the quote, a line break or would read as NULL) and the
 * header row is optional.
 */
export function selectionToCSV(columns: ResultColumn[], rows: Cell[][], sel: SelectionSpec, o: SelectionCSVOptions): string {
  const { colIdx, rowIdx } = selectionIndexes(columns, rows, sel)
  if (colIdx.length === 0 || rowIdx.length === 0) return ''
  const field = (s: string, value: boolean) =>
    s.includes(o.delimiter) || s.includes(o.quote) || /[\r\n]/.test(s) || /^\s|\s$/.test(s) || (value && s === o.nullText)
      ? o.quote + s.split(o.quote).join(o.quote + o.quote) + o.quote
      : s
  const lines = rowIdx.map((r) =>
    colIdx
      .map((c) => {
        const cell = rows[r][c]
        if (isNull(cell)) return o.nullText
        return field(cell?.k === 'bytes' ? String(cell.v ?? '') : cellText(cell), true)
      })
      .join(o.delimiter),
  )
  if (o.header) lines.unshift(colIdx.map((c) => field(columns[c].name, false)).join(o.delimiter))
  const eol = o.crlf ? '\r\n' : '\n'
  return lines.join(eol) + eol
}

/** Header title with the sort direction (and position for multi-sort). */
export function headerTitle(column: ResultColumn, sort: SortSpec[]): string {
  const i = sort.findIndex((s) => s.column === column.name)
  if (i < 0) return column.name
  const arrow = sort[i].desc ? '▼' : '▲'
  return sort.length > 1 ? `${column.name} ${arrow}${i + 1}` : `${column.name} ${arrow}`
}

/** Initial column width from the name and type lengths. */
export function defaultColumnWidth(column: ResultColumn): number {
  const chars = column.name.length + column.type.length + 4
  return Math.max(120, Math.min(320, chars * 7 + 24))
}

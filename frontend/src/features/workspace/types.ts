/**
 * Frontend view of the Go browse models (internal/driver, internal/query,
 * internal/values). The generated bindings use TS enums; these literal
 * types are identical on the wire. api.ts converts at the boundary.
 */

/** Tag of a cell; see internal/values for the format. */
export type CellKind = 'null' | 'str' | 'num' | 'bool' | 'bytes' | 'time' | 'json'

/**
 * One value. NULL is {k:'null'} (distinct from {k:'str', v:''}). Numbers
 * that do not fit a JS double exactly (big ints, decimals) arrive as
 * strings with k:'num'. Bytes are hex with `len` = full size; `trunc`
 * marks values cut for display by the backend.
 */
export interface Cell {
  k: CellKind
  v?: string | number | boolean
  len?: number
  trunc?: boolean
}

export interface ResultColumn {
  name: string
  type: string
  isPK: boolean
  nullable: boolean
}

export interface Page {
  columns: ResultColumn[]
  rows: Cell[][]
  offset: number
  limit: number
  hasMore: boolean
  totalCount: number | null
  elapsedMs: number
}

export type TableType = 'table' | 'view' | 'materialized view'

export interface TableInfo {
  schema: string
  name: string
  type: TableType
  estimatedRows: number | null
}

export type FilterOp =
  | '='
  | '!='
  | '<'
  | '>'
  | '<='
  | '>='
  | 'contains'
  | 'notContains'
  | 'startsWith'
  | 'endsWith'
  | 'in'
  | 'notIn'
  | 'isNull'
  | 'isNotNull'
  | 'between'

export const FILTER_OPS: { value: FilterOp; label: string }[] = [
  { value: '=', label: '=' },
  { value: '!=', label: '≠' },
  { value: '<', label: '<' },
  { value: '>', label: '>' },
  { value: '<=', label: '≤' },
  { value: '>=', label: '≥' },
  { value: 'contains', label: 'contains' },
  { value: 'notContains', label: 'does not contain' },
  { value: 'startsWith', label: 'starts with' },
  { value: 'endsWith', label: 'ends with' },
  { value: 'in', label: 'in (a, b, …)' },
  { value: 'notIn', label: 'not in (a, b, …)' },
  { value: 'isNull', label: 'is NULL' },
  { value: 'isNotNull', label: 'is not NULL' },
  { value: 'between', label: 'between' },
]

/** Operators without a value input. */
export function opTakesValue(op: FilterOp): boolean {
  return op !== 'isNull' && op !== 'isNotNull'
}

export interface Filter {
  column: string
  op: FilterOp
  value: string
  value2: string
}

export interface SortSpec {
  column: string
  desc: boolean
}

export interface FetchRequest {
  schema: string
  table: string
  filters: Filter[]
  rawWhere: string
  sort: SortSpec[]
  offset: number
  limit: number
}

export interface Location {
  database: string
  schema: string
}

/** Page size: explicit pages like a spreadsheet, not infinite scroll. */
export const PAGE_SIZE = 300

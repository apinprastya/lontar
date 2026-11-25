/**
 * Frontend view of the SQL editor models (internal/query, internal/services,
 * internal/history). Offsets are UTF-16 code units, i.e. JavaScript string
 * indices into the editor text. api.ts normalises nulls at the boundary.
 */
import type { Cell, ResultColumn, TableType } from '../workspace/types'

export type StatementStatus = 'ok' | 'error' | 'cancelled' | 'skipped'

export interface ResultSet {
  id: string
  columns: ResultColumn[]
  rows: Cell[][]
  /** More rows exist than were returned. */
  truncated: boolean
  /** The cursor is still open: FetchMore can return the next rows. */
  canLoadMore: boolean
}

export interface ExecError {
  message: string
  /** SQLSTATE (PostgreSQL), error number (MySQL), result code (SQLite). */
  code: string
  sqlState?: string
  detail?: string
  hint?: string
  /** Error position in the editor, when the engine reports one. */
  position: number | null
}

export interface StatementResult {
  index: number
  sql: string
  start: number
  end: number
  status: StatementStatus
  resultSets: ResultSet[]
  rowsAffected: number | null
  durationMs: number
  error: ExecError | null
}

export type TxStateName = 'idle' | 'active' | 'failed' | 'unknown'

export interface TxInfo {
  state: TxStateName
  /** "server": read from the connection (PostgreSQL); "heuristic": tracked from BEGIN/COMMIT. */
  source: 'server' | 'heuristic'
}

export interface ExecResult {
  statements: StatementResult[]
  durationMs: number
  tx: TxInfo
  notices: string[]
  sessionReset: boolean
  cancelled: boolean
  /** Confirm safe mode: nothing ran; these write statements need confirmation. */
  needsConfirm?: boolean
  writeStatements?: string[]
  /** Nothing ran: these :name placeholders have no value. */
  needsParams?: string[]
}

/** How a parameter value is bound: as a string (the database casts it) or as a number. */
export type ParamType = 'text' | 'number'

/** The value of one :name placeholder; always bound as a parameter, never put into the SQL. */
export interface ParamValue {
  value: string
  null: boolean
  type: ParamType
}

export type ParamValues = Record<string, ParamValue>

export interface ExecOptions {
  maxRows: number
  continueOnError: boolean
  baseOffset: number
  /** The user confirmed the writes (confirm safe mode). */
  confirmed: boolean
  /** Values of the :name placeholders. */
  params?: ParamValues
}

export interface ExplainOptions {
  /** Run the statement and report actual rows and times (reads only; not SQLite). */
  analyze: boolean
  baseOffset: number
  /** Values of the :name placeholders. */
  params?: ParamValues
}

export interface PlanProp {
  key: string
  value: string
}

/** One operation of a query plan. Rows are per loop; timeMs is inclusive over all loops. */
export interface PlanNode {
  op: string
  object: string
  detail: string
  estRows: number | null
  actualRows: number | null
  loops: number | null
  cost: number | null
  timeMs: number | null
  props: PlanProp[]
  children: PlanNode[]
}

/** A query plan (Go query.Plan). */
export interface Plan {
  engine: string
  analyzed: boolean
  /** The EXPLAIN statement that ran. */
  sql: string
  planningMs: number | null
  executionMs: number | null
  nodes: PlanNode[]
  raw: string
  rawFormat: 'json' | 'text'
  notices: string[]
  error: ExecError | null
  cancelled: boolean
  sessionReset: boolean
  tx: TxInfo
  durationMs: number
  /** Nothing ran: these :name placeholders have no value. */
  needsParams?: string[]
}

export interface Statement {
  text: string
  start: number
  end: number
  delimEnd: number
}

export interface CompletionColumn {
  name: string
  type: string
}

export interface CompletionTable {
  name: string
  type: TableType
  /** null: not loaded (big schemas load columns per table). */
  columns: CompletionColumn[] | null
}

export interface CompletionSchema {
  database: string
  schema: string
  schemas: string[]
  tables: CompletionTable[]
  columnsLoaded: boolean
}

export interface HistoryEntry {
  id: number
  connectionId: string
  database: string
  sql: string
  startedAt: string
  durationMs: number
  rowCount: number | null
  rowsAffected: number | null
  isError: boolean
  error: string
  truncated: boolean
  /** Who ran it: '' for the SQL editor, 'mcp' for an AI assistant (lontar mcp). */
  source?: string
}

export const MAX_ROWS_OPTIONS = [100, 500, 1000, 5000, 10000, 50000]
export const DEFAULT_MAX_ROWS = 1000

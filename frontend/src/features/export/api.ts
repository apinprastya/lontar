import { ExportService, ImportService } from '../../../bindings/dbedit/internal/services'
import type { Cell, FetchRequest, ResultColumn } from '../workspace/types'

export type ExportFormat = 'csv' | 'json' | 'sql'

export interface CsvOptions {
  delimiter: string
  header: boolean
  quote: string
  null: string
  nullText: string
  crlf: boolean
  bom: boolean
}

export interface JsonOptions {
  ndjson: boolean
  numbersAsStrings: boolean
  bytes: 'hex' | 'base64'
}

export interface SqlOptions {
  table: string
  schema: string
  batchSize: number
  createTable: boolean
}

export interface ExportOptions {
  format: ExportFormat
  csv: CsvOptions
  json: JsonOptions
  sql: SqlOptions
}

export interface ExportRequest {
  jobId: string
  sessionId: string
  path: string
  options: ExportOptions
  source: 'table' | 'query' | 'rows'
  table?: FetchRequest
  sql?: string
  columns?: { name: string; type: string }[]
  rows?: Cell[][]
}

export interface ImportOptions {
  delimiter: string
  header: boolean
  quote: string
  encoding: string
  nullMarker: string
  emptyAsNull: boolean
  skip: number
}

export interface ImportMapping {
  source: number
  column: string
}

export interface TargetColumn {
  name: string
  type: string
  nullable: boolean
  hasDefault: boolean
  isPK: boolean
}

export interface ImportPreview {
  headers: string[]
  rows: (string | null)[][]
  columns: TargetColumn[]
  mapping: ImportMapping[]
}

export interface ImportRequest {
  path: string
  options: ImportOptions
  schema: string
  table: string
  mapping: ImportMapping[]
  batchSize: number
}

export interface ImportFailure {
  line: number
  column: string
  message: string
}

export interface ImportResult {
  ok: boolean
  rows: number
  needsConfirm: boolean
  failure: ImportFailure | null
}

type Bind<F extends (...a: never[]) => unknown, I extends number> = Parameters<F>[I]

/** Typed wrapper around ExportService/ImportService (injectable for tests). */
export interface FilesApi {
  export(req: ExportRequest): Promise<{ rows: number; path: string }>
  cancelExport(jobId: string): Promise<boolean>
  checkQuery(sessionId: string, sql: string): Promise<void>
  saveText(path: string, text: string): Promise<void>
  previewImport(sessionId: string, path: string, opts: ImportOptions, schema: string, table: string): Promise<ImportPreview>
  runImport(jobId: string, sessionId: string, req: ImportRequest, confirmed: boolean): Promise<ImportResult>
  cancelImport(jobId: string): Promise<boolean>
}

export const filesApi: FilesApi = {
  async export(req) {
    const r = await ExportService.Export(req as unknown as Bind<typeof ExportService.Export, 0>)
    return { rows: r?.rows ?? 0, path: r?.path ?? req.path }
  },
  cancelExport: (jobId) => ExportService.Cancel(jobId),
  async checkQuery(sessionId, sql) {
    await ExportService.CheckQuery(sessionId, sql)
  },
  async saveText(path, text) {
    await ExportService.SaveText(path, text)
  },
  async previewImport(sessionId, path, opts, schema, table) {
    const p = await ImportService.Preview(sessionId, path, opts as unknown as Bind<typeof ImportService.Preview, 2>, schema, table)
    if (!p) throw new Error('empty response')
    return { headers: p.headers ?? [], rows: (p.rows ?? []) as (string | null)[][], columns: (p.columns ?? []) as TargetColumn[], mapping: p.mapping ?? [] }
  },
  async runImport(jobId, sessionId, req, confirmed) {
    const r = await ImportService.Import(jobId, sessionId, req as unknown as Bind<typeof ImportService.Import, 2>, confirmed)
    if (!r) throw new Error('empty response')
    return r as unknown as ImportResult
  },
  cancelImport: (jobId) => ImportService.Cancel(jobId),
}

/** Columns of a result for an export request. */
export function exportColumns(cols: ResultColumn[]): { name: string; type: string }[] {
  return cols.map((c) => ({ name: c.name, type: c.type }))
}

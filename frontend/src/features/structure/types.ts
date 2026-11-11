/**
 * Frontend view of driver.TableDetail (internal/driver/structure.go), the
 * read-only structure of a table or view. api.ts converts at the boundary.
 */
import type { TableType } from '../workspace/types'

export interface ColumnDetail {
  name: string
  ordinal: number
  /** Full declared type: varchar(255), numeric(10,2), int unsigned, text[]. */
  dataType: string
  nullable: boolean
  default: string | null
  isPrimaryKey: boolean
  pkPosition: number
  autoIncrement: boolean
  /** PostgreSQL identity: ALWAYS or BY DEFAULT. */
  identity: string
  /** STORED or VIRTUAL. */
  generated: string
  generationExpr: string | null
  /** MySQL ON UPDATE expression. */
  onUpdate: string
  collation: string
  comment: string
  enumValues?: string[] | null
}

export interface IndexInfo {
  name: string
  columns: string[]
  unique: boolean
  primary: boolean
  method: string
  predicate: string
  definition: string
}

export interface ForeignKey {
  name: string
  schema: string
  table: string
  columns: string[]
  refSchema: string
  refTable: string
  refColumns: string[]
  onUpdate: string
  onDelete: string
}

export interface CheckInfo {
  name: string
  expression: string
}

export interface TriggerInfo {
  name: string
  timing: string
  event: string
  definition: string
}

export interface TableDetail {
  schema: string
  name: string
  type: TableType
  comment: string
  engine: string
  collation: string
  estimatedRows: number | null
  sizeBytes: number | null
  columns: ColumnDetail[]
  indexes: IndexInfo[]
  foreignKeys: ForeignKey[]
  referencedBy: ForeignKey[]
  checks: CheckInfo[]
  triggers: TriggerInfo[]
  checksSupported: boolean
}

export type StructureSection = 'columns' | 'indexes' | 'fks' | 'refs' | 'checks' | 'triggers' | 'ddl'

/** The Extra column: auto-increment, identity, generated, on update. */
export function columnExtra(c: ColumnDetail): string {
  const out: string[] = []
  if (c.identity) out.push(`identity (${c.identity.toLowerCase()})`)
  else if (c.autoIncrement) out.push('auto increment')
  if (c.generated) out.push(`generated ${c.generated.toLowerCase()}${c.generationExpr ? `: ${c.generationExpr}` : ''}`)
  if (c.onUpdate) out.push(`on update ${c.onUpdate}`)
  return out.join(', ')
}

/** Human readable size: 512 B, 1.5 KiB, 20 MiB. */
export function formatBytes(n: number | null): string {
  if (n === null || n < 0) return ''
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']
  let v = n
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i += 1
  }
  return i === 0 ? `${v} B` : `${v < 10 ? v.toFixed(1) : Math.round(v)} ${units[i]}`
}

/** Normalises a backend detail (Go nil slices arrive as null). */
export function normalizeDetail(d: TableDetail): TableDetail {
  return {
    ...d,
    columns: d.columns ?? [],
    indexes: (d.indexes ?? []).map((ix) => ({ ...ix, columns: ix.columns ?? [] })),
    foreignKeys: d.foreignKeys ?? [],
    referencedBy: d.referencedBy ?? [],
    checks: d.checks ?? [],
    triggers: d.triggers ?? [],
  }
}

/**
 * Frontend view of package ddl (internal/ddl) and StructureEditService: the
 * structured change set the Structure view sends (never DDL), engine
 * capabilities and results. editApi.ts converts at the boundary.
 */
import type { TableDetail } from './types'

export type Op = 'add' | 'alter' | 'drop' | 'rename'
export type DefaultKind = 'none' | 'null' | 'value' | 'expression'

export interface TypeSpec {
  text: string
  enumValues: string[] | null
}

export interface DefaultSpec {
  kind: DefaultKind
  text: string
}

export interface GeneratedSpec {
  expr: string
  stored: boolean
}

export interface ColumnChange {
  op: Op
  name: string
  newName: string
  type: TypeSpec | null
  nullable: boolean | null
  default: DefaultSpec | null
  comment: string | null
  collation: string | null
  autoIncrement: boolean | null
  using: string | null
  generated: GeneratedSpec | null
}

export interface IndexColumn {
  column: string
  expr: string
  desc: boolean
  length: number
}

export interface IndexChange {
  op: Op
  name: string
  newName: string
  columns: IndexColumn[]
  unique: boolean
  method: string
  predicate: string
}

export interface FKChange {
  op: Op
  name: string
  columns: string[]
  refSchema: string
  refTable: string
  refColumns: string[]
  onUpdate: string
  onDelete: string
}

export interface CheckChange {
  op: Op
  name: string
  expression: string
}

/** ddl.TableChange: everything staged for one table. */
export interface TableChange {
  schema: string
  table: string
  create: boolean
  base: TableDetail | null
  newName: string | null
  comment: string | null
  columns: ColumnChange[]
  primaryKey: string[] | null
  indexes: IndexChange[]
  foreignKeys: FKChange[]
  checks: CheckChange[]
  dropTriggers: string[]
}

/** ddl.Caps: what the engine can change (the UI disables the rest). */
export interface Caps {
  flavor: 'postgres' | 'mysql' | 'mariadb' | 'sqlite'
  version: string
  alterColumn: boolean
  renameColumn: boolean
  dropColumn: boolean
  comments: boolean
  collation: boolean
  primaryKey: boolean
  foreignKeys: boolean
  checks: boolean
  renameIndex: boolean
  partialIndexes: boolean
  expressionIndexes: boolean
  indexPrefix: boolean
  indexMethods: string[]
  renameTable: boolean
  dropCascade: boolean
  usingExpr: boolean
  enumTypes: boolean
  generatedStored: boolean
  generatedVirtual: boolean
  maxNameLength: number
  transactionalDdl: boolean
  types: string[]
  reason: string
}

export type Section = 'table' | 'columns' | 'primaryKey' | 'indexes' | 'foreignKeys' | 'checks' | 'triggers'

export interface StructureStatement {
  sql: string
  section: Section
  index: number
}

export type FailureKind = 'invalid' | 'unsupported' | 'not_found' | 'structure_changed' | 'readonly' | 'database'

/** ddl.ChangeError: why a change was refused or failed. */
export interface ChangeFailure {
  kind: FailureKind
  section: Section
  /** Position in its section of the TableChange (-1: the table). */
  index: number
  field: string
  message: string
  sql: string
  applied: number
}

export interface StructurePreview {
  statements: StructureStatement[]
  failure: ChangeFailure | null
}

export interface StructureApplyResult {
  ok: boolean
  statements: StructureStatement[]
  failure: ChangeFailure | null
}

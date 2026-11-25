import type { Caps } from '../features/structure/editTypes'
import type { ColumnDetail, ForeignKey, TableDetail } from '../features/structure/types'

/** A TableDetail for tests (no columns, keys or indexes unless given). */
export function makeDetail(over: Partial<TableDetail> = {}): TableDetail {
  return {
    schema: 'public',
    name: 't',
    type: 'table',
    comment: '',
    engine: '',
    collation: '',
    estimatedRows: null,
    sizeBytes: null,
    columns: [],
    indexes: [],
    foreignKeys: [],
    referencedBy: [],
    checks: [],
    triggers: [],
    checksSupported: true,
    ...over,
  }
}

export function makeColumn(over: Partial<ColumnDetail> = {}): ColumnDetail {
  return {
    name: 'c',
    ordinal: 1,
    dataType: 'integer',
    nullable: true,
    default: null,
    isPrimaryKey: false,
    pkPosition: 0,
    autoIncrement: false,
    identity: '',
    generated: '',
    generationExpr: null,
    onUpdate: '',
    collation: '',
    comment: '',
    ...over,
  }
}

export function makeFK(over: Partial<ForeignKey> = {}): ForeignKey {
  return {
    name: 'fk',
    schema: 'public',
    table: 't',
    columns: ['parent_id'],
    refSchema: 'public',
    refTable: 'parent',
    refColumns: ['id'],
    onUpdate: 'NO ACTION',
    onDelete: 'NO ACTION',
    ...over,
  }
}

/** Capabilities for tests (PostgreSQL-like unless overridden). */
export function makeCaps(over: Partial<Caps> = {}): Caps {
  return {
    flavor: 'postgres',
    version: 'PostgreSQL 16',
    alterColumn: true,
    renameColumn: true,
    dropColumn: true,
    comments: true,
    collation: false,
    primaryKey: true,
    foreignKeys: true,
    checks: true,
    renameIndex: true,
    partialIndexes: true,
    expressionIndexes: true,
    indexPrefix: false,
    indexMethods: ['btree', 'gin'],
    renameTable: true,
    dropCascade: true,
    usingExpr: true,
    enumTypes: false,
    generatedStored: true,
    generatedVirtual: false,
    maxNameLength: 63,
    transactionalDdl: true,
    types: ['integer', 'text'],
    reason: '',
    ...over,
  }
}

/** users(id PK, email, org_id FK) with an index, a check and a trigger. */
export function editableDetail(): TableDetail {
  return makeDetail({
    name: 'users',
    comment: 'People',
    columns: [
      makeColumn({ name: 'id', ordinal: 1, nullable: false, isPrimaryKey: true, pkPosition: 1, autoIncrement: true, identity: 'BY DEFAULT' }),
      makeColumn({ name: 'email', ordinal: 2, dataType: 'text', nullable: false, default: "''::text", comment: 'mail' }),
      makeColumn({ name: 'org_id', ordinal: 3 }),
      makeColumn({ name: 'total', ordinal: 4, dataType: 'numeric', generated: 'STORED', generationExpr: 'id * 2' }),
    ],
    indexes: [
      { name: 'users_pkey', columns: ['id'], unique: true, primary: true, method: 'btree', predicate: '', definition: '' },
      { name: 'users_email_idx', columns: ['email'], unique: false, primary: false, method: 'btree', predicate: '', definition: '' },
    ],
    foreignKeys: [makeFK({ name: 'users_org_fk', table: 'users', columns: ['org_id'], refTable: 'orgs' })],
    checks: [{ name: 'users_email_check', expression: "email <> ''" }],
    triggers: [{ name: 'users_touch', timing: 'BEFORE', event: 'UPDATE', definition: 'CREATE TRIGGER users_touch …' }],
  })
}

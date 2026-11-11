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

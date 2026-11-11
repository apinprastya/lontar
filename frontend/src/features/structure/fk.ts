import type { Cell, Filter, ResultColumn } from '../workspace/types'
import type { ForeignKey } from './types'

/**
 * The foreign key each grid column belongs to (the first one when a column
 * is in several), by column index; null for other columns.
 */
export function fkByColumn(fks: ForeignKey[], columns: Pick<ResultColumn, 'name'>[]): (ForeignKey | null)[] {
  return columns.map((c) => fks.find((fk) => fk.columns.includes(c.name)) ?? null)
}

/**
 * Filters that select the row a foreign key points at: refColumn = value
 * for every column of the key, in key order (composite keys give several
 * filters). The values travel as bound parameters through the normal
 * filter model. Returns null when the row has no reference: any key column
 * NULL (MATCH SIMPLE), missing, binary or truncated.
 */
export function referenceFilters(fk: ForeignKey, columns: Pick<ResultColumn, 'name'>[], row: Cell[] | undefined): Filter[] | null {
  if (!row || fk.columns.length === 0 || fk.columns.length !== fk.refColumns.length) return null
  const out: Filter[] = []
  for (let i = 0; i < fk.columns.length; i++) {
    const idx = columns.findIndex((c) => c.name === fk.columns[i])
    const cell = idx < 0 ? undefined : row[idx]
    if (!cell || cell.k === 'null' || cell.k === 'bytes' || cell.trunc || cell.v === undefined) return null
    out.push({ column: fk.refColumns[i], op: '=', value: String(cell.v), value2: '' })
  }
  return out
}

/** "schema.table" when the table is outside the current schema, else "table". */
export function displayName(schema: string, table: string, currentSchema: string): string {
  return schema && schema !== currentSchema ? `${schema}.${table}` : table
}

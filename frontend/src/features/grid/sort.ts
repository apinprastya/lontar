import type { Cell, SortSpec } from '../workspace/types'

/**
 * Next sort after clicking a column header. A plain click makes the column
 * the only sort key and cycles it asc → desc → none; with additive (Shift)
 * the column is cycled in place and other keys are kept, so it can become a
 * secondary key.
 */
export function cycleSort(sort: SortSpec[], column: string, additive: boolean): SortSpec[] {
  const existing = sort.find((s) => s.column === column)
  const next: SortSpec | null = !existing ? { column, desc: false } : !existing.desc ? { column, desc: true } : null
  if (!additive) return next ? [next] : []
  if (!existing) return [...sort, next as SortSpec]
  return next ? sort.map((s) => (s.column === column ? next : s)) : sort.filter((s) => s.column !== column)
}

/** Orders two cells: NULL first, numbers numerically, the rest as text. */
export function compareCells(a: Cell | undefined, b: Cell | undefined): number {
  const an = !a || a.k === 'null'
  const bn = !b || b.k === 'null'
  if (an || bn) return an === bn ? 0 : an ? -1 : 1
  if (a.k === 'num' && b.k === 'num') {
    const x = Number(a.v)
    const y = Number(b.v)
    if (!Number.isNaN(x) && !Number.isNaN(y) && x !== y) return x < y ? -1 : 1
    if (x === y) return 0
  }
  if (a.k === 'bool' && b.k === 'bool') return Number(a.v) - Number(b.v)
  const x = String(a.v ?? '')
  const y = String(b.v ?? '')
  return x < y ? -1 : x > y ? 1 : 0
}

/** Rows sorted client-side by sort (stable; the input is not changed). */
export function sortRows(rows: Cell[][], columnNames: string[], sort: SortSpec[]): Cell[][] {
  const keys = sort.map((s) => ({ i: columnNames.indexOf(s.column), desc: s.desc })).filter((k) => k.i >= 0)
  if (keys.length === 0) return rows
  return rows
    .map((row, n) => ({ row, n }))
    .sort((a, b) => {
      for (const k of keys) {
        const c = compareCells(a.row[k.i], b.row[k.i])
        if (c !== 0) return k.desc ? -c : c
      }
      return a.n - b.n
    })
    .map((x) => x.row)
}

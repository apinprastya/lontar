import type { DbObject, ObjectKind } from '../objects/types'
import type { TableInfo } from '../workspace/types'

export interface TableGroups {
  tables: TableInfo[]
  views: TableInfo[]
}

const byName = (a: TableInfo, b: TableInfo) => a.name.localeCompare(b.name, undefined, { sensitivity: 'base' })

/**
 * Filters tables by a case-insensitive substring and groups them into
 * tables and views (materialized views count as views), sorted by name.
 */
export function groupTables(list: TableInfo[], search: string): TableGroups {
  const q = search.trim().toLowerCase()
  const match = q ? list.filter((t) => t.name.toLowerCase().includes(q)) : list
  return {
    tables: match.filter((t) => t.type === 'table').sort(byName),
    views: match.filter((t) => t.type !== 'table').sort(byName),
  }
}

/** Compact row estimate: 950, 12.3K, 4.5M. */
export function formatEstimate(n: number | null): string {
  if (n === null || n < 0) return ''
  if (n < 1000) return String(n)
  if (n < 1_000_000) return `${(n / 1000).toFixed(n < 10_000 ? 1 : 0)}K`
  if (n < 1_000_000_000) return `${(n / 1_000_000).toFixed(n < 10_000_000 ? 1 : 0)}M`
  return `${(n / 1_000_000_000).toFixed(1)}B`
}

/** Sidebar object groups, in display order (below Tables and Views). */
export const OBJECT_GROUPS = [
  { id: 'functions', label: 'Functions', kinds: ['function', 'aggregate'] },
  { id: 'procedures', label: 'Procedures', kinds: ['procedure'] },
  { id: 'sequences', label: 'Sequences', kinds: ['sequence'] },
  { id: 'types', label: 'Types', kinds: ['enum', 'composite', 'domain'] },
  { id: 'triggers', label: 'Triggers', kinds: ['trigger'] },
  { id: 'events', label: 'Events', kinds: ['event'] },
] as const satisfies readonly { id: string; label: string; kinds: readonly ObjectKind[] }[]

export type ObjectGroupId = (typeof OBJECT_GROUPS)[number]['id']

export type ObjectGroups = Record<ObjectGroupId, DbObject[]>

const objectOrder = (a: DbObject, b: DbObject) =>
  a.name.localeCompare(b.name, undefined, { sensitivity: 'base' }) || a.signature.localeCompare(b.signature) || a.table.localeCompare(b.table)

/**
 * Filters objects by a case-insensitive substring of the name (or, for
 * triggers, of their table) and groups them for the sidebar, sorted by
 * name.
 */
export function groupObjects(list: DbObject[], search: string): ObjectGroups {
  const q = search.trim().toLowerCase()
  const match = q ? list.filter((o) => o.name.toLowerCase().includes(q) || (o.table !== '' && o.table.toLowerCase().includes(q))) : list
  const out = {} as ObjectGroups
  for (const g of OBJECT_GROUPS) {
    const kinds: readonly ObjectKind[] = g.kinds
    out[g.id] = match.filter((o) => kinds.includes(o.kind)).sort(objectOrder)
  }
  return out
}

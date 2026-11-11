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

import { describe, expect, it } from 'vitest'
import type { TableInfo } from '../workspace/types'
import { formatEstimate, groupTables } from './tableList'

const t = (name: string, type: TableInfo['type'] = 'table'): TableInfo => ({ schema: 's', name, type, estimatedRows: null })

describe('groupTables', () => {
  const list = [t('orders'), t('Users'), t('v_orders', 'view'), t('mv_stats', 'materialized view'), t('audit')]

  it('groups into tables and views, sorted case-insensitively', () => {
    const g = groupTables(list, '')
    expect(g.tables.map((x) => x.name)).toEqual(['audit', 'orders', 'Users'])
    expect(g.views.map((x) => x.name)).toEqual(['mv_stats', 'v_orders'])
  })

  it('filters by a case-insensitive substring', () => {
    const g = groupTables(list, '  ORD ')
    expect(g.tables.map((x) => x.name)).toEqual(['orders'])
    expect(g.views.map((x) => x.name)).toEqual(['v_orders'])
    expect(groupTables(list, 'zzz')).toEqual({ tables: [], views: [] })
  })
})

describe('formatEstimate', () => {
  it('formats compactly', () => {
    expect(formatEstimate(null)).toBe('')
    expect(formatEstimate(-1)).toBe('')
    expect(formatEstimate(0)).toBe('0')
    expect(formatEstimate(999)).toBe('999')
    expect(formatEstimate(1234)).toBe('1.2K')
    expect(formatEstimate(123456)).toBe('123K')
    expect(formatEstimate(1_234_567)).toBe('1.2M')
    expect(formatEstimate(123_456_789)).toBe('123M')
    expect(formatEstimate(2_500_000_000)).toBe('2.5B')
  })
})

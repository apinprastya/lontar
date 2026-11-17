import { describe, expect, it } from 'vitest'
import type { TableInfo } from '../workspace/types'
import { OBJECT_GROUPS, formatEstimate, groupObjects, groupTables } from './tableList'
import { makeObject } from '../../test/objects'

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

describe('groupObjects', () => {
  const list = [
    makeObject({ name: 'Zeta' }),
    makeObject({ name: 'add', signature: 'b bigint' }),
    makeObject({ name: 'add', signature: 'a integer' }),
    makeObject({ kind: 'aggregate', name: 'my_sum' }),
    makeObject({ kind: 'procedure', name: 'reset' }),
    makeObject({ kind: 'sequence', name: 'seq' }),
    makeObject({ kind: 'enum', name: 'mood' }),
    makeObject({ kind: 'composite', name: 'pair' }),
    makeObject({ kind: 'domain', name: 'posint' }),
    makeObject({ kind: 'trigger', name: 't1', table: 'orders' }),
    makeObject({ kind: 'trigger', name: 't1', table: 'audit' }),
    makeObject({ kind: 'event', name: 'purge' }),
  ]

  it('groups by kind in sidebar order, sorted by name, then signature and table', () => {
    const g = groupObjects(list, '')
    expect(OBJECT_GROUPS.map((x) => x.id)).toEqual(['functions', 'procedures', 'sequences', 'types', 'triggers', 'events'])
    expect(g.functions.map((o) => `${o.name}(${o.signature})`)).toEqual(['add(a integer)', 'add(b bigint)', 'my_sum()', 'Zeta()'])
    expect(g.procedures.map((o) => o.name)).toEqual(['reset'])
    expect(g.sequences.map((o) => o.name)).toEqual(['seq'])
    expect(g.types.map((o) => o.name)).toEqual(['mood', 'pair', 'posint'])
    expect(g.triggers.map((o) => o.table)).toEqual(['audit', 'orders'])
    expect(g.events.map((o) => o.name)).toEqual(['purge'])
  })

  it('filters by name, or by the table of a trigger', () => {
    const g = groupObjects(list, ' ADD ')
    expect(g.functions).toHaveLength(2)
    expect(g.triggers).toEqual([])
    expect(groupObjects(list, 'orders').triggers.map((o) => o.table)).toEqual(['orders'])
    expect(Object.values(groupObjects(list, 'zzz')).every((x) => x.length === 0)).toBe(true)
  })
})

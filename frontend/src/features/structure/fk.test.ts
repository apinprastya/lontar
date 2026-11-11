import { describe, expect, it } from 'vitest'
import { makeFK } from '../../test/structure'
import { displayName, fkByColumn, referenceFilters } from './fk'

const cols = [{ name: 'id' }, { name: 'a' }, { name: 'b' }, { name: 'parent_id' }]

describe('foreign key navigation helpers', () => {
  it('maps columns to their first foreign key', () => {
    const one = makeFK({ name: 'one', columns: ['parent_id'] })
    const comp = makeFK({ name: 'comp', columns: ['a', 'b'], refColumns: ['x', 'y'] })
    const again = makeFK({ name: 'again', columns: ['b'] })
    expect(fkByColumn([one, comp, again], cols).map((f) => f?.name ?? null)).toEqual([null, 'comp', 'comp', 'one'])
    expect(fkByColumn([], cols)).toEqual([null, null, null, null])
  })

  it('builds one bound = filter per key column, in key order', () => {
    const comp = makeFK({ columns: ['b', 'a'], refColumns: ['rb', 'ra'] })
    const row = [{ k: 'num' as const, v: 1 }, { k: 'str' as const, v: "x'y" }, { k: 'num' as const, v: '9007199254740993' }, { k: 'null' as const }]
    expect(referenceFilters(comp, cols, row)).toEqual([
      { column: 'rb', op: '=', value: '9007199254740993', value2: '' },
      { column: 'ra', op: '=', value: "x'y", value2: '' },
    ])
    expect(referenceFilters(makeFK({ columns: ['a'], refColumns: ['id'] }), cols, [{ k: 'num', v: 1 }, { k: 'bool', v: false }])).toEqual([
      { column: 'id', op: '=', value: 'false', value2: '' },
    ])
  })

  it('has no reference for NULL, binary, truncated or unknown key values', () => {
    const fk = makeFK({ columns: ['a', 'b'], refColumns: ['x', 'y'] })
    const ok = { k: 'num' as const, v: 1 }
    expect(referenceFilters(fk, cols, [ok, ok, { k: 'null' }])).toBeNull()
    expect(referenceFilters(fk, cols, [ok, { k: 'bytes', v: '00', len: 1 }, ok])).toBeNull()
    expect(referenceFilters(fk, cols, [ok, { k: 'str', v: 'x', trunc: true }, ok])).toBeNull()
    expect(referenceFilters(fk, cols, [ok, { k: 'str' }, ok])).toBeNull()
    expect(referenceFilters(fk, cols, [ok, ok])).toBeNull() // missing cell
    expect(referenceFilters(fk, cols, undefined)).toBeNull()
    expect(referenceFilters(makeFK({ columns: ['zz'] }), cols, [ok, ok, ok, ok])).toBeNull()
    expect(referenceFilters(makeFK({ columns: ['a'], refColumns: [] }), cols, [ok, ok])).toBeNull()
    expect(referenceFilters(makeFK({ columns: [], refColumns: [] }), cols, [ok])).toBeNull()
  })

  it('qualifies names outside the current schema', () => {
    expect(displayName('public', 't', 'public')).toBe('t')
    expect(displayName('audit', 't', 'public')).toBe('audit.t')
    expect(displayName('', 't', 'public')).toBe('t')
  })
})

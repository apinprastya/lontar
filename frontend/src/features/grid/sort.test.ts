import { describe, expect, it } from 'vitest'
import type { Cell } from '../workspace/types'
import { compareCells, sortRows } from './sort'

const n = (v: number | string): Cell => ({ k: 'num', v })
const s = (v: string): Cell => ({ k: 'str', v })
const NULL: Cell = { k: 'null' }

describe('client-side sort', () => {
  it('compares cells', () => {
    expect(compareCells(NULL, n(1))).toBe(-1)
    expect(compareCells(n(1), NULL)).toBe(1)
    expect(compareCells(NULL, undefined)).toBe(0)
    expect(compareCells(n(2), n(10))).toBe(-1)
    expect(compareCells(n('9007199254740993'), n(5))).toBe(1)
    expect(compareCells(n(3), n(3))).toBe(0)
    expect(compareCells(n('NaN'), n(1))).toBe(1) // falls back to text
    expect(compareCells({ k: 'bool', v: false }, { k: 'bool', v: true })).toBeLessThan(0)
    expect(compareCells(s('b'), s('a'))).toBe(1)
    expect(compareCells(s('a'), s('a'))).toBe(0)
    expect(compareCells(s('a'), n(1))).toBe(1)
  })

  it('sorts rows stably by several keys', () => {
    const rows = [
      [n(2), s('b')],
      [n(1), s('b')],
      [n(3), s('a')],
      [NULL, s('a')],
    ]
    expect(sortRows(rows, ['id', 'name'], [])).toBe(rows)
    expect(sortRows(rows, ['id', 'name'], [{ column: 'missing', desc: false }])).toBe(rows)
    expect(sortRows(rows, ['id', 'name'], [{ column: 'id', desc: false }]).map((r) => r[0].v)).toEqual([undefined, 1, 2, 3])
    expect(sortRows(rows, ['id', 'name'], [{ column: 'name', desc: true }, { column: 'id', desc: true }]).map((r) => r[0].v)).toEqual([2, 1, 3, undefined])
    expect(sortRows(rows, ['id', 'name'], [{ column: 'name', desc: false }]).map((r) => r[0].v)).toEqual([3, undefined, 2, 1])
  })
})

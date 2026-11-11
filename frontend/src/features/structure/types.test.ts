import { describe, expect, it } from 'vitest'
import { makeColumn, makeDetail } from '../../test/structure'
import { columnExtra, formatBytes, normalizeDetail, type TableDetail } from './types'

describe('structure helpers', () => {
  it('describes column extras', () => {
    expect(columnExtra(makeColumn())).toBe('')
    expect(columnExtra(makeColumn({ autoIncrement: true }))).toBe('auto increment')
    expect(columnExtra(makeColumn({ autoIncrement: true, identity: 'BY DEFAULT' }))).toBe('identity (by default)')
    expect(columnExtra(makeColumn({ generated: 'STORED', generationExpr: 'a * 2' }))).toBe('generated stored: a * 2')
    expect(columnExtra(makeColumn({ generated: 'VIRTUAL', onUpdate: 'CURRENT_TIMESTAMP' }))).toBe('generated virtual, on update CURRENT_TIMESTAMP')
  })

  it('formats sizes', () => {
    expect(formatBytes(null)).toBe('')
    expect(formatBytes(-1)).toBe('')
    expect(formatBytes(512)).toBe('512 B')
    expect(formatBytes(1536)).toBe('1.5 KiB')
    expect(formatBytes(20 * 1024 * 1024)).toBe('20 MiB')
    expect(formatBytes(2 ** 50)).toBe('1024 TiB')
  })

  it('normalises nil slices', () => {
    const d = normalizeDetail({ ...makeDetail(), columns: null, indexes: null, foreignKeys: null, referencedBy: null, checks: null, triggers: null } as unknown as TableDetail)
    expect(d).toEqual(makeDetail())
  })
})

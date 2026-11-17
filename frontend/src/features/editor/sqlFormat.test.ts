import { describe, expect, it, vi } from 'vitest'
import { FORMAT_DIALECT, formatSql, hasDelimiterCommand, mapCursor, type FormatterModule } from './sqlFormat'

const upper2 = { keywordCase: 'upper' as const, tabWidth: 2 }

describe('formatSql', () => {
  it('formats in every dialect', async () => {
    for (const kind of ['postgres', 'mysql', 'mariadb', 'sqlite'] as const) {
      const res = await formatSql('select a, b from t where x = 1', kind, upper2)
      expect(res).toEqual({ ok: true, text: 'SELECT\n  a,\n  b\nFROM\n  t\nWHERE\n  x = 1' })
    }
    expect(FORMAT_DIALECT).toEqual({ postgres: 'postgresql', mysql: 'mysql', mariadb: 'mariadb', sqlite: 'sqlite' })
  })

  it('passes the dialect and options to the library', async () => {
    const format = vi.fn(() => 'out')
    const load = async () => ({ format }) as FormatterModule
    await formatSql('x', 'mariadb', { keywordCase: 'lower', tabWidth: 4 }, load)
    expect(format).toHaveBeenCalledWith('x', { language: 'mariadb', keywordCase: 'lower', tabWidth: 4 })
    await formatSql('x', 'postgres', { keywordCase: 'preserve', tabWidth: 3 }, load)
    expect(format).toHaveBeenLastCalledWith('x', { language: 'postgresql', keywordCase: 'preserve', tabWidth: 2 })
  })

  it('applies keyword case and indent', async () => {
    expect(await formatSql('SELECT a::int FROM t', 'postgres', { keywordCase: 'lower', tabWidth: 4 })).toEqual({
      ok: true,
      text: 'select\n    a::int\nfrom\n    t',
    })
    expect(await formatSql('Select a From t', 'sqlite', { keywordCase: 'preserve', tabWidth: 2 })).toEqual({
      ok: true,
      text: 'Select\n  a\nFrom\n  t',
    })
  })

  it('keeps a trailing newline and leaves blank text alone', async () => {
    expect(await formatSql('select 1\n', 'postgres', upper2)).toEqual({ ok: true, text: 'SELECT\n  1\n' })
    expect(await formatSql('  \n', 'postgres', upper2)).toEqual({ ok: true, text: '  \n' })
  })

  it('refuses scripts with a DELIMITER command', async () => {
    const load = vi.fn()
    const res = await formatSql('DELIMITER //\nCREATE TRIGGER t BEGIN END//\nDELIMITER ;\n', 'mysql', upper2, load)
    expect(res).toEqual({ ok: false, reason: 'Scripts with a DELIMITER command cannot be formatted.' })
    expect(load).not.toHaveBeenCalled()
    expect(hasDelimiterCommand('select 1;\n  delimiter $$')).toBe(true)
    expect(hasDelimiterCommand("select 'delimiter x'")).toBe(false)
    expect(hasDelimiterCommand('select delimiter from t')).toBe(false)
  })

  it('reports a library error with its first line only', async () => {
    const res = await formatSql('select $$ from', 'sqlite', upper2)
    expect(res.ok).toBe(false)
    if (!res.ok) {
      expect(res.reason).toMatch(/^Could not format the SQL: Parse error/)
      expect(res.reason).not.toContain('\n')
    }
    const empty = await formatSql('x', 'sqlite', upper2, async () => ({
      format: () => {
        throw ''
      },
    }))
    expect(empty).toEqual({ ok: false, reason: 'Could not format the SQL.' })
    const failedLoad = await formatSql('x', 'sqlite', upper2, () => Promise.reject(new Error('chunk failed')))
    expect(failedLoad).toEqual({ ok: false, reason: 'Could not format the SQL: chunk failed' })
  })
})

describe('mapCursor', () => {
  it('keeps the cursor after the same non-blank characters', () => {
    const before = 'select a, b from t'
    const after = 'SELECT\n  a,\n  b\nFROM\n  t'
    expect(mapCursor(before, after, 0)).toBe(0)
    // After "select a," (8 non-blank characters).
    expect(mapCursor(before, after, 9)).toBe(after.indexOf(',') + 1)
    expect(mapCursor(before, after, before.length)).toBe(after.length)
    // More non-blank characters than the result has: the end.
    expect(mapCursor('abc', 'ab', 3)).toBe(2)
  })
})

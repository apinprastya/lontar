import { afterEach, describe, expect, it, vi } from 'vitest'
import { migrateLegacyStorage } from './legacyStorage'

describe('migrateLegacyStorage', () => {
  afterEach(() => {
    vi.restoreAllMocks()
    window.localStorage.clear()
  })

  it('moves dbedit.* keys to lontar.* and removes the old ones', () => {
    const s = window.localStorage
    s.setItem('dbedit.queryDrafts.c1', '[{"title":"Q","sql":"SELECT 1"}]')
    s.setItem('dbedit.theme', 'dark')
    s.setItem('unrelated', 'x')
    expect(migrateLegacyStorage()).toBe(2)
    expect(s.getItem('lontar.queryDrafts.c1')).toBe('[{"title":"Q","sql":"SELECT 1"}]')
    expect(s.getItem('lontar.theme')).toBe('dark')
    expect(s.getItem('dbedit.queryDrafts.c1')).toBeNull()
    expect(s.getItem('dbedit.theme')).toBeNull()
    expect(s.getItem('unrelated')).toBe('x')
  })

  it('never overwrites an existing lontar.* key', () => {
    const s = window.localStorage
    s.setItem('dbedit.queryDrafts.c1', 'old')
    s.setItem('lontar.queryDrafts.c1', 'new')
    expect(migrateLegacyStorage(s)).toBe(0)
    expect(s.getItem('lontar.queryDrafts.c1')).toBe('new')
    expect(s.getItem('dbedit.queryDrafts.c1')).toBeNull()
  })

  it('is idempotent', () => {
    window.localStorage.setItem('dbedit.a', '1')
    expect(migrateLegacyStorage()).toBe(1)
    expect(migrateLegacyStorage()).toBe(0)
    expect(window.localStorage.getItem('lontar.a')).toBe('1')
  })

  it('keeps the old key when the copy fails', () => {
    const data = new Map<string, string>([['dbedit.a', '1']])
    const failing = {
      get length() { return data.size },
      key: (i: number) => [...data.keys()][i] ?? null,
      getItem: (k: string) => data.get(k) ?? null,
      setItem: () => { throw new Error('QuotaExceededError') },
      removeItem: (k: string) => { data.delete(k) },
      clear: () => data.clear(),
    } as Storage
    expect(migrateLegacyStorage(failing)).toBe(0)
    expect(data.get('dbedit.a')).toBe('1')
  })

  it('survives storage that is unavailable', () => {
    vi.spyOn(window, 'localStorage', 'get').mockImplementation(() => {
      throw new Error('SecurityError')
    })
    expect(migrateLegacyStorage()).toBe(0)
  })
})

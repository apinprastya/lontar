import { beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('../../../bindings/lontar/internal/services', () => import('../../test/mockBindings'))

import { SQLFileService, resetBindings } from '../../test/mockBindings'
import { fileErrorCode, sqlFileApi } from './sqlFiles'

const file = { path: '/w/a.sql', name: 'a.sql', text: 'x', modTime: 5, eol: '\n' }

describe('sqlFileApi', () => {
  beforeEach(resetBindings)

  it('wraps the SQLFileService bindings', async () => {
    expect(await sqlFileApi.open()).toBeNull()
    SQLFileService.OpenSQLFile.mockResolvedValueOnce(file)
    expect(await sqlFileApi.open()).toEqual(file)

    SQLFileService.ReadSQLFile.mockResolvedValueOnce(file)
    expect(await sqlFileApi.read('/w/a.sql')).toEqual(file)
    expect(SQLFileService.ReadSQLFile).toHaveBeenCalledWith('/w/a.sql')
    SQLFileService.ReadSQLFile.mockResolvedValueOnce(null)
    await expect(sqlFileApi.read('/w/a.sql')).rejects.toThrow('empty response')

    expect(await sqlFileApi.save('', 'Query 1', 'x', '\n', 0)).toBeNull()
    SQLFileService.SaveSQLFile.mockResolvedValueOnce(file)
    expect(await sqlFileApi.save('/w/a.sql', 'a', 'x', '\r\n', 9)).toEqual(file)
    expect(SQLFileService.SaveSQLFile).toHaveBeenLastCalledWith('/w/a.sql', 'a', 'x', '\r\n', 9)
  })
})

describe('fileErrorCode', () => {
  it('reads the code of a structured file error', () => {
    const cause = { kind: 'fileError', code: 'changed_on_disk', path: '/x', message: 'm' }
    expect(fileErrorCode({ cause })).toBe('changed_on_disk')
    expect(fileErrorCode({ cause: JSON.stringify({ ...cause, code: 'too_large' }) })).toBe('too_large')
    expect(fileErrorCode({ cause: { ...cause, code: 'other' } })).toBeNull()
    expect(fileErrorCode({ cause: { kind: 'hostKeyUnknown' } })).toBeNull()
    expect(fileErrorCode({ cause: '{nope' })).toBeNull()
    expect(fileErrorCode({ cause: null })).toBeNull()
    expect(fileErrorCode(new Error('plain'))).toBeNull()
    expect(fileErrorCode('text')).toBeNull()
    expect(fileErrorCode(null)).toBeNull()
  })
})

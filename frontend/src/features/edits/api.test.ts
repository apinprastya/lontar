import { beforeEach, describe, expect, it, vi } from 'vitest'
vi.mock('../../../bindings/lontar/internal/services', () => import('../../test/mockBindings'))
import { EditService, resetBindings } from '../../test/mockBindings'
import { editApi } from './api'
import { safeModeLabel } from './types'

const cs = { schema: 's', table: 't', updates: [], inserts: [], deletes: [] }

describe('editApi', () => {
  beforeEach(resetBindings)

  it('previews (null → [])', async () => {
    EditService.Preview.mockResolvedValue([{ op: 'insert', index: 0, sql: 'X;' }])
    expect(await editApi.preview('s1', cs)).toEqual([{ op: 'insert', index: 0, sql: 'X;' }])
    expect(EditService.Preview).toHaveBeenCalledWith('s1', cs)
    EditService.Preview.mockResolvedValue(null)
    expect(await editApi.preview('s1', cs)).toEqual([])
  })

  it('applies and normalises nulls', async () => {
    EditService.Apply.mockResolvedValue({ ok: true, result: { deleted: 1, updated: 0, inserted: 0, insertedKeys: null }, failure: null })
    expect(await editApi.apply('s1', cs)).toEqual({ ok: true, result: { deleted: 1, updated: 0, inserted: 0, insertedKeys: [] }, failure: null })
    EditService.Apply.mockResolvedValue({ ok: false, result: null, failure: { index: 0, op: 'update', kind: 'stale', message: 'm' } })
    expect(await editApi.apply('s1', cs)).toEqual({ ok: false, result: null, failure: { index: 0, op: 'update', kind: 'stale', message: 'm' } })
    EditService.Apply.mockResolvedValue({ ok: false })
    expect((await editApi.apply('s1', cs)).failure).toBeNull()
    EditService.Apply.mockResolvedValue(null)
    await expect(editApi.apply('s1', cs)).rejects.toThrow('empty response')
  })

  it('labels safe modes', () => {
    expect(safeModeLabel('confirm')).toBe('Confirm writes')
    expect(safeModeLabel('readonly')).toBe('Read-only')
    expect(safeModeLabel(undefined)).toBe('Off')
  })
})

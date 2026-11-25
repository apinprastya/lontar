import { beforeEach, describe, expect, it, vi } from 'vitest'
vi.mock('../../../bindings/lontar/internal/services', () => import('../../test/mockBindings'))
import { StructureEditService, resetBindings } from '../../test/mockBindings'
import { structureEditApi } from './editApi'
import type { TableChange } from './editTypes'

const tc: TableChange = {
  schema: 's', table: 't', create: false, base: null, newName: null, comment: null, columns: [], primaryKey: null, indexes: [], foreignKeys: [], checks: [], dropTriggers: [],
}

describe('structureEditApi', () => {
  beforeEach(resetBindings)

  it('normalises capabilities', async () => {
    StructureEditService.Capabilities.mockResolvedValue({ flavor: 'sqlite', indexMethods: null, types: null })
    expect(await structureEditApi.capabilities('s1')).toMatchObject({ flavor: 'sqlite', indexMethods: [], types: [] })
    StructureEditService.Capabilities.mockResolvedValue(null)
    await expect(structureEditApi.capabilities('s1')).rejects.toThrow('empty response')
  })

  it('previews and applies', async () => {
    StructureEditService.Preview.mockResolvedValue({ statements: null, failure: null })
    expect(await structureEditApi.preview('s1', tc)).toEqual({ statements: [], failure: null })
    expect(StructureEditService.Preview).toHaveBeenCalledWith('s1', tc)
    const failure = { kind: 'invalid', section: 'columns', index: 0, field: 'name', message: 'm', sql: '', applied: 0 }
    StructureEditService.Preview.mockResolvedValue({ statements: [], failure })
    expect((await structureEditApi.preview('s1', tc)).failure).toEqual(failure)
    StructureEditService.Preview.mockResolvedValue(null)
    await expect(structureEditApi.preview('s1', tc)).rejects.toThrow('empty response')

    StructureEditService.Apply.mockResolvedValue({ ok: true, result: { statements: [{ sql: 'X', section: 'table', index: -1 }] }, failure: null })
    expect(await structureEditApi.apply('s1', tc)).toEqual({ ok: true, statements: [{ sql: 'X', section: 'table', index: -1 }], failure: null })
    StructureEditService.Apply.mockResolvedValue({ ok: false, result: null, failure })
    expect(await structureEditApi.apply('s1', tc)).toEqual({ ok: false, statements: [], failure })
    StructureEditService.Apply.mockResolvedValue(null)
    await expect(structureEditApi.apply('s1', tc)).rejects.toThrow('empty response')
  })

  it('drops tables', async () => {
    StructureEditService.DropTablePreview.mockResolvedValue({ statements: [{ sql: 'DROP TABLE t', section: 'table', index: -1 }] })
    expect((await structureEditApi.dropTablePreview('s1', 's', 't', true)).statements[0].sql).toBe('DROP TABLE t')
    expect(StructureEditService.DropTablePreview).toHaveBeenCalledWith('s1', 's', 't', true)
    StructureEditService.DropTable.mockResolvedValue({ ok: true, result: null })
    expect(await structureEditApi.dropTable('s1', 's', 't', false)).toEqual({ ok: true, statements: [], failure: null })
  })
})

import { beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('../../../bindings/dbedit/internal/services', () => import('../../test/mockBindings'))

import { BrowseService, SchemaService, resetBindings } from '../../test/mockBindings'
import { workspaceApi } from './api'
import type { FetchRequest } from './types'

const req: FetchRequest = { schema: 's', table: 't', filters: [], rawWhere: '', sort: [], offset: 0, limit: 300 }

describe('workspaceApi', () => {
  beforeEach(resetBindings)

  it('wraps the schema service', async () => {
    expect(await workspaceApi.current('s1')).toEqual({ database: 'shop', schema: 'public' })
    SchemaService.ListDatabases.mockResolvedValue(null)
    expect(await workspaceApi.listDatabases('s1', true)).toEqual([])
    expect(SchemaService.ListDatabases).toHaveBeenCalledWith('s1', true)
    SchemaService.ListSchemas.mockResolvedValue(null)
    expect(await workspaceApi.listSchemas('s1', false)).toEqual([])
    SchemaService.ListTables.mockResolvedValue(null)
    expect(await workspaceApi.listTables('s1', 'public')).toEqual([])
    SchemaService.UseDatabase.mockResolvedValue({ database: 'b', schema: 'b' })
    expect(await workspaceApi.useDatabase('s1', 'b')).toEqual({ database: 'b', schema: 'b' })
    expect(await workspaceApi.listDatabases('s1', false)).toEqual([])
    SchemaService.ListDatabases.mockResolvedValue(['x'])
    expect(await workspaceApi.listDatabases('s1', false)).toEqual(['x'])
    SchemaService.ListSchemas.mockResolvedValue(['p'])
    expect(await workspaceApi.listSchemas('s1', false)).toEqual(['p'])
    SchemaService.ListTables.mockResolvedValue([{ schema: 'p', name: 't', type: 'table', estimatedRows: null }])
    expect(await workspaceApi.listTables('s1', 'p')).toHaveLength(1)
  })

  it('wraps the browse service and normalises nulls', async () => {
    BrowseService.FetchTable.mockResolvedValue({ columns: null, rows: null, offset: 0, limit: 300, hasMore: false, totalCount: null, elapsedMs: 1 } as never)
    const p = await workspaceApi.fetchTable('s1', 'r1', req)
    expect(p.columns).toEqual([])
    expect(p.rows).toEqual([])
    expect(p.totalCount).toBeNull()
    expect(BrowseService.FetchTable).toHaveBeenCalledWith('s1', 'r1', req)
    BrowseService.FetchTable.mockResolvedValue(null)
    await expect(workspaceApi.fetchTable('s1', 'r1', req)).rejects.toThrow('empty response')
    BrowseService.CountRows.mockResolvedValue(42)
    expect(await workspaceApi.countRows('s1', 'r2', req)).toBe(42)
    expect(await workspaceApi.cancel('r1')).toBe(true)
    expect(BrowseService.CancelFetch).toHaveBeenCalledWith('r1')
  })
})

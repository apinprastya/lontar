import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { WorkspaceApi } from '../features/workspace/api'
import type { DbObject, ObjectRef } from '../features/objects/types'
import { makeObject } from '../test/objects'
import { createWorkspaceStore, type ObjectTabState, type WorkspaceStore } from './workspace'

const tick = () => new Promise((r) => setTimeout(r, 0))

const seq = makeObject({ kind: 'sequence', name: 'ticket_seq', id: '10' })
const fn = makeObject({ name: 'add_one', signature: 'x integer', id: '11' })

function fakeApi(objects: DbObject[] = [seq, fn]) {
  return {
    current: vi.fn(async () => ({ database: 'shop', schema: 'public' })),
    listDatabases: vi.fn(async () => ['shop', 'other']),
    useDatabase: vi.fn(async (_s: string, db: string) => ({ database: db, schema: 'public' })),
    listSchemas: vi.fn(async () => ['public', 'geo']),
    listTables: vi.fn(async () => [{ schema: 'public', name: 'tickets', type: 'table' as const, estimatedRows: 1 }]),
    fetchTable: vi.fn(async () => ({ columns: [], rows: [], offset: 0, limit: 300, hasMore: false, totalCount: 0, elapsedMs: 1 })),
    countRows: vi.fn(async () => 0),
    cancel: vi.fn(async () => true),
    tableInfo: vi.fn(),
    tableDDL: vi.fn(async () => ''),
    listObjects: vi.fn(async (_s: string, schema: string) => (schema === 'public' ? objects : [])),
    objectDDL: vi.fn(async (_s: string, _schema: string, ref: ObjectRef) => `CREATE ${ref.kind} ${ref.name} -- ${ref.id}`),
  } satisfies WorkspaceApi
}

describe('objects in the workspace store', () => {
  let api: ReturnType<typeof fakeApi>
  let store: WorkspaceStore
  const objectTabs = () => store.getState().tabs.filter((t): t is ObjectTabState => t.kind === 'object')

  beforeEach(async () => {
    api = fakeApi()
    store = createWorkspaceStore({ id: 's1', kind: 'postgres', safeMode: 'off' }, api)
    await store.getState().init()
  })

  it('loads the objects with the tables, per schema', async () => {
    expect(store.getState().objects).toEqual([seq, fn])
    expect(store.getState().objectsLoading).toBe(false)
    await store.getState().selectSchema('geo')
    expect(api.listObjects).toHaveBeenLastCalledWith('s1', 'geo')
    expect(store.getState().objects).toEqual([])
  })

  it('reports a listing error in the sidebar', async () => {
    api.listObjects.mockRejectedValueOnce(new Error('no access'))
    await store.getState().refreshTables()
    expect(store.getState().objects).toEqual([])
    expect(store.getState().sidebarError).toBe('no access')
    expect(store.getState().tables).toHaveLength(1)
  })

  it('opens one tab per object and loads its DDL', async () => {
    store.getState().openObject(seq)
    await tick()
    expect(objectTabs()).toHaveLength(1)
    const t = objectTabs()[0]
    expect(store.getState().activeTabId).toBe(t.id)
    expect(t.ddl).toBe('CREATE sequence ticket_seq -- 10')
    expect(api.objectDDL).toHaveBeenCalledWith('s1', 'public', { kind: 'sequence', name: 'ticket_seq', signature: '', table: '', id: '10' })
    store.getState().openObject(fn)
    await tick()
    store.getState().openObject({ ...seq, id: '99' }) // same object, newer listing
    expect(objectTabs()).toHaveLength(2)
    expect(store.getState().activeTabId).toBe(t.id)
  })

  it('keeps a DDL error on the tab and retries', async () => {
    api.objectDDL.mockRejectedValueOnce(new Error('object not found'))
    store.getState().openObject(seq)
    await tick()
    expect(objectTabs()[0].ddlError).toBe('object not found')
    await store.getState().loadObjectDDL(objectTabs()[0].id)
    expect(objectTabs()[0].ddlError).toBeNull()
    expect(objectTabs()[0].ddl).toContain('ticket_seq')
    await store.getState().loadObjectDDL('nope')
  })

  it('refresh follows a re-created object and reloads its DDL', async () => {
    store.getState().openObject(seq)
    await tick()
    api.listObjects.mockResolvedValue([{ ...seq, id: '20' }, fn])
    await store.getState().refresh()
    expect(objectTabs()[0].object.id).toBe('20')
    expect(objectTabs()[0].ddl).toBe('CREATE sequence ticket_seq -- 20')
    // Dropped: the tab keeps the old object and shows the error.
    api.listObjects.mockResolvedValue([fn])
    api.objectDDL.mockRejectedValueOnce(new Error('object not found'))
    await store.getState().refresh()
    expect(objectTabs()[0].object.id).toBe('20')
    expect(objectTabs()[0].ddlError).toBe('object not found')
  })

  it('closes object tabs on a database switch and when closed', async () => {
    store.getState().openObject(seq)
    store.getState().newQueryTab('select 1')
    await tick()
    await store.getState().selectDatabase('other')
    expect(objectTabs()).toHaveLength(0)
    expect(store.getState().tabs).toHaveLength(1)
    await store.getState().selectDatabase('shop')
    store.getState().openObject(fn)
    await tick()
    store.getState().closeTab(objectTabs()[0].id)
    expect(objectTabs()).toHaveLength(0)
    store.getState().dispose()
  })

  it('ignores answers after dispose', async () => {
    let resolve: (s: string) => void = () => {}
    api.objectDDL.mockImplementationOnce(() => new Promise((r) => (resolve = r)))
    store.getState().openObject(seq)
    store.getState().dispose()
    resolve('late')
    await tick()
    expect(objectTabs()[0].ddl).toBeNull()
  })
})

import { beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('../lib/clipboard', () => ({ copyText: vi.fn(async () => {}) }))

import { copyText } from '../lib/clipboard'
import type { WorkspaceApi } from '../features/workspace/api'
import type { Cell, FetchRequest } from '../features/workspace/types'
import { makeDetail, makeFK } from '../test/structure'
import { createWorkspaceStore, type TabState, type WorkspaceStore } from './workspace'
import { isDirty } from './pending'

const tick = () => new Promise((r) => setTimeout(r, 0))

const tables = [
  { schema: 'public', name: 'orders', type: 'table' as const, estimatedRows: 5 },
  { schema: 'public', name: 'customers', type: 'table' as const, estimatedRows: 2 },
]
const orderFK = makeFK({ name: 'o_c', table: 'orders', columns: ['customer_id'], refTable: 'customers', refColumns: ['id'] })
const compFK = makeFK({ name: 'o_l', table: 'orders', columns: ['customer_id', 'region'], refSchema: 'geo', refTable: 'regions', refColumns: ['cust', 'code'] })

function fakeApi() {
  return {
    current: vi.fn(async () => ({ database: 'shop', schema: 'public' })),
    listDatabases: vi.fn(async () => ['shop']),
    useDatabase: vi.fn(async (_s: string, db: string) => ({ database: db, schema: 'public' })),
    listSchemas: vi.fn(async () => ['public', 'geo']),
    listTables: vi.fn(async () => tables),
    fetchTable: vi.fn(async (_s: string, _r: string, req: FetchRequest) => ({
      columns: [
        { name: 'id', type: 'integer', isPK: true, nullable: false },
        { name: 'customer_id', type: 'integer', isPK: false, nullable: true },
        { name: 'region', type: 'text', isPK: false, nullable: true },
      ],
      rows: [
        [{ k: 'num', v: 1 }, { k: 'num', v: 7 }, { k: 'str', v: 'EU' }],
        [{ k: 'num', v: 2 }, { k: 'null' }, { k: 'str', v: 'US' }],
      ] as Cell[][],
      offset: req.offset,
      limit: req.limit,
      hasMore: false,
      totalCount: 2,
      elapsedMs: 1,
    })),
    countRows: vi.fn(async () => 2),
    cancel: vi.fn(async () => true),
    tableInfo: vi.fn(async (_s: string, schema: string, table: string) =>
      makeDetail({ schema, name: table, foreignKeys: table === 'orders' ? [orderFK, compFK] : [] }),
    ),
    tableDDL: vi.fn(async (_s: string, _schema: string, table: string) => `CREATE TABLE ${table} ();`),
  } satisfies WorkspaceApi
}

describe('structure in the workspace store', () => {
  let api: ReturnType<typeof fakeApi>
  let store: WorkspaceStore
  const tabs = () => store.getState().tabs.filter((t): t is TabState => t.kind === 'table')
  const tab = (name: string) => tabs().find((t) => t.table === name) as TabState
  const lastRequest = () => api.fetchTable.mock.calls[api.fetchTable.mock.calls.length - 1]?.[2]

  beforeEach(async () => {
    api = fakeApi()
    store = createWorkspaceStore({ id: 's1', kind: 'postgres', safeMode: 'off' }, api)
    await store.getState().init()
    store.getState().openTable(tables[0], true)
    await tick()
  })

  it('loads the structure with the tab (for foreign keys) and caches it', async () => {
    expect(api.tableInfo).toHaveBeenCalledWith('s1', 'public', 'orders')
    expect(tab('orders').structure?.foreignKeys).toHaveLength(2)
    expect(tab('orders').view).toBe('data')
    store.getState().setTabView(tab('orders').id, 'structure')
    expect(tab('orders').view).toBe('structure')
    expect(api.tableInfo).toHaveBeenCalledTimes(1) // cached
    store.getState().toggleTabView(tab('orders').id)
    expect(tab('orders').view).toBe('data')
    store.getState().setTabView(tab('orders').id, 'data') // no-op
    store.getState().toggleTabView('nope')
    store.getState().setStructureSection('nope', 'ddl')
    await store.getState().loadStructure('nope')
    await store.getState().loadDDL('nope')
    expect(api.tableDDL).not.toHaveBeenCalled()
  })

  it('loads the DDL lazily and reports errors', async () => {
    const id = tab('orders').id
    store.getState().setStructureSection(id, 'indexes')
    expect(api.tableDDL).not.toHaveBeenCalled()
    store.getState().setStructureSection(id, 'ddl')
    await tick()
    expect(tab('orders').ddl).toBe('CREATE TABLE orders ();')
    store.getState().setStructureSection(id, 'ddl')
    expect(api.tableDDL).toHaveBeenCalledTimes(1)
    api.tableDDL.mockRejectedValueOnce(new Error('no ddl'))
    await store.getState().loadDDL(id)
    expect(tab('orders').ddlError).toBe('no ddl')
    api.tableInfo.mockRejectedValueOnce(new Error('no info'))
    await store.getState().loadStructure(id)
    expect(tab('orders').structureError).toBe('no info')
    expect(tab('orders').structure).not.toBeNull() // the cached one stays
  })

  it('reloads the DDL when switching to a structure view left on the DDL section', async () => {
    const id = tab('orders').id
    store.getState().setStructureSection(id, 'ddl')
    await tick()
    // A data refresh drops the cached DDL.
    await store.getState().refresh()
    expect(tab('orders').ddl).toBeNull()
    store.getState().setTabView(id, 'structure')
    await tick()
    expect(api.tableDDL).toHaveBeenCalledTimes(2)
    expect(tab('orders').ddl).toBe('CREATE TABLE orders ();')
  })

  it('refreshes the structure (and loaded DDL) without the guard in the structure view', async () => {
    const id = tab('orders').id
    store.getState().stageCells(id, [{ ref: { type: 'row', index: 0 }, column: 'region', value: { kind: 'value', text: 'X' } }])
    store.getState().setTabView(id, 'structure')
    store.getState().setStructureSection(id, 'ddl')
    await tick()
    api.fetchTable.mockClear()
    await store.getState().refresh()
    expect(store.getState().guard).toBeNull()
    expect(api.tableInfo).toHaveBeenCalledTimes(2)
    expect(api.tableDDL).toHaveBeenCalledTimes(2)
    expect(api.fetchTable).not.toHaveBeenCalled()
    expect(isDirty(tab('orders').pending)).toBe(true)
  })

  it('opens a referenced row in a new tab with bound filters', async () => {
    const id = tab('orders').id
    const row = tab('orders').rows[0]
    expect(store.getState().followReference(id, orderFK, row)).toBe(true)
    await tick()
    const c = tab('customers')
    expect(store.getState().activeTabId).toBe(c.id)
    expect(c.pinned && c.filterOpen && c.view === 'data').toBe(true)
    expect(c.appliedFilters).toEqual([{ column: 'id', op: '=', value: '7', value2: '' }])
    expect(c.draftFilters.map((f) => [f.column, f.op, f.value])).toEqual([['id', '=', '7']])
    const req = lastRequest() as FetchRequest
    expect(req).toMatchObject({ schema: 'public', table: 'customers', filters: [{ column: 'id', op: '=', value: '7', value2: '' }], rawWhere: '' })
    expect(api.tableInfo).toHaveBeenCalledWith('s1', 'public', 'customers')
    expect(c.estimatedRows).toBe(2) // from the sidebar list
  })

  it('opens composite and cross-schema references with one filter per column', async () => {
    const id = tab('orders').id
    expect(store.getState().followReference(id, compFK, tab('orders').rows[0])).toBe(true)
    await tick()
    const r = tab('regions')
    expect(r.schema).toBe('geo')
    expect(r.type).toBe('table')
    expect(r.appliedFilters).toEqual([
      { column: 'cust', op: '=', value: '7', value2: '' },
      { column: 'code', op: '=', value: 'EU', value2: '' },
    ])
    expect(lastRequest()).toMatchObject({ schema: 'geo', table: 'regions' })
  })

  it('does nothing for NULL keys or unknown tabs', () => {
    const id = tab('orders').id
    expect(store.getState().followReference(id, orderFK, tab('orders').rows[1])).toBe(false)
    expect(store.getState().followReference('nope', orderFK, tab('orders').rows[0])).toBe(false)
    expect(tabs()).toHaveLength(1)
  })

  it('reuses an open tab, asking first when it has staged changes', async () => {
    const orders = tab('orders').id
    store.getState().openTable(tables[1], true)
    await tick()
    const cust = tab('customers').id
    // No staged changes: reused directly.
    store.getState().followReference(orders, orderFK, tab('orders').rows[0])
    await tick()
    expect(tabs()).toHaveLength(2)
    expect(store.getState().activeTabId).toBe(cust)
    expect(tab('customers').appliedFilters).toHaveLength(1)

    store.getState().clearFilters(cust)
    await tick()
    store.getState().stageCells(cust, [{ ref: { type: 'row', index: 0 }, column: 'region', value: { kind: 'value', text: 'X' } }])
    store.getState().activateTab(orders)
    store.getState().followReference(orders, orderFK, tab('orders').rows[0])
    expect(store.getState().guard?.tabIds).toEqual([cust])
    await store.getState().resolveGuard('cancel')
    expect(tab('customers').appliedFilters).toEqual([])
    expect(store.getState().activeTabId).toBe(orders)

    store.getState().followReference(orders, orderFK, tab('orders').rows[0])
    await store.getState().resolveGuard('discard')
    await tick()
    expect(isDirty(tab('customers').pending)).toBe(false)
    expect(tab('customers').appliedFilters).toEqual([{ column: 'id', op: '=', value: '7', value2: '' }])
    expect(store.getState().activeTabId).toBe(cust)

    // The tab went away while the guard was open.
    store.getState().stageCells(cust, [{ ref: { type: 'row', index: 0 }, column: 'region', value: { kind: 'value', text: 'Y' } }])
    store.getState().followReference(orders, orderFK, tab('orders').rows[0])
    const g = store.getState().guard
    store.setState({ tabs: store.getState().tabs.filter((t) => t.id !== cust), guard: null, activeTabId: orders })
    const calls = api.fetchTable.mock.calls.length
    g?.proceed()
    expect(store.getState().activeTabId).toBe(orders)
    expect(api.fetchTable.mock.calls.length).toBe(calls)
  })

  it('opens tables in a view from the sidebar menu and copies DDL', async () => {
    store.getState().openTableView('public', 'customers', 'structure')
    await tick()
    expect(tab('customers').view).toBe('structure')
    expect(tab('customers').pinned).toBe(true)
    store.getState().openTableView('public', 'customers')
    expect(tab('customers').view).toBe('data')
    expect(await store.getState().copyDDL('public', 'orders')).toBe(true)
    expect(copyText).toHaveBeenCalledWith('CREATE TABLE orders ();')
    api.tableDDL.mockRejectedValueOnce(new Error('denied'))
    expect(await store.getState().copyDDL('public', 'orders')).toBe(false)
    expect(store.getState().sidebarError).toBe('denied')
  })

  it('ignores late structure results after dispose', async () => {
    let resolve: (v: ReturnType<typeof makeDetail>) => void = () => {}
    api.tableInfo.mockImplementationOnce(() => new Promise((r) => (resolve = r)))
    api.tableDDL.mockRejectedValueOnce(new Error('late'))
    const id = tab('orders').id
    const p = store.getState().loadStructure(id)
    const q = store.getState().loadDDL(id)
    store.getState().dispose()
    resolve(makeDetail({ name: 'late' }))
    await p
    await q
    expect(tab('orders').structure?.name).toBe('orders')
    expect(tab('orders').ddlError).toBeNull()
  })

  it('ignores late structure errors after dispose', async () => {
    api.tableInfo.mockRejectedValueOnce(new Error('late'))
    api.tableDDL.mockResolvedValueOnce('late')
    const id = tab('orders').id
    const p = store.getState().loadStructure(id)
    const q = store.getState().loadDDL(id)
    store.getState().dispose()
    await p
    await q
    expect(tab('orders').structureError).toBeNull()
    expect(tab('orders').ddl).toBeNull()
  })
})

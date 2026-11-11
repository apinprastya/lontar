import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { WorkspaceApi } from '../features/workspace/api'
import type { FetchRequest, Page, TableInfo } from '../features/workspace/types'
import { createWorkspaceStore, cycleSort, effectiveFilters, filterKey, newFilterRow, newRequestId, tabRequest, type TabState, type WorkspaceStore } from './workspace'

const T = (name: string, type: TableInfo['type'] = 'table', estimatedRows: number | null = 1000): TableInfo => ({
  schema: 'public',
  name,
  type,
  estimatedRows,
})

function page(req: FetchRequest, over: Partial<Page> = {}): Page {
  return {
    columns: [
      { name: 'id', type: 'integer', isPK: true, nullable: false },
      { name: 'name', type: 'text', isPK: false, nullable: true },
    ],
    rows: [
      [{ k: 'num', v: req.offset + 1 }, { k: 'str', v: 'a' }],
      [{ k: 'num', v: req.offset + 2 }, { k: 'null' }],
    ],
    offset: req.offset,
    limit: req.limit,
    hasMore: true,
    totalCount: null,
    elapsedMs: 5,
    ...over,
  }
}

function fakeApi() {
  const api = {
    current: vi.fn(async () => ({ database: 'shop', schema: 'public' })),
    listDatabases: vi.fn(async () => ['postgres', 'shop']),
    useDatabase: vi.fn(async (_s: string, db: string) => ({ database: db, schema: 'public' })),
    listSchemas: vi.fn(async () => ['other', 'public']),
    listTables: vi.fn(async () => [T('users'), T('orders'), T('v_users', 'view', null)]),
    fetchTable: vi.fn(async (_s: string, _r: string, req: FetchRequest) => page(req)),
    countRows: vi.fn(async (_s: string, _r: string, _req: FetchRequest) => 12345),
    cancel: vi.fn(async () => true),
  } satisfies WorkspaceApi
  return api
}

const flush = () => new Promise((r) => setTimeout(r, 0))

describe('pure helpers', () => {
  it('cycles a single sort asc → desc → none', () => {
    let s = cycleSort([], 'a', false)
    expect(s).toEqual([{ column: 'a', desc: false }])
    s = cycleSort(s, 'a', false)
    expect(s).toEqual([{ column: 'a', desc: true }])
    expect(cycleSort(s, 'a', false)).toEqual([])
    // A plain click on another column replaces the sort.
    expect(cycleSort([{ column: 'a', desc: true }, { column: 'b', desc: false }], 'c', false)).toEqual([{ column: 'c', desc: false }])
  })

  it('adds and cycles secondary sorts with shift', () => {
    let s = cycleSort([{ column: 'a', desc: false }], 'b', true)
    expect(s).toEqual([
      { column: 'a', desc: false },
      { column: 'b', desc: false },
    ])
    s = cycleSort(s, 'b', true)
    expect(s[1]).toEqual({ column: 'b', desc: true })
    s = cycleSort(s, 'a', true)
    expect(s).toEqual([
      { column: 'a', desc: true },
      { column: 'b', desc: true },
    ])
    expect(cycleSort(s, 'a', true)).toEqual([{ column: 'b', desc: true }])
  })

  it('builds effective filters', () => {
    const rows = [
      { ...newFilterRow('a', 'isNull'), value: 'x' },
      { ...newFilterRow('b', 'between'), value: '1', value2: '2' },
      { ...newFilterRow('c', '='), value: '3', value2: 'ignored' },
      newFilterRow(''),
    ]
    expect(effectiveFilters(rows)).toEqual([
      { column: 'a', op: 'isNull', value: '', value2: '' },
      { column: 'b', op: 'between', value: '1', value2: '2' },
      { column: 'c', op: '=', value: '3', value2: '' },
    ])
  })

  it('makes unique request ids', () => {
    expect(newRequestId()).not.toBe(newRequestId())
    expect(newRequestId('count')).toMatch(/^count-/)
    expect(filterKey({ appliedFilters: [], appliedRawWhere: 'x' })).toBe('[[],"x"]')
  })
})

describe('workspace store', () => {
  let api: ReturnType<typeof fakeApi>
  let store: WorkspaceStore
  const st = () => store.getState()
  const tab = () => st().activeTab()! as TabState

  beforeEach(() => {
    api = fakeApi()
    store = createWorkspaceStore({ id: 's1', kind: 'postgres' }, api)
  })

  it('initialises databases, schemas and tables', async () => {
    await st().init()
    expect(st().database).toBe('shop')
    expect(st().databases).toEqual(['postgres', 'shop'])
    expect(st().schema).toBe('public')
    expect(st().tables.map((t) => t.name)).toEqual(['users', 'orders', 'v_users'])
    expect(st().tablesLoading).toBe(false)
    expect(api.listTables).toHaveBeenCalledWith('s1', 'public')
  })

  it('falls back to the first schema and handles no schemas', async () => {
    api.current.mockResolvedValue({ database: 'x', schema: 'missing' })
    await st().init()
    expect(st().schema).toBe('other')
    api.listSchemas.mockResolvedValue([])
    api.listTables.mockClear()
    await st().init()
    // The previously chosen schema is gone: no schema, no tables query.
    expect(st().schema).toBe('')
    expect(api.listTables).not.toHaveBeenCalled()
    expect(st().tables).toEqual([])
  })

  it('shows init and table errors inline', async () => {
    api.current.mockRejectedValue(new Error('boom'))
    await st().init()
    expect(st().sidebarError).toBe('boom')
    api.current.mockResolvedValue({ database: 'shop', schema: 'public' })
    api.listTables.mockRejectedValue(new Error('no access'))
    await st().refreshTables()
    expect(st().sidebarError).toBe('no access')
    expect(st().tables).toEqual([])
  })

  it('switches database, closing tabs', async () => {
    await st().init()
    st().openTable(T('users'), true)
    await flush()
    const reqId = 'pending'
    store.setState({ tabs: st().tabs.map((t) => ({ ...t, requestId: reqId })) })
    await st().selectDatabase('postgres')
    expect(api.useDatabase).toHaveBeenCalledWith('s1', 'postgres')
    expect(api.cancel).toHaveBeenCalledWith(reqId)
    expect(st().tabs).toEqual([])
    expect(st().database).toBe('shop') // init re-reads current() from the fake
    await st().selectDatabase('shop') // same database: no-op
    expect(api.useDatabase).toHaveBeenCalledTimes(1)
    api.useDatabase.mockRejectedValue(new Error('denied'))
    await st().selectDatabase('postgres')
    expect(st().sidebarError).toBe('denied')
  })

  it('switches schema and system visibility', async () => {
    await st().init()
    await st().selectSchema('other')
    expect(api.listTables).toHaveBeenLastCalledWith('s1', 'other')
    await st().setShowSystem(true)
    expect(api.listSchemas).toHaveBeenLastCalledWith('s1', true)
    expect(st().schema).toBe('other')
    st().setSearch('us')
    expect(st().search).toBe('us')
  })

  it('previews on single click and pins on double click', async () => {
    st().openTable(T('users'))
    expect(st().tabs).toHaveLength(1)
    expect(tab().pinned).toBe(false)
    const first = tab().id
    // Another single click replaces the preview tab.
    st().openTable(T('orders'))
    expect(st().tabs).toHaveLength(1)
    expect(tab().table).toBe('orders')
    expect(tab().id).not.toBe(first)
    // Double click pins it; the next preview opens a new tab.
    st().openTable(T('orders'), true)
    expect(st().tabs).toHaveLength(1)
    expect(tab().pinned).toBe(true)
    st().openTable(T('users'))
    expect(st().tabs.map((t) => [(t as TabState).table, t.pinned])).toEqual([
      ['orders', true],
      ['users', false],
    ])
    // Opening an open table activates it.
    st().openTable(T('orders'))
    expect(tab().table).toBe('orders')
    st().pinTab(st().tabs[1].id)
    expect(st().tabs[1].pinned).toBe(true)
    // Pinned directly from the list.
    st().openTable(T('v_users', 'view'), true)
    expect(st().tabs).toHaveLength(3)
  })

  it('loads a page and then counts rows', async () => {
    st().openTable(T('users'))
    await flush()
    await flush()
    const t = tab()
    expect(t.rows).toHaveLength(2)
    expect(t.columns[0].name).toBe('id')
    expect(t.loading).toBe(false)
    expect(t.totalCount).toBe(12345)
    expect(t.counting).toBe(false)
    expect(t.elapsedMs).toBe(5)
    expect(t.selectedRow).toBe(0)
    expect(api.fetchTable.mock.calls[0][2]).toEqual({
      schema: 'public',
      table: 'users',
      filters: [],
      rawWhere: '',
      sort: [],
      offset: 0,
      limit: 300,
    })
    expect(tabRequest(t).table).toBe('users')
  })

  it('uses the page total and skips the count when known', async () => {
    api.fetchTable.mockImplementation(async (_s, _r, req) => page(req, { hasMore: false, totalCount: 2 }))
    st().openTable(T('users'))
    await flush()
    expect(tab().totalCount).toBe(2)
    expect(api.countRows).not.toHaveBeenCalled()
  })

  it('ignores a failed count', async () => {
    api.countRows.mockRejectedValue(new Error('slow'))
    st().openTable(T('users'))
    await flush()
    await flush()
    expect(tab().totalCount).toBeNull()
    expect(tab().counting).toBe(false)
    expect(tab().error).toBeNull()
  })

  it('shows fetch errors inline and retries', async () => {
    api.fetchTable.mockRejectedValueOnce(new Error('permission denied'))
    st().openTable(T('users'))
    await flush()
    expect(tab().error).toBe('permission denied')
    expect(tab().loading).toBe(false)
    await st().loadTab(tab().id)
    expect(tab().error).toBeNull()
    expect(tab().rows).toHaveLength(2)
  })

  it('drops stale responses and cancels superseded requests', async () => {
    let resolveFirst: (p: Page) => void = () => {}
    api.fetchTable.mockImplementationOnce(
      (_s, _r, req) =>
        new Promise<Page>((r) => {
          resolveFirst = () => r(page(req, { rows: [] }))
        }),
    )
    st().openTable(T('users'))
    const firstReq = tab().requestId
    expect(tab().loading).toBe(true)
    await st().loadTab(tab().id) // second load supersedes the first
    expect(api.cancel).toHaveBeenCalledWith(firstReq)
    resolveFirst(page({} as FetchRequest))
    await flush()
    expect(tab().rows).toHaveLength(2) // the stale empty page was ignored
  })

  it('cancels a running load', () => {
    api.fetchTable.mockImplementation(() => new Promise(() => {}))
    st().openTable(T('users'))
    const req = tab().requestId
    st().cancelLoad(tab().id)
    expect(api.cancel).toHaveBeenCalledWith(req)
    expect(tab().loading).toBe(false)
    expect(tab().error).toMatch(/cancelled/)
    st().cancelLoad(tab().id) // nothing running: no-op
    expect(api.cancel).toHaveBeenCalledTimes(1)
  })

  it('pages forward and back, pinning the tab', async () => {
    st().openTable(T('users'))
    await flush()
    const id = tab().id
    st().nextPage(id)
    expect(tab().pinned).toBe(true)
    await flush()
    expect(tab().offset).toBe(300)
    expect(tab().rows[0][0].v).toBe(301)
    st().prevPage(id)
    await flush()
    expect(tab().offset).toBe(0)
    st().prevPage(id) // already at the start
    expect(api.fetchTable).toHaveBeenCalledTimes(3)
    store.setState({ tabs: st().tabs.map((t) => ({ ...t, hasMore: false })) })
    st().nextPage(id) // no more rows
    expect(api.fetchTable).toHaveBeenCalledTimes(3)
    st().nextPage('nope')
    st().prevPage('nope')
  })

  it('sorts from header clicks and resets the page', async () => {
    st().openTable(T('users'))
    await flush()
    const id = tab().id
    store.setState({ tabs: st().tabs.map((t) => ({ ...t, offset: 600 })) })
    st().toggleSort(id, 'name', false)
    expect(tab().sort).toEqual([{ column: 'name', desc: false }])
    expect(tab().offset).toBe(0)
    st().toggleSort(id, 'id', true)
    expect(tab().sort).toHaveLength(2)
    await flush()
    expect(api.fetchTable.mock.lastCall?.[2].sort).toEqual([
      { column: 'name', desc: false },
      { column: 'id', desc: false },
    ])
    st().toggleSort('nope', 'id', false)
  })

  it('manages the filter bar and applies filters', async () => {
    st().openTable(T('users'))
    await flush()
    await flush()
    const id = tab().id
    st().toggleFilterBar(id)
    expect(tab().filterOpen).toBe(true)
    expect(tab().draftFilters).toHaveLength(1)
    expect(tab().draftFilters[0].column).toBe('id')
    st().addFilterRow(id)
    expect(tab().draftFilters).toHaveLength(2)
    st().setDraftFilters(id, [{ ...tab().draftFilters[0], op: 'contains', value: 'x' }])
    st().applyFilters(id)
    expect(tab().appliedFilters).toEqual([{ column: 'id', op: 'contains', value: 'x', value2: '' }])
    expect(tab().totalCount).toBeNull()
    await flush()
    await flush()
    expect(api.countRows.mock.lastCall?.[2].filters).toHaveLength(1)
    expect(tab().totalCount).toBe(12345)

    // Raw mode sends only the raw condition.
    st().setRawMode(id, true)
    st().setRawWhere(id, '  id > 5 ')
    st().applyFilters(id)
    expect(tab().appliedFilters).toEqual([])
    expect(tab().appliedRawWhere).toBe('id > 5')

    st().clearFilters(id)
    expect(tab().appliedRawWhere).toBe('')
    expect(tab().rawWhere).toBe('')
    expect(tab().draftFilters).toHaveLength(1)

    st().toggleFilterBar(id)
    expect(tab().filterOpen).toBe(false)
    st().toggleFilterBar(id, true)
    expect(tab().draftFilters).toHaveLength(1) // kept
    st().toggleFilterBar('nope')
    st().applyFilters('nope')
    st().clearFilters('nope')
  })

  it('discards a count for outdated filters', async () => {
    let resolveCount: (n: number) => void = () => {}
    api.countRows.mockImplementationOnce(() => new Promise<number>((r) => (resolveCount = r)))
    st().openTable(T('users'))
    await flush()
    const id = tab().id
    expect(tab().counting).toBe(true)
    st().setRawMode(id, true)
    st().setRawWhere(id, 'id = 1')
    st().applyFilters(id)
    await flush()
    resolveCount(999)
    await flush()
    expect(tab().totalCount).toBe(12345) // from the second count
  })

  it('closes tabs and moves the selection', async () => {
    st().openTable(T('a'), true)
    st().openTable(T('b'), true)
    st().openTable(T('c'), true)
    const [a, b, c] = st().tabs.map((t) => t.id)
    st().activateTab(b)
    st().closeTab(b)
    expect(st().activeTabId).toBe(c)
    st().closeTab(c)
    expect(st().activeTabId).toBe(a)
    st().openTable(T('d'), true)
    st().activateTab(a)
    st().closeTab(st().tabs[1].id) // closing an inactive tab keeps the active one
    expect(st().activeTabId).toBe(a)
    st().closeTab(a)
    expect(st().activeTabId).toBeNull()
    expect(st().activeTab()).toBeNull()
    st().closeTab('nope')
    st().activateTab('nope')
  })

  it('activates tabs by index (9 = last)', () => {
    for (const n of ['a', 'b', 'c']) st().openTable(T(n), true)
    st().activateIndex(0)
    expect(tab().table).toBe('a')
    st().activateIndex(8)
    expect(tab().table).toBe('c')
    st().activateIndex(5)
    expect(tab().table).toBe('c')
  })

  it('refreshes the active tab and the table list', async () => {
    await st().refresh() // no tab: only the list
    expect(api.listTables).toHaveBeenCalledTimes(1)
    st().openTable(T('users'))
    await flush()
    await st().refresh()
    expect(api.fetchTable).toHaveBeenCalledTimes(2)
    expect(api.listTables).toHaveBeenCalledTimes(2)
  })

  it('tracks selection, detail panel and column widths', async () => {
    st().openTable(T('users'))
    await flush()
    const id = tab().id
    st().selectRow(id, 1)
    expect(tab().selectedRow).toBe(1)
    st().toggleDetail()
    expect(st().showDetail).toBe(true)
    st().setColumnWidth(id, 'name', 222)
    expect(tab().columnWidths).toEqual({ name: 222 })
  })

  it('cancels outstanding requests on dispose and ignores late results', async () => {
    let resolve: (p: Page) => void = () => {}
    api.fetchTable.mockImplementationOnce(() => new Promise<Page>((r) => (resolve = r)))
    st().openTable(T('users'))
    const req = tab().requestId
    st().dispose()
    expect(api.cancel).toHaveBeenCalledWith(req)
    resolve(page({ offset: 0, limit: 300 } as FetchRequest))
    await flush()
    expect(tab().rows).toEqual([])
  })

  it('ignores errors from cancel', async () => {
    api.cancel.mockRejectedValue(new Error('gone'))
    api.fetchTable.mockImplementation(() => new Promise(() => {}))
    st().openTable(T('users'))
    st().cancelLoad(tab().id)
    await flush()
    expect(tab().error).toMatch(/cancelled/)
  })
})

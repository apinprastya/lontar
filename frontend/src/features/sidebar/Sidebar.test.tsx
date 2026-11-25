import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('../../lib/clipboard', () => ({ copyText: vi.fn(async () => {}) }))

import { copyText } from '../../lib/clipboard'
import { getUIState } from '../../lib/uiState'
import { makeObject } from '../../test/objects'
import type { QueryTabState } from '../../stores/queryTabs'
import { WorkspaceContext, createWorkspaceStore, type TabState, type WorkspaceStore } from '../../stores/workspace'
import { makeDetail } from '../../test/structure'
import type { WorkspaceApi } from '../workspace/api'
import Sidebar from './Sidebar'
import { fakeStructureApi } from '../../test/fakeStructureApi'

const tick = () => new Promise((r) => setTimeout(r, 0))

function api(): WorkspaceApi {
  return {
    current: vi.fn(async () => ({ database: 'd', schema: 'public' })),
    listDatabases: vi.fn(async () => ['d']),
    useDatabase: vi.fn(),
    listSchemas: vi.fn(async () => ['public']),
    listTables: vi.fn(async () => [
      { schema: 'public', name: 'orders', type: 'table' as const, estimatedRows: 2 },
      { schema: 'public', name: 'v', type: 'view' as const, estimatedRows: null },
    ]),
    fetchTable: vi.fn(async () => ({ columns: [], rows: [], offset: 0, limit: 300, hasMore: false, totalCount: 0, elapsedMs: 1 })),
    countRows: vi.fn(async () => 0),
    cancel: vi.fn(async () => true),
    tableInfo: vi.fn(async (_s, schema, table) => makeDetail({ schema, name: table })),
    tableDDL: vi.fn(async (_s, _schema, table) => `CREATE TABLE ${table} ();`),
    listObjects: vi.fn(async () => []),
    objectDDL: vi.fn(async () => ''),
  }
}

describe('Sidebar table menu', () => {
  let store: WorkspaceStore
  let wsApi: WorkspaceApi
  let structure: ReturnType<typeof fakeStructureApi>
  const mount = () =>
    render(
      <WorkspaceContext.Provider value={store}>
        <Sidebar name="conn" kind="postgres" color="" />
      </WorkspaceContext.Provider>,
    )
  const openMenu = (name: string) => fireEvent.contextMenu(screen.getByRole('button', { name: new RegExp(name) }), { clientX: 20, clientY: 30 })
  const tableTab = () => store.getState().tabs.find((t): t is TabState => t.kind === 'table') as TabState

  beforeEach(async () => {
    vi.mocked(copyText).mockClear()
    wsApi = api()
    structure = fakeStructureApi()
    store = createWorkspaceStore({ id: 's', kind: 'postgres', safeMode: 'off' }, wsApi, undefined, undefined, undefined, structure)
    await store.getState().init()
  })
  afterEach(cleanup)

  it('edits the structure, drops a table and creates one', async () => {
    mount()
    openMenu('orders')
    fireEvent.click(screen.getByRole('menuitem', { name: 'Edit structure' }))
    await act(tick)
    expect(tableTab().view).toBe('structure')
    expect(tableTab().structureEdit).not.toBeNull()
    openMenu('orders')
    fireEvent.click(screen.getByRole('menuitem', { name: 'Drop table…' }))
    await act(tick)
    expect(store.getState().dropTable).toMatchObject({ schema: 'public', table: 'orders', loading: false })
    store.getState().cancelDropTable()
    // Views have neither.
    openMenu('v')
    expect(screen.queryByRole('menuitem', { name: 'Edit structure' })).toBeNull()
    fireEvent.keyDown(screen.getByRole('menu'), { key: 'Escape' })
    fireEvent.click(screen.getByRole('button', { name: 'New table' }))
    await act(tick)
    expect(store.getState().activeTab()).toMatchObject({ kind: 'table', creating: true, view: 'structure' })
  })

  it('has no structure actions in read-only safe mode', async () => {
    store = createWorkspaceStore({ id: 's', kind: 'postgres', safeMode: 'readonly' }, wsApi, undefined, undefined, undefined, structure)
    await store.getState().init()
    mount()
    expect(screen.queryByRole('button', { name: 'New table' })).toBeNull()
    openMenu('orders')
    expect(screen.queryByRole('menuitem', { name: 'Drop table…' })).toBeNull()
  })

  it('opens data and structure', async () => {
    mount()
    openMenu('orders')
    const menu = screen.getByRole('menu', { name: 'orders actions' })
    expect(menu).toHaveStyle({ left: '20px', top: '30px' })
    expect(screen.getAllByRole('menuitem').map((m) => m.textContent)).toEqual(['Open data', 'Open structure', 'Copy name', 'Copy DDL', 'Edit structure', 'Drop table…'])
    fireEvent.click(screen.getByRole('menuitem', { name: 'Open structure' }))
    expect(screen.queryByRole('menu')).toBeNull()
    await act(tick)
    expect(tableTab().view).toBe('structure')
    expect(tableTab().pinned).toBe(true)
    openMenu('orders')
    fireEvent.click(screen.getByRole('menuitem', { name: 'Open data' }))
    expect(tableTab().view).toBe('data')
  })

  it('copies the name and the DDL', async () => {
    mount()
    openMenu('orders')
    fireEvent.click(screen.getByRole('menuitem', { name: 'Copy name' }))
    expect(copyText).toHaveBeenCalledWith('orders')
    openMenu('v')
    fireEvent.click(screen.getByRole('menuitem', { name: 'Copy DDL' }))
    await act(tick)
    expect(wsApi.tableDDL).toHaveBeenCalledWith('s', 'public', 'v')
    expect(copyText).toHaveBeenCalledWith('CREATE TABLE v ();')
    vi.mocked(wsApi.tableDDL).mockRejectedValueOnce(new Error('denied'))
    openMenu('v')
    fireEvent.click(screen.getByRole('menuitem', { name: 'Copy DDL' }))
    await act(tick)
    expect(screen.getByRole('alert')).toHaveTextContent('denied')
  })

  it('closes on Escape, outside click and right-click', () => {
    mount()
    openMenu('orders')
    fireEvent.keyDown(screen.getByRole('menu'), { key: 'Escape' })
    expect(screen.queryByRole('menu')).toBeNull()
    openMenu('orders')
    fireEvent.keyDown(screen.getByRole('menu'), { key: 'a' })
    fireEvent.mouseDown(screen.getByRole('menu'))
    expect(screen.getByRole('menu')).toBeInTheDocument()
    fireEvent.mouseDown(document.querySelector('.context-menu-backdrop') as Element)
    expect(screen.queryByRole('menu')).toBeNull()
    openMenu('orders')
    fireEvent.contextMenu(document.querySelector('.context-menu-backdrop') as Element)
    expect(screen.queryByRole('menu')).toBeNull()
  })
})

describe('Sidebar objects', () => {
  let store: WorkspaceStore
  let wsApi: WorkspaceApi
  const objects = [
    makeObject({ name: 'add_one', signature: 'x integer', detail: 'returns integer · sql' }),
    makeObject({ kind: 'procedure', name: 'reset', id: '2' }),
    makeObject({ kind: 'sequence', name: 'ticket_seq', id: '3' }),
    makeObject({ kind: 'enum', name: 'mood', id: '4' }),
    makeObject({ kind: 'trigger', name: 'orders_touch', table: 'orders', id: '5' }),
  ]
  const mount = () =>
    render(
      <WorkspaceContext.Provider value={store}>
        <Sidebar name="conn" kind="postgres" color="" />
      </WorkspaceContext.Provider>,
    )
  const groups = () => screen.getAllByRole('heading', { level: 3 }).map((h) => h.textContent)

  beforeEach(async () => {
    vi.mocked(copyText).mockClear()
    wsApi = api()
    vi.mocked(wsApi.listObjects).mockResolvedValue(objects)
    vi.mocked(wsApi.objectDDL).mockImplementation(async (_s, _schema, ref) => `CREATE ${ref.kind} ${ref.name};`)
    store = createWorkspaceStore({ id: 's', kind: 'postgres', safeMode: 'off' }, wsApi)
    await store.getState().init()
  })
  afterEach(cleanup)

  it('shows the non-empty groups with counts below tables and views', () => {
    mount()
    expect(wsApi.listObjects).toHaveBeenCalledWith('s', 'public')
    expect(groups()).toEqual(['Tables 1', 'Views 1', 'Functions 1', 'Procedures 1', 'Sequences 1', 'Types 1', 'Triggers 1'])
    const fns = screen.getByRole('region', { name: 'Functions' })
    expect(within(fns).getByRole('button', { name: 'add_one(x integer)' })).toHaveAttribute('title', 'public.add_one (function) — returns integer · sql')
    expect(screen.getByRole('button', { name: 'orders_touch orders' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'mood enum' })).toBeInTheDocument()
  })

  it('filters every group by the search', () => {
    mount()
    fireEvent.change(screen.getByLabelText('Search tables'), { target: { value: 'order' } })
    // The table and the trigger on it.
    expect(groups()).toEqual(['Tables 1', 'Triggers 1'])
    fireEvent.change(screen.getByLabelText('Search tables'), { target: { value: 'seq' } })
    expect(groups()).toEqual(['Sequences 1'])
    fireEvent.change(screen.getByLabelText('Search tables'), { target: { value: 'zzz' } })
    expect(screen.getByText('Nothing matches “zzz”.')).toBeInTheDocument()
  })

  it('collapses groups and remembers it', () => {
    const { unmount } = mount()
    const toggle = screen.getByRole('button', { name: 'Sequences 1' })
    expect(toggle).toHaveAttribute('aria-expanded', 'true')
    fireEvent.click(toggle)
    expect(toggle).toHaveAttribute('aria-expanded', 'false')
    expect(screen.queryByRole('button', { name: 'ticket_seq' })).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: 'Tables 1' }))
    expect(JSON.parse(getUIState('sidebar.collapsedGroups') ?? '[]')).toEqual(['sequences', 'tables'])
    unmount()
    mount()
    expect(screen.getByRole('button', { name: 'Sequences 1' })).toHaveAttribute('aria-expanded', 'false')
    fireEvent.click(screen.getByRole('button', { name: 'Sequences 1' }))
    expect(screen.getByRole('button', { name: 'ticket_seq' })).toBeInTheDocument()
    expect(JSON.parse(getUIState('sidebar.collapsedGroups') ?? '[]')).toEqual(['tables'])
  })

  it('opens an object tab on click and marks it current', async () => {
    mount()
    fireEvent.click(screen.getByRole('button', { name: 'ticket_seq' }))
    await act(tick)
    const tab = store.getState().activeTab()
    expect(tab?.kind).toBe('object')
    expect(screen.getByRole('button', { name: 'ticket_seq' })).toHaveAttribute('aria-current', 'true')
    expect(wsApi.objectDDL).toHaveBeenCalledWith('s', 'public', { kind: 'sequence', name: 'ticket_seq', signature: '', table: '', id: '3' })
  })

  it('copies the name and the DDL, and opens the DDL in a query tab', async () => {
    mount()
    const openMenu = () => fireEvent.contextMenu(screen.getByRole('button', { name: 'add_one(x integer)' }), { clientX: 5, clientY: 6 })
    openMenu()
    expect(screen.getAllByRole('menuitem').map((m) => m.textContent)).toEqual(['Copy name', 'Copy DDL', 'Open in new query tab'])
    fireEvent.click(screen.getByRole('menuitem', { name: 'Copy name' }))
    expect(copyText).toHaveBeenCalledWith('add_one')
    openMenu()
    fireEvent.click(screen.getByRole('menuitem', { name: 'Copy DDL' }))
    await act(tick)
    expect(copyText).toHaveBeenLastCalledWith('CREATE function add_one;')
    openMenu()
    fireEvent.click(screen.getByRole('menuitem', { name: 'Open in new query tab' }))
    await act(tick)
    const q = store.getState().activeTab() as QueryTabState
    expect(q.kind).toBe('query')
    expect(q.sql).toBe('CREATE function add_one;')

    vi.mocked(wsApi.objectDDL).mockRejectedValueOnce(new Error('gone'))
    openMenu()
    fireEvent.click(screen.getByRole('menuitem', { name: 'Copy DDL' }))
    await act(tick)
    expect(screen.getByRole('alert')).toHaveTextContent('gone')
    vi.mocked(wsApi.objectDDL).mockRejectedValueOnce(new Error('denied'))
    openMenu()
    fireEvent.click(screen.getByRole('menuitem', { name: 'Open in new query tab' }))
    await act(tick)
    expect(screen.getByRole('alert')).toHaveTextContent('denied')
  })
})

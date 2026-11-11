import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('../../lib/clipboard', () => ({ copyText: vi.fn(async () => {}) }))

import { copyText } from '../../lib/clipboard'
import { WorkspaceContext, createWorkspaceStore, type TabState, type WorkspaceStore } from '../../stores/workspace'
import { makeDetail } from '../../test/structure'
import type { WorkspaceApi } from '../workspace/api'
import Sidebar from './Sidebar'

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
  }
}

describe('Sidebar table menu', () => {
  let store: WorkspaceStore
  let wsApi: WorkspaceApi
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
    store = createWorkspaceStore({ id: 's', kind: 'postgres', safeMode: 'off' }, wsApi)
    await store.getState().init()
  })
  afterEach(cleanup)

  it('opens data and structure', async () => {
    mount()
    openMenu('orders')
    const menu = screen.getByRole('menu', { name: 'orders actions' })
    expect(menu).toHaveStyle({ left: '20px', top: '30px' })
    expect(screen.getAllByRole('menuitem').map((m) => m.textContent)).toEqual(['Open data', 'Open structure', 'Copy name', 'Copy DDL'])
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

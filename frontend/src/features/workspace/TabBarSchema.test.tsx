import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { WorkspaceContext, createWorkspaceStore } from '../../stores/workspace'
import { makeDetail } from '../../test/structure'
import type { WorkspaceApi } from './api'
import TabBar from './TabBar'

const api: WorkspaceApi = {
  current: vi.fn(async () => ({ database: 'd', schema: 'public' })),
  listDatabases: vi.fn(async () => ['d']),
  useDatabase: vi.fn(),
  listSchemas: vi.fn(async () => ['public', 'geo']),
  listTables: vi.fn(async () => [{ schema: 'public', name: 'orders', type: 'table' as const, estimatedRows: 1 }]),
  fetchTable: vi.fn(async () => ({ columns: [], rows: [], offset: 0, limit: 300, hasMore: false, totalCount: 0, elapsedMs: 1 })),
  countRows: vi.fn(async () => 0),
  cancel: vi.fn(async () => true),
  tableInfo: vi.fn(async () => makeDetail()),
  tableDDL: vi.fn(async () => ''),
}

describe('TabBar titles', () => {
  afterEach(cleanup)
  it('qualifies tables of another schema', async () => {
    const store = createWorkspaceStore({ id: 's', kind: 'postgres' }, api)
    await store.getState().init()
    store.getState().openTableView('public', 'orders')
    store.getState().openTableView('geo', 'regions')
    await act(() => new Promise((r) => setTimeout(r, 0)))
    render(
      <WorkspaceContext.Provider value={store}>
        <TabBar />
      </WorkspaceContext.Provider>,
    )
    expect(screen.getAllByRole('tab').map((t) => t.textContent)).toEqual(['orders×', 'geo.regions×'])
    expect(screen.getByRole('button', { name: 'Close geo.regions' })).toBeInTheDocument()
  })

  it('activates any tab on click, query tabs included', async () => {
    window.localStorage.clear()
    const store = createWorkspaceStore({ id: 's', kind: 'postgres' }, api)
    await store.getState().init()
    const q1 = store.getState().newQueryTab()
    const q2 = store.getState().newQueryTab()
    store.getState().openTableView('public', 'orders')
    await act(() => new Promise((r) => setTimeout(r, 0)))
    render(
      <WorkspaceContext.Provider value={store}>
        <TabBar />
      </WorkspaceContext.Provider>,
    )
    const tab = (name: RegExp) => screen.getByRole('tab', { name })
    fireEvent.click(tab(/Query 1/))
    expect(store.getState().activeTabId).toBe(q1)
    expect(tab(/Query 1/)).toHaveAttribute('aria-selected', 'true')
    fireEvent.click(tab(/Query 2/))
    expect(store.getState().activeTabId).toBe(q2)
    fireEvent.click(tab(/orders/))
    expect(store.getState().tabs.find((t) => t.id === store.getState().activeTabId)?.kind).toBe('table')
    // Unknown IDs are ignored.
    act(() => store.getState().activateTab('nope'))
    expect(store.getState().tabs.find((t) => t.id === store.getState().activeTabId)?.kind).toBe('table')
  })
})

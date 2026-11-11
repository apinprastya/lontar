import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { GridLinks } from '../grid/DataGrid'

let links: GridLinks | undefined
vi.mock('../grid/DataGrid', () => ({
  default: (p: { links?: GridLinks }) => {
    links = p.links
    return <div data-testid="grid" />
  },
}))

import { WorkspaceContext, createWorkspaceStore, type TabState, type WorkspaceStore } from '../../stores/workspace'
import { makeDetail, makeFK } from '../../test/structure'
import type { WorkspaceApi } from './api'
import type { Cell } from './types'
import TableTab from './TableTab'

const tick = () => new Promise((r) => setTimeout(r, 0))
const fk = makeFK({ table: 't', columns: ['parent_id'], refTable: 't', refColumns: ['id'] })

function api(fks = [fk]): WorkspaceApi {
  return {
    current: vi.fn(async () => ({ database: 'd', schema: 'public' })),
    listDatabases: vi.fn(async () => ['d']),
    useDatabase: vi.fn(),
    listSchemas: vi.fn(async () => ['public']),
    listTables: vi.fn(async () => [{ schema: 'public', name: 't', type: 'table' as const, estimatedRows: 2 }]),
    fetchTable: vi.fn(async () => ({
      columns: [
        { name: 'id', type: 'integer', isPK: true, nullable: false },
        { name: 'parent_id', type: 'integer', isPK: false, nullable: true },
      ],
      rows: [
        [{ k: 'num', v: 1 }, { k: 'null' }],
        [{ k: 'num', v: 2 }, { k: 'num', v: 1 }],
      ] as Cell[][],
      offset: 0,
      limit: 300,
      hasMore: false,
      totalCount: 2,
      elapsedMs: 1,
    })),
    countRows: vi.fn(async () => 2),
    cancel: vi.fn(async () => true),
    tableInfo: vi.fn(async () => makeDetail({ name: 't', foreignKeys: fks })),
    tableDDL: vi.fn(async () => ''),
  }
}

describe('TableTab data / structure', () => {
  let store: WorkspaceStore
  const tab = () => store.getState().tabs[0] as TabState
  const mount = () =>
    render(
      <WorkspaceContext.Provider value={store}>
        <TableTab tab={tab()} platform="darwin" />
      </WorkspaceContext.Provider>,
    )

  async function setup(fks = [fk]) {
    links = undefined
    store = createWorkspaceStore({ id: 's', kind: 'postgres', safeMode: 'off' }, api(fks))
    await store.getState().init()
    store.getState().openTable({ schema: 'public', name: 't', type: 'table', estimatedRows: 2 }, true)
    await act(tick)
  }
  beforeEach(() => setup())
  afterEach(cleanup)

  it('switches between data and structure with the toggle', () => {
    mount()
    expect(screen.getByTestId('grid')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Data' })).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getByRole('button', { name: 'Data' })).toHaveAttribute('title', 'Switch view (⌘E)')
    fireEvent.click(screen.getByRole('button', { name: 'Structure' }))
    expect(tab().view).toBe('structure')
    cleanup()
    mount()
    expect(screen.getByTestId('structure-view')).toBeInTheDocument()
    expect(screen.queryByTestId('grid')).toBeNull()
    expect(screen.getByRole('button', { name: 'Structure' })).toHaveAttribute('aria-pressed', 'true')
    fireEvent.click(screen.getByRole('button', { name: 'Data' }))
    expect(tab().view).toBe('data')
  })

  it('gives the grid the foreign keys of the structure', () => {
    mount()
    const l = links as GridLinks
    expect(l.fkColumns).toEqual([false, true])
    expect(l.canFollow(0, 1)).toBe(false) // NULL key
    expect(l.canFollow(1, 1)).toBe(true)
    expect(l.canFollow(1, 0)).toBe(false) // not a key column
    l.onFollow(0, 0) // ignored
    l.onFollow(9, 1) // no such row
    expect(store.getState().guard).toBeNull()
    l.onFollow(1, 1) // self reference: reuses this tab
    expect(tab().appliedFilters).toEqual([{ column: 'id', op: '=', value: '1', value2: '' }])
  })

  it('has no links without foreign keys', async () => {
    cleanup()
    await setup([])
    mount()
    expect(links).toBeUndefined()
  })
})

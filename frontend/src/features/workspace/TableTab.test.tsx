import { act, cleanup, render } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { GridEditing } from '../grid/DataGrid'

let editing: GridEditing | undefined
vi.mock('../grid/DataGrid', () => ({
  default: (p: { editing?: GridEditing }) => {
    editing = p.editing
    return <div data-testid="grid" />
  },
}))

import { WorkspaceContext, createWorkspaceStore, type TabState, type WorkspaceStore } from '../../stores/workspace'
import { pendingCount } from '../../stores/pending'
import type { WorkspaceApi } from './api'
import type { FetchRequest } from './types'
import TableTab from './TableTab'
import { makeDetail } from '../../test/structure'

const api: WorkspaceApi = {
  current: vi.fn(async () => ({ database: 'd', schema: 'public' })),
  listDatabases: vi.fn(async () => ['d']),
  useDatabase: vi.fn(),
  listSchemas: vi.fn(async () => ['public']),
  listTables: vi.fn(async () => [{ schema: 'public', name: 't', type: 'table' as const, estimatedRows: 2 }]),
  fetchTable: vi.fn(async (_s: string, _r: string, req: FetchRequest) => ({
    columns: [
      { name: 'id', type: 'integer', isPK: true, nullable: false },
      { name: 'v', type: 'text', isPK: false, nullable: true },
    ],
    rows: [
      [{ k: 'num' as const, v: 1 }, { k: 'str' as const, v: 'a' }],
      [{ k: 'num' as const, v: 2 }, { k: 'null' as const }],
    ],
    offset: req.offset,
    limit: req.limit,
    hasMore: false,
    totalCount: 2,
    elapsedMs: 1,
  })),
  countRows: vi.fn(async () => 2),
  cancel: vi.fn(async () => true),
  tableInfo: vi.fn(async () => makeDetail()),
  tableDDL: vi.fn(async () => ''),
}
const edits = { preview: vi.fn(async () => []), apply: vi.fn(async () => ({ ok: true, result: null, failure: null })) }

describe('TableTab editing wiring', () => {
  let store: WorkspaceStore
  const tab = () => store.getState().tabs[0] as TabState
  const mount = () =>
    render(
      <WorkspaceContext.Provider value={store}>
        <TableTab tab={tab()} />
      </WorkspaceContext.Provider>,
    )

  beforeEach(async () => {
    store = createWorkspaceStore({ id: 's', kind: 'sqlite', safeMode: 'off' }, api, undefined, edits)
    await store.getState().init()
    store.getState().openTable({ schema: 'public', name: 't', type: 'table', estimatedRows: 2 }, true)
    await new Promise((r) => setTimeout(r, 0))
  })
  afterEach(cleanup)

  it('routes grid callbacks to the store', async () => {
    mount()
    const e = editing as GridEditing
    expect(e.engine).toBe('sqlite')
    expect(e.editText(0, 1)).toBe('a')
    expect(e.editText(9, 9)).toBe('')
    act(() => e.onCommitCell(0, 1, { kind: 'value', text: 'b' }))
    act(() => e.onCommitCell(9, 9, { kind: 'null' })) // ignored
    expect(pendingCount(tab().pending)).toBe(1)
    cleanup()
    mount()
    const e2 = editing as GridEditing
    expect(e2.editText(0, 1)).toBe('b') // the staged value
    act(() => e2.onSetCells([[1, 1], [5, 5]], { kind: 'value', text: 'x' }))
    expect(pendingCount(tab().pending)).toBe(2)
    act(() => e2.onInsertRow())
    act(() => e2.onDuplicateRow(0))
    act(() => e2.onDuplicateRow(99))
    expect(tab().pending.inserts).toHaveLength(2)
    act(() => e2.onDeleteRows([1, 99]))
    expect(Object.keys(tab().pending.deletes)).toHaveLength(1)
    act(() => e2.onUndo())
    expect(Object.keys(tab().pending.deletes)).toHaveLength(0)
    act(() => e2.onRedo())
    expect(Object.keys(tab().pending.deletes)).toHaveLength(1)
    act(() => e2.onPaste(0, 1, [['p']]))
    expect(tab().pending.edits['[["num",1]]'].set.v).toEqual({ kind: 'value', text: 'p' })
    await act(async () => e2.onCommit())
    expect(edits.apply).toHaveBeenCalled()
  })

  it('has no editing callbacks for read-only connections', async () => {
    store = createWorkspaceStore({ id: 's', kind: 'sqlite', safeMode: 'readonly' }, api, undefined, edits)
    await store.getState().init()
    store.getState().openTable({ schema: 'public', name: 't', type: 'table', estimatedRows: 2 }, true)
    await new Promise((r) => setTimeout(r, 0))
    mount()
    expect(editing).toBeUndefined()
  })
})

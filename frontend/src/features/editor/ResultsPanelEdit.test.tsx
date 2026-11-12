import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { DataGridProps } from '../grid/DataGrid'

// The canvas grid is replaced by a probe that exposes its editing props.
vi.mock('../grid/DataGrid', () => ({
  default: (p: DataGridProps) => (
    <div data-testid="grid">
      <span data-testid="grid-cells">{p.rows.map((r) => r.map((c) => String(c?.v ?? '')).join('|')).join(',')}</span>
      <span data-testid="grid-editable">{p.editing ? 'yes' : 'no'}</span>
      {p.editing && (
        <>
          <button type="button" onClick={() => p.editing?.onCommitCell(0, 1, { kind: 'value', text: 'zed' })}>
            edit cell
          </button>
          <button type="button" onClick={() => p.editing?.onInsertRow()}>
            insert
          </button>
        </>
      )}
    </div>
  ),
}))

import { WorkspaceContext, createWorkspaceStore, useWorkspace, type WorkspaceStore } from '../../stores/workspace'
import type { QueryTabState } from '../../stores/queryTabs'
import { execResult, fakeEditorApi, resultSet, stmt } from '../../test/fakeEditorApi'
import type { WorkspaceApi } from '../workspace/api'
import type { ResultSource } from '../edits/types'
import { makeDetail } from '../../test/structure'
import ResultsPanel from './ResultsPanel'

const wsApi: WorkspaceApi = {
  current: vi.fn(async () => ({ database: 'shop', schema: 'public' })),
  listDatabases: vi.fn(async () => ['shop']),
  useDatabase: vi.fn(async () => ({ database: 'shop', schema: 'public' })),
  listSchemas: vi.fn(async () => ['public']),
  listTables: vi.fn(async () => []),
  fetchTable: vi.fn(),
  countRows: vi.fn(),
  cancel: vi.fn(async () => true),
  tableInfo: vi.fn(async () => makeDetail()),
  tableDDL: vi.fn(async () => ''),
}

const editable: ResultSource = {
  editable: true,
  reason: '',
  schema: 'public',
  table: 'users',
  columns: [
    { column: 'id', isPK: true, nullable: false, hasDefault: true },
    { column: 'name', isPK: false, nullable: true, hasDefault: false },
  ],
}

describe('ResultsPanel editing', () => {
  let store: WorkspaceStore
  const resultSource = vi.fn(async (): Promise<ResultSource> => editable)

  function Live() {
    const t = useWorkspace((s) => s.tabs.find((x) => x.id === s.activeTabId) as QueryTabState | undefined)
    return t ? <ResultsPanel tab={t} platform="linux" /> : null
  }

  async function mount(safeMode: 'off' | 'readonly' = 'off') {
    const editor = fakeEditorApi()
    editor.execute.mockImplementation(async (_e: string, _r: string, sql: string) => execResult([stmt(0, sql, { resultSets: [resultSet('rs-1')] })]))
    const edits = { preview: vi.fn(async () => []), apply: vi.fn(), resultSource }
    store = createWorkspaceStore({ id: 's1', kind: 'postgres', connectionId: 'c1', safeMode }, wsApi, editor, edits)
    const id = store.getState().newQueryTab('SELECT * FROM users')
    await store.getState().runQuery(id, 'all')
    render(
      <WorkspaceContext.Provider value={store}>
        <Live />
      </WorkspaceContext.Provider>,
    )
    return id
  }

  beforeEach(() => resultSource.mockClear())
  afterEach(() => store.getState().dispose())

  it('edits a result that maps onto one table', async () => {
    const id = await mount()
    expect(await screen.findByTestId('result-editable')).toHaveTextContent('Editing public.users')
    expect(resultSource).toHaveBeenCalledWith('s1', 'SELECT * FROM users', 2)
    expect(screen.getByTestId('grid-editable')).toHaveTextContent('yes')
    fireEvent.click(screen.getByRole('button', { name: 'edit cell' }))
    expect(screen.getByTestId('grid-cells')).toHaveTextContent('1|zed')
    fireEvent.click(screen.getByRole('button', { name: 'insert' }))
    expect(screen.getByTestId('grid-cells').textContent).toContain('|DEFAULT')

    act(() => {
      store.setState({
        tabs: store.getState().tabs.map((t) => (t.kind === 'query' && t.id === id && t.edit ? { ...t, edit: { ...t.edit, commitError: 'stale row', failed: {} } } : t)),
      })
    })
    expect(screen.getByTestId('commit-error')).toHaveTextContent('Commit failed. stale row Your changes are still staged; the failing row is highlighted.')
  })

  it('says why a result is read-only', async () => {
    resultSource.mockResolvedValueOnce({ ...editable, editable: false, reason: 'Grouped or DISTINCT results are read-only.', columns: [] })
    await mount()
    const badge = await screen.findByTestId('result-readonly')
    expect(badge).toHaveAttribute('title', 'Grouped or DISTINCT results are read-only.')
    expect(screen.getByTestId('grid-editable')).toHaveTextContent('no')
  })

  it('stays read-only in read-only safe mode', async () => {
    await mount('readonly')
    await waitFor(() => expect(screen.getByTestId('result-readonly')).toHaveAttribute('title', 'This connection is in read-only safe mode.'))
  })

  it('shows lookup errors as the read-only reason', async () => {
    resultSource.mockRejectedValueOnce(new Error('metadata down'))
    await mount()
    expect(await screen.findByTestId('result-readonly')).toHaveAttribute('title', 'metadata down')
  })
})

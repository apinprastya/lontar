import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { DataGridProps } from '../grid/DataGrid'

// The canvas grid is replaced by a probe that exposes its props.
vi.mock('../grid/DataGrid', () => ({
  default: (p: DataGridProps) => (
    <div data-testid="grid">
      <span data-testid="grid-rows">{p.rows.map((r) => String(r[0]?.v)).join(',')}</span>
      <button type="button" onClick={() => p.onHeaderClick('id', false)}>
        sort id
      </button>
      <button type="button" onClick={() => p.onColumnResize('id', 99)}>
        resize
      </button>
      <button type="button" onClick={() => p.onSelectRow(1)}>
        select row
      </button>
      <span data-testid="grid-width">{p.columnWidths.id ?? ''}</span>
    </div>
  ),
}))

import { WorkspaceContext, createWorkspaceStore, type WorkspaceStore } from '../../stores/workspace'
import type { QueryTabState } from '../../stores/queryTabs'
import { execResult, fakeEditorApi, resultSet, stmt } from '../../test/fakeEditorApi'
import type { WorkspaceApi } from '../workspace/api'
import QueryTab, { HIGHLIGHT_DELAY } from './QueryTab'

const wsApi: WorkspaceApi = {
  current: vi.fn(async () => ({ database: 'shop', schema: 'public' })),
  listDatabases: vi.fn(async () => ['shop']),
  useDatabase: vi.fn(async () => ({ database: 'shop', schema: 'public' })),
  listSchemas: vi.fn(async () => ['public']),
  listTables: vi.fn(async () => []),
  fetchTable: vi.fn(),
  countRows: vi.fn(),
  cancel: vi.fn(async () => true),
}

describe('QueryTab', () => {
  let store: WorkspaceStore
  let editor: ReturnType<typeof fakeEditorApi>
  const tab = () => store.getState().activeTab() as QueryTabState

  function mount() {
    return render(
      <WorkspaceContext.Provider value={store}>
        <Live />
      </WorkspaceContext.Provider>,
    )
  }
  /** Re-renders on store changes, like the Workspace. */
  function Live() {
    const t = useTab()
    return t ? <QueryTab tab={t} platform="linux" api={editor} /> : null
  }
  let useTab: () => QueryTabState | null

  beforeEach(async () => {
    window.localStorage.clear()
    editor = fakeEditorApi()
    store = createWorkspaceStore({ id: 's1', kind: 'postgres', connectionId: 'c1' }, wsApi, editor)
    await store.getState().init()
    store.getState().newQueryTab('SELECT 1;\nSELEC 2')
    const { useWorkspace } = await import('../../stores/workspace')
    useTab = () => useWorkspace((s) => (s.tabs.find((t) => t.id === s.activeTabId) as QueryTabState) ?? null)
  })
  afterEach(() => store.getState().dispose())

  it('runs from the toolbar and shows results, banner and messages', async () => {
    editor.execute.mockResolvedValueOnce(
      execResult(
        [
          stmt(0, 'SELECT 1', { start: 0, end: 8, resultSets: [resultSet('r1', 3, { truncated: true, canLoadMore: true })] }),
          stmt(1, 'SELECT 2', { start: 10, end: 18, resultSets: [resultSet('r2', 1)] }),
        ],
        { tx: { state: 'active', source: 'server' } },
      ),
    )
    mount()
    expect(screen.getByText(/Run the statement under the cursor with Ctrl\+Enter/)).toBeInTheDocument()
    await waitFor(() => expect(editor.completionSchema).toHaveBeenCalledWith('s1', 'public', false))
    fireEvent.click(screen.getByText('Run all'))
    await waitFor(() => expect(screen.getByRole('tab', { name: /Result 1/ })).toHaveAttribute('aria-selected', 'true'))
    expect(screen.getByRole('tab', { name: /Result 1/ })).toHaveTextContent('3+')
    expect(screen.getByTestId('tx-indicator')).toHaveTextContent('Transaction open')
    expect(screen.getByRole('status', { name: '' })).toBeTruthy()
    expect(screen.getByText(/Showing first 3 rows/)).toBeInTheDocument()

    // Load more.
    fireEvent.click(screen.getByText('Load 1,000 more'))
    await waitFor(() => expect(screen.getByTestId('grid-rows')).toHaveTextContent('1,2,3,1'))
    expect(screen.queryByText(/Showing first/)).not.toBeInTheDocument()

    // Client-side sort, resize and row selection.
    fireEvent.click(screen.getByText('sort id'))
    expect(screen.getByTestId('grid-rows')).toHaveTextContent('1,1,2,3')
    fireEvent.click(screen.getByText('resize'))
    expect(screen.getByTestId('grid-width')).toHaveTextContent('99')
    fireEvent.click(screen.getByText('select row'))
    expect(tab().selectedRow).toBe(1)

    // Other result, then Messages with click-to-select.
    fireEvent.click(screen.getByRole('tab', { name: /Result 2/ }))
    expect(screen.getByTestId('grid-rows')).toHaveTextContent('1')
    fireEvent.click(screen.getByRole('tab', { name: /Messages/ }))
    fireEvent.click(screen.getAllByRole('listitem')[1])
    expect(tab().editorRequest).toMatchObject({ type: 'select', from: 10, to: 18 })
  })

  it('shows a non-reloadable truncated result and empty results', async () => {
    editor.execute.mockResolvedValueOnce(
      execResult([stmt(0, 'x', { resultSets: [resultSet('r1', 0, { truncated: true })] }), stmt(1, 'SELEC', { status: 'error', error: { message: 'bad', code: '1', position: null } })]),
    )
    mount()
    fireEvent.click(screen.getByText('Run all'))
    await waitFor(() => expect(screen.getByRole('tab', { name: /Messages/ })).toHaveTextContent('1 ✗'))
    fireEvent.click(screen.getByRole('tab', { name: /Result 1/ }))
    expect(screen.getByText(/Run the query again with a higher row limit/)).toBeInTheDocument()
    expect(screen.getByText('No rows.')).toBeInTheDocument()
  })

  it('wires the toolbar controls', async () => {
    let finish: (v: unknown) => void = () => {}
    editor.execute.mockImplementationOnce(() => new Promise((r) => (finish = r)) as never)
    mount()
    fireEvent.click(screen.getByLabelText('Continue on error'))
    expect(tab().continueOnError).toBe(true)
    fireEvent.change(screen.getByLabelText('Max rows'), { target: { value: '5000' } })
    expect(tab().maxRows).toBe(5000)
    fireEvent.click(screen.getByText('Run'))
    await waitFor(() => expect(screen.getByText('Stop')).toBeEnabled())
    expect(screen.getAllByText('Running…').length).toBeGreaterThan(0)
    fireEvent.click(screen.getByText('Stop'))
    expect(editor.cancel).toHaveBeenCalled()
    await act(async () => finish(execResult([stmt(0, 'SELECT 1', { status: 'cancelled' })], { cancelled: true })))
    await waitFor(() => expect(screen.getByText('Stop')).toBeDisabled())

    // History panel.
    fireEvent.click(screen.getByText('History'))
    expect(screen.getByLabelText('Query history')).toBeInTheDocument()
    await waitFor(() => expect(editor.historyList).toHaveBeenCalledWith('c1', '', 200, 0))
    fireEvent.click(screen.getByLabelText('Close history'))
    expect(screen.queryByLabelText('Query history')).not.toBeInTheDocument()
  })

  it('highlights the statement under the cursor after a pause', async () => {
    vi.useFakeTimers()
    try {
      mount()
      await act(async () => {
        vi.advanceTimersByTime(HIGHLIGHT_DELAY + 10)
      })
      expect(editor.statementAt).toHaveBeenCalledWith('postgres', 'SELECT 1;\nSELEC 2', expect.any(Number))
    } finally {
      vi.useRealTimers()
    }
  })

  it('connects the editor and the history to the store', async () => {
    const { EditorView } = await import('@codemirror/view')
    editor.historyList.mockResolvedValue([
      { id: 1, connectionId: 'c1', database: 'shop', sql: 'SELECT 99', startedAt: new Date().toISOString(), durationMs: 1, rowCount: 1, rowsAffected: null, isError: false, error: '', truncated: false },
    ] as never)
    const { container } = mount()
    const view = EditorView.findFromDOM(container.querySelector('.cm-editor') as HTMLElement)!
    act(() => view.dispatch({ selection: { anchor: 0, head: 8 } }))
    expect(tab().selection).toEqual({ from: 0, to: 8 })
    expect(screen.getByText('Run selection')).toBeInTheDocument()
    act(() => view.dispatch({ changes: { from: 0, insert: '-- x\n' } }))
    expect(tab().sql).toBe('-- x\nSELECT 1;\nSELEC 2')
    let finish: (v: unknown) => void = () => {}
    editor.execute.mockImplementationOnce(() => new Promise((r) => (finish = r)) as never)
    act(() => {
      view.contentDOM.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', ctrlKey: true, shiftKey: true, bubbles: true }))
    })
    await waitFor(() => expect(tab().running).toBe(true))
    act(() => {
      view.contentDOM.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    })
    expect(editor.cancel).toHaveBeenCalled()
    await act(async () => finish(execResult([])))
    act(() => {
      view.contentDOM.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    })
    expect(editor.cancel).toHaveBeenCalledTimes(1)

    // History: click inserts, double-click opens a new tab.
    fireEvent.click(screen.getByText('History'))
    const entry = await screen.findByText('SELECT 99', { selector: '.history-sql' })
    fireEvent.doubleClick(entry)
    expect(store.getState().tabs).toHaveLength(2)
    expect(tab().sql).toBe('SELECT 99')
    const again = await screen.findByText('SELECT 99', { selector: '.history-sql' })
    vi.useFakeTimers()
    try {
      fireEvent.click(again)
      act(() => vi.advanceTimersByTime(500))
    } finally {
      vi.useRealTimers()
    }
    expect(tab().editorRequest).toMatchObject({ type: 'insert', text: 'SELECT 99' })
  })

  it('shows run errors before any result', async () => {
    editor.execute.mockRejectedValueOnce(new Error('session not found'))
    mount()
    fireEvent.click(screen.getByText('Run all'))
    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent('session not found'))
  })
})

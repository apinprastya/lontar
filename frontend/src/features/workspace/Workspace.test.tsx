import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('../../../bindings/dbedit/internal/services', () => import('../../test/mockBindings'))
vi.mock('../../lib/clipboard', () => ({ copyText: vi.fn(async () => {}) }))
vi.mock('@glideapps/glide-data-grid', async (importOriginal) => {
  const real = await importOriginal<typeof import('@glideapps/glide-data-grid')>()
  return {
    ...real,
    DataEditor: (await import('react')).forwardRef((p: {
      columns: { title: string }[]
      rows: number
      getCellContent(c: [number, number]): { displayData: string }
      onHeaderClicked(i: number, e: { shiftKey: boolean }): void
      onGridSelectionChange(s: unknown): void
      onKeyDown(e: unknown): void
    }, _ref: unknown) => (
      <div
        data-testid="fake-grid"
        tabIndex={0}
        onKeyDown={(e) => p.onKeyDown({ key: e.key, ctrlKey: e.ctrlKey, metaKey: e.metaKey, shiftKey: e.shiftKey, altKey: e.altKey, cancel() {}, preventDefault() {}, stopPropagation() {} })}
      >
        {p.columns.map((c, i) => (
          <button key={c.title} type="button" onClick={(e) => p.onHeaderClicked(i, { shiftKey: e.shiftKey })}>
            {c.title}
          </button>
        ))}
        {Array.from({ length: p.rows }, (_, r) => (
          <div
            key={r}
            role="row"
            onClick={() =>
              p.onGridSelectionChange({
                current: { cell: [0, r], range: { x: 0, y: r, width: 1, height: 1 }, rangeStack: [] },
                columns: real.CompactSelection.empty(),
                rows: real.CompactSelection.empty(),
              })
            }
          >
            {p.columns.map((_, c) => p.getCellContent([c, r]).displayData).join(' | ')}
          </div>
        ))}
      </div>
    )),
  }
})

import { initialConnectionsState, useConnections } from '../../stores/connections'
import { ConnectionService, makeConnection, makeSession, resetBindings } from '../../test/mockBindings'
import type { WorkspaceApi } from './api'
import type { FetchRequest, Page, TableInfo } from './types'
import Workspace from './Workspace'
import { draftKey } from '../../stores/queryTabs'
import { execResult, fakeEditorApi, resultSet, stmt } from '../../test/fakeEditorApi'

const tables: TableInfo[] = [
  { schema: 'public', name: 'users', type: 'table', estimatedRows: 12345 },
  { schema: 'public', name: 'orders', type: 'table', estimatedRows: 50 },
  { schema: 'public', name: 'active_users', type: 'view', estimatedRows: null },
  { schema: 'public', name: 'stats', type: 'materialized view', estimatedRows: null },
]

function page(req: FetchRequest): Page {
  return {
    columns: [
      { name: 'id', type: 'integer', isPK: true, nullable: false },
      { name: 'email', type: 'text', isPK: false, nullable: true },
    ],
    rows: [
      [{ k: 'num', v: req.offset + 1 }, { k: 'str', v: `${req.table}@x` }],
      [{ k: 'num', v: req.offset + 2 }, { k: 'null' }],
    ],
    offset: req.offset,
    limit: req.limit,
    hasMore: true,
    totalCount: null,
    elapsedMs: 7,
  }
}

function fakeApi() {
  return {
    current: vi.fn(async () => ({ database: 'shop', schema: 'public' })),
    listDatabases: vi.fn(async () => ['postgres', 'shop']),
    useDatabase: vi.fn(async (_s: string, db: string) => ({ database: db, schema: 'public' })),
    listSchemas: vi.fn(async () => ['public', 'audit']),
    listTables: vi.fn(async () => tables),
    fetchTable: vi.fn(async (_s: string, _r: string, req: FetchRequest) => page(req)),
    countRows: vi.fn(async () => 12001),
    cancel: vi.fn(async () => true),
  } satisfies WorkspaceApi
}

describe('Workspace', () => {
  let api: ReturnType<typeof fakeApi>

  beforeEach(() => {
    resetBindings()
    window.localStorage.clear()
    api = fakeApi()
    useConnections.setState({ ...initialConnectionsState, session: makeSession(), connections: [makeConnection({ id: 'c1', tag: 'prod' })] })
  })
  afterEach(cleanup)

  it('renders nothing without a session', () => {
    useConnections.setState({ session: null })
    const { container } = render(<Workspace api={api} />)
    expect(container).toBeEmptyDOMElement()
  })

  it('shows the sidebar with switchers and grouped tables', async () => {
    const user = userEvent.setup()
    render(<Workspace api={api} platform="linux" />)
    const sidebar = screen.getByLabelText('Sidebar')
    expect(sidebar).toHaveTextContent('Production')
    expect(sidebar).toHaveTextContent('PostgreSQL')
    expect(screen.getByTestId('conn-strip')).toHaveStyle({ background: '#f85149' })
    expect(screen.getByLabelText('Workspace')).toHaveStyle({ borderTopColor: '#f85149' })
    expect(await screen.findByRole('button', { name: /^users/ })).toBeInTheDocument()
    expect(screen.getByLabelText('Database')).toHaveValue('shop')
    expect(screen.getByLabelText('Schema')).toHaveValue('public')
    const groups = within(sidebar).getAllByRole('heading', { level: 3 })
    expect(groups.map((g) => g.textContent)).toEqual(['Tables 2', 'Views 2'])
    expect(sidebar).toHaveTextContent('12K')

    // jsdom has no layout, so the panel group treats every pointer as a
    // resize-handle hit and blocks focus; set the value directly.
    fireEvent.change(screen.getByLabelText('Search tables'), { target: { value: 'user' } })
    expect(within(sidebar).getAllByRole('heading', { level: 3 }).map((g) => g.textContent)).toEqual(['Tables 1', 'Views 1'])
    fireEvent.change(screen.getByLabelText('Search tables'), { target: { value: 'zzz' } })
    expect(sidebar).toHaveTextContent('No tables match')

    await user.selectOptions(screen.getByLabelText('Schema'), 'audit')
    expect(api.listTables).toHaveBeenLastCalledWith('s1', 'audit')
    await user.selectOptions(screen.getByLabelText('Database'), 'postgres')
    expect(api.useDatabase).toHaveBeenCalledWith('s1', 'postgres')
    await user.click(screen.getByRole('checkbox', { name: /Show system schemas/ }))
    expect(api.listSchemas).toHaveBeenLastCalledWith('s1', true)
    await user.click(screen.getByRole('button', { name: 'Refresh table list' }))
    expect(api.current).toHaveBeenCalledTimes(4)
  })

  it('shows sidebar errors inline', async () => {
    api.listTables.mockRejectedValue(new Error('permission denied for schema public'))
    render(<Workspace api={api} />)
    expect(await screen.findByRole('alert')).toHaveTextContent('permission denied for schema public')
  })

  it('opens a preview tab, pins on double click, sorts, pages and filters', async () => {
    const user = userEvent.setup()
    render(<Workspace api={api} platform="linux" />)
    expect(screen.getByText('Select a table in the sidebar to browse its rows.')).toBeInTheDocument()
    await user.click(await screen.findByRole('button', { name: /^users/ }))
    const tab = await screen.findByRole('tab', { name: /users/ })
    expect(tab).toHaveClass('tab-preview')
    expect(await screen.findByText('1 | users@x')).toBeInTheDocument()
    expect(screen.getByText('2 | NULL')).toBeInTheDocument()
    await waitFor(() => expect(screen.getByTestId('row-range')).toHaveTextContent('1–2 of 12,001'))
    expect(screen.getByTestId('query-time')).toHaveTextContent('7 ms')

    // Single click on another table replaces the preview.
    await user.click(screen.getByRole('button', { name: /^orders/ }))
    expect(screen.getAllByRole('tab')).toHaveLength(1)
    expect(await screen.findByText('1 | orders@x')).toBeInTheDocument()
    // Double click pins.
    await user.dblClick(screen.getByRole('button', { name: /^orders/ }))
    expect(screen.getByRole('tab', { name: /orders/ })).not.toHaveClass('tab-preview')
    await user.click(screen.getByRole('button', { name: /^users/ }))
    expect(screen.getAllByRole('tab')).toHaveLength(2)
    await user.dblClick(screen.getByRole('tab', { name: /users/ }))
    expect(screen.getByRole('tab', { name: /users/ })).not.toHaveClass('tab-preview')

    // Sort by header click; shift-click adds a secondary sort.
    await user.click(await screen.findByRole('button', { name: 'email' }))
    expect(api.fetchTable.mock.lastCall?.[2].sort).toEqual([{ column: 'email', desc: false }])
    await user.keyboard('{Shift>}')
    await user.click(await screen.findByRole('button', { name: 'id' }))
    await user.keyboard('{/Shift}')
    expect(api.fetchTable.mock.lastCall?.[2].sort).toEqual([
      { column: 'email', desc: false },
      { column: 'id', desc: false },
    ])

    // Paging.
    await user.click(screen.getByRole('button', { name: 'Next page' }))
    expect(api.fetchTable.mock.lastCall?.[2].offset).toBe(300)
    expect(await screen.findByText('301 | users@x')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Previous page' }))
    expect(api.fetchTable.mock.lastCall?.[2].offset).toBe(0)

    // Filter bar via the toolbar.
    await user.click(screen.getByRole('button', { name: 'Filter' }))
    await user.selectOptions(screen.getByLabelText('Operator 1'), 'isNull')
    await user.click(screen.getByRole('button', { name: 'Apply' }))
    expect(api.fetchTable.mock.lastCall?.[2].filters).toEqual([{ column: 'id', op: 'isNull', value: '', value2: '' }])
    await user.click(screen.getByRole('button', { name: 'Clear' }))
    expect(api.fetchTable.mock.lastCall?.[2].filters).toEqual([])

    // Close via the tab button.
    await user.click(screen.getByRole('button', { name: 'Close users' }))
    expect(screen.getAllByRole('tab')).toHaveLength(1)
  })

  it('shows backend errors inline in the tab and retries', async () => {
    api.fetchTable.mockRejectedValueOnce(new Error('relation "users" does not exist'))
    const user = userEvent.setup()
    render(<Workspace api={api} />)
    await user.click(await screen.findByRole('button', { name: /^users/ }))
    expect(await screen.findByRole('alert')).toHaveTextContent('relation "users" does not exist')
    await user.click(screen.getByRole('button', { name: 'Retry' }))
    expect(await screen.findByText('1 | users@x')).toBeInTheDocument()
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('shows loading with cancel, and empty tables', async () => {
    api.fetchTable.mockImplementationOnce(() => new Promise(() => {}))
    const user = userEvent.setup()
    render(<Workspace api={api} />)
    await user.click(await screen.findByRole('button', { name: /^users/ }))
    await user.click(await screen.findByRole('button', { name: 'Cancel' }))
    expect(api.cancel).toHaveBeenCalled()
    expect(await screen.findByRole('alert')).toHaveTextContent('Query cancelled.')
    api.fetchTable.mockImplementationOnce(async (_s, _r, req) => ({ ...page(req), rows: [], hasMore: false, totalCount: 0 }))
    await user.click(screen.getByRole('button', { name: 'Retry' }))
    expect(await screen.findByText('This table is empty.')).toBeInTheDocument()
    expect(screen.getByTestId('row-range')).toHaveTextContent('No rows')
  })

  it('shows the row detail panel for the selected row', async () => {
    const user = userEvent.setup()
    render(<Workspace api={api} platform="linux" />)
    await user.click(await screen.findByRole('button', { name: /^users/ }))
    await user.click(await screen.findByText('2 | NULL'))
    await user.click(screen.getByRole('button', { name: 'Row detail' }))
    const detail = screen.getByLabelText('Row detail')
    expect(detail).toHaveTextContent('Row 2')
    expect(detail).toHaveTextContent('email')
    expect(detail.querySelector('.null-value')).toHaveTextContent('NULL')
    // Space in the grid toggles it off.
    fireEvent.keyDown(screen.getByTestId('fake-grid'), { key: ' ' })
    expect(screen.queryByLabelText('Row detail')).toBeNull()
    fireEvent.keyDown(window, { key: 'i', ctrlKey: true })
    expect(screen.getByLabelText('Row detail')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Close row detail' }))
    expect(screen.queryByLabelText('Row detail')).toBeNull()
  })

  it('handles keyboard shortcuts', async () => {
    const user = userEvent.setup()
    render(<Workspace api={api} platform="linux" />)
    await user.dblClick(await screen.findByRole('button', { name: /^users/ }))
    await user.dblClick(screen.getByRole('button', { name: /^orders/ }))
    expect(screen.getAllByRole('tab')).toHaveLength(2)
    act(() => {
      fireEvent.keyDown(window, { key: '1', ctrlKey: true })
    })
    expect(screen.getByRole('tab', { name: /users/ })).toHaveAttribute('aria-selected', 'true')
    act(() => {
      fireEvent.keyDown(window, { key: 'f', ctrlKey: true })
    })
    expect(screen.getByRole('search', { name: 'Filters' })).toBeInTheDocument()
    const calls = api.fetchTable.mock.calls.length
    act(() => {
      fireEvent.keyDown(window, { key: 'r', ctrlKey: true })
    })
    expect(api.fetchTable.mock.calls.length).toBe(calls + 1)
    act(() => {
      fireEvent.keyDown(window, { key: 'w', ctrlKey: true })
    })
    expect(screen.getAllByRole('tab')).toHaveLength(1)
    // A plain key does nothing.
    fireEvent.keyDown(window, { key: 'w' })
    expect(screen.getAllByRole('tab')).toHaveLength(1)
    // Middle click closes a tab.
    fireEvent(screen.getByRole('tab', { name: /orders/ }), new MouseEvent('auxclick', { bubbles: true, button: 1 }))
    expect(screen.queryAllByRole('tab')).toHaveLength(0)
  })

  it('disconnects and refreshes from the toolbar', async () => {
    ConnectionService.Disconnect.mockResolvedValue()
    const user = userEvent.setup()
    render(<Workspace api={api} />)
    await screen.findByRole('button', { name: /^users/ })
    await user.click(screen.getByRole('button', { name: /^Refresh$/ }))
    expect(api.current).toHaveBeenCalledTimes(2)
    await user.click(screen.getByRole('button', { name: 'Disconnect' }))
    expect(ConnectionService.Disconnect).toHaveBeenCalledWith('s1')
    expect(useConnections.getState().session).toBeNull()
  })

  it('adapts the sidebar to MySQL and SQLite', async () => {
    useConnections.setState({ session: makeSession({ kind: 'mysql', connectionId: 'gone' }) })
    api.current.mockResolvedValue({ database: '', schema: '' })
    api.listSchemas.mockResolvedValue([])
    const { unmount } = render(<Workspace api={api} />)
    expect(await screen.findByText('Choose a database to see its tables.')).toBeInTheDocument()
    expect(screen.queryByLabelText('Schema')).toBeNull()
    expect(screen.getByLabelText('Database')).toHaveTextContent('— choose —')
    expect(screen.getByText(/Show system databases/)).toBeInTheDocument()
    unmount()

    useConnections.setState({ session: makeSession({ id: 's2', kind: 'sqlite' }) })
    api.current.mockResolvedValue({ database: 'main', schema: 'main' })
    api.listSchemas.mockResolvedValue(['main'])
    api.listTables.mockResolvedValue([])
    render(<Workspace api={api} />)
    expect(await screen.findByText('No tables.')).toBeInTheDocument()
    expect(screen.queryByLabelText('Database')).toBeNull()
    expect(screen.queryByLabelText('Schema')).toBeNull()
  })
})

describe('Workspace SQL tabs', () => {
  let api: ReturnType<typeof fakeApi>
  let editor: ReturnType<typeof fakeEditorApi>

  beforeEach(() => {
    resetBindings()
    window.localStorage.clear()
    api = fakeApi()
    editor = fakeEditorApi()
    useConnections.setState({ ...initialConnectionsState, session: makeSession(), connections: [makeConnection({ id: 'c1' })] })
  })
  afterEach(() => {
    cleanup()
    window.localStorage.clear()
  })

  it('opens, runs, renames and closes query tabs with shortcuts', async () => {
    const user = userEvent.setup()
    render(<Workspace api={api} editor={editor} platform="linux" />)
    await screen.findByRole('button', { name: /^users/ })
    expect(screen.getByText('Ctrl+T opens a new SQL query')).toBeInTheDocument()
    act(() => {
      fireEvent.keyDown(window, { key: 't', ctrlKey: true })
    })
    expect(screen.getByRole('tab', { name: /Query 1/ })).toHaveAttribute('aria-selected', 'true')
    expect(screen.getByLabelText('SQL editor')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'New query' }))
    expect(screen.getByRole('tab', { name: /Query 2/ })).toHaveAttribute('aria-selected', 'true')

    // Rename by double-click.
    await user.dblClick(screen.getByRole('tab', { name: /Query 2/ }))
    const input = screen.getByLabelText('Tab name')
    fireEvent.change(input, { target: { value: 'Report' } })
    fireEvent.keyDown(input, { key: 'Enter' })
    expect(screen.getByRole('tab', { name: /Report/ })).toBeInTheDocument()
    await user.dblClick(screen.getByRole('tab', { name: /Report/ }))
    fireEvent.change(screen.getByLabelText('Tab name'), { target: { value: 'x' } })
    fireEvent.keyDown(screen.getByLabelText('Tab name'), { key: 'Escape' })
    expect(screen.getByRole('tab', { name: /Report/ })).toBeInTheDocument()
    await user.dblClick(screen.getByRole('tab', { name: /Report/ }))
    fireEvent.blur(screen.getByLabelText('Tab name'))
    expect(screen.queryByLabelText('Tab name')).not.toBeInTheDocument()

    // Mod+F opens the editor search, not the filter bar.
    act(() => {
      fireEvent.keyDown(window, { key: 'f', ctrlKey: true })
    })
    await waitFor(() => expect(document.querySelector('.cm-search')).not.toBeNull())
    expect(screen.queryByRole('search', { name: 'Filters' })).not.toBeInTheDocument()

    act(() => {
      fireEvent.keyDown(window, { key: 'w', ctrlKey: true })
    })
    expect(screen.getAllByRole('tab')).toHaveLength(1)
  })

  it('restores drafts, runs with Mod+Enter and shows results in the status bar and row detail', async () => {
    window.localStorage.setItem(draftKey('c1'), JSON.stringify([{ title: 'Saved', sql: 'SELECT * FROM users' }]))
    editor.execute.mockResolvedValue(execResult([stmt(0, 'SELECT * FROM users', { resultSets: [resultSet('r1', 2, { truncated: true })] })]))
    render(<Workspace api={api} editor={editor} platform="linux" />)
    expect(screen.getByRole('tab', { name: /Saved/ })).toHaveAttribute('aria-selected', 'true')
    act(() => {
      fireEvent.keyDown(window, { key: 'Enter', ctrlKey: true })
    })
    await waitFor(() => expect(screen.getByTestId('row-range')).toHaveTextContent('2+ rows'))
    expect(editor.execute).toHaveBeenCalledWith('ed-1', expect.any(String), 'SELECT * FROM users', expect.objectContaining({ baseOffset: 0 }))
    expect(screen.getByTestId('query-time')).toHaveTextContent('3.25 ms')

    // Row detail of the selected result row.
    fireEvent.click(screen.getAllByRole('row')[1])
    act(() => {
      fireEvent.keyDown(window, { key: 'i', ctrlKey: true })
    })
    expect(screen.getByLabelText('Row detail')).toHaveTextContent('Row 2')
    expect(screen.getByLabelText('Row detail')).toHaveTextContent('n2')

    // Run all, and Esc stops a running query.
    let finish: (v: unknown) => void = () => {}
    editor.execute.mockImplementationOnce(() => new Promise((r) => (finish = r)) as never)
    act(() => {
      fireEvent.keyDown(window, { key: 'Enter', ctrlKey: true, shiftKey: true })
    })
    await waitFor(() => expect(screen.getByText('Stop')).toBeEnabled())
    act(() => {
      fireEvent.keyDown(window, { key: 'Escape' })
    })
    expect(editor.cancel).toHaveBeenCalled()
    await act(async () => finish(execResult([])))
    await waitFor(() => expect(screen.getByTestId('row-range')).toHaveTextContent('0 statements'))
    // Escape with nothing running is not consumed.
    const ev = new KeyboardEvent('keydown', { key: 'Escape', cancelable: true })
    window.dispatchEvent(ev)
    expect(ev.defaultPrevented).toBe(false)
  })
})

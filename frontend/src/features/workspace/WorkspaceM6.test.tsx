import { act, cleanup, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { makeDetail } from '../../test/structure'

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
vi.mock('../../lib/events', () => ({ onEvent: () => () => {} }))

import { initialConnectionsState, useConnections } from '../../stores/connections'
import { useSettings } from '../../stores/settings'
import { DEFAULT_SETTINGS } from '../settings/types'
import { makeConnection, makeSession, resetBindings } from '../../test/mockBindings'
import type { WorkspaceApi } from './api'
import type { FetchRequest, Page, TableInfo } from './types'
import Workspace from './Workspace'
import { fakeEditorApi } from '../../test/fakeEditorApi'

const tables: TableInfo[] = [
  { schema: 'public', name: 'users', type: 'table', estimatedRows: 10 },
  { schema: 'public', name: 'orders', type: 'table', estimatedRows: 5 },
]

function page(req: FetchRequest): Page {
  return {
    columns: [{ name: 'id', type: 'integer', isPK: true, nullable: false }],
    rows: [[{ k: 'num', v: 1 }]],
    offset: req.offset,
    limit: req.limit,
    hasMore: false,
    totalCount: 1,
    elapsedMs: 1,
  }
}

function fakeApi() {
  return {
    current: vi.fn(async () => ({ database: 'shop', schema: 'public' })),
    listDatabases: vi.fn(async () => ['shop']),
    useDatabase: vi.fn(async () => ({ database: 'shop', schema: 'public' })),
    listSchemas: vi.fn(async () => ['public', 'audit']),
    listTables: vi.fn(async (_s: string, schema: string) =>
      schema === 'audit' ? [{ schema: 'audit', name: 'log', type: 'table' as const, estimatedRows: 1 }] : tables,
    ),
    fetchTable: vi.fn(async (_s: string, _r: string, req: FetchRequest) => page(req)),
    countRows: vi.fn(async () => 1),
    cancel: vi.fn(async () => true),
    tableInfo: vi.fn(async (_s: string, schema: string, table: string) => makeDetail({ schema, name: table })),
    tableDDL: vi.fn(async () => ''),
  } satisfies WorkspaceApi
}

const key = (k: string, o: KeyboardEventInit = {}) => {
  const ev = new KeyboardEvent('keydown', { key: k, ctrlKey: true, bubbles: true, cancelable: true, ...o })
  act(() => {
    window.dispatchEvent(ev)
  })
  return ev
}

describe('Workspace M6: palette, export, import, settings', () => {
  let api: ReturnType<typeof fakeApi>

  beforeEach(() => {
    resetBindings()
    window.localStorage.clear()
    api = fakeApi()
    useSettings.setState({ settings: { ...DEFAULT_SETTINGS, gridPageSize: 50, editorMaxRows: 2000 }, open: false })
    useConnections.setState({ ...initialConnectionsState, session: makeSession({ tunneled: true }), connections: [makeConnection({ id: 'c1' })] })
  })
  afterEach(cleanup)

  it('opens tables with Mod+P (data and structure) and blocks print', async () => {
    const user = userEvent.setup()
    render(<Workspace api={api} platform="linux" editor={fakeEditorApi()} />)
    await screen.findByRole('button', { name: /^users/ })
    expect(screen.getByTestId('ssh-badge')).toHaveTextContent('SSH')
    const ev = key('p')
    expect(ev.defaultPrevented).toBe(true)
    const palette = await screen.findByRole('dialog', { hidden: true }).catch(() => document.querySelector('.palette') as HTMLElement)
    expect(palette).toBeTruthy()
    // Other schemas are listed too (qualified).
    expect(await screen.findByText('audit.log')).toBeInTheDocument()
    await user.keyboard('orders{Enter}')
    await waitFor(() => expect(api.fetchTable).toHaveBeenCalledWith('s1', expect.any(String), expect.objectContaining({ table: 'orders', limit: 50 })))
    expect(document.querySelector('.palette')).toBeNull()

    key('p')
    await user.keyboard('users{Control>}{Enter}{/Control}')
    await waitFor(() => expect(api.tableInfo).toHaveBeenCalledWith('s1', 'public', 'users'))
    expect(await screen.findByRole('button', { name: 'Structure', pressed: true }).catch(() => screen.getAllByText(/Columns/)[0])).toBeTruthy()
  })

  it('runs commands from Mod+Shift+P and the toolbar export/import', async () => {
    const user = userEvent.setup()
    render(<Workspace api={api} platform="linux" editor={fakeEditorApi()} />)
    await user.click(await screen.findByRole('button', { name: /^users/ }))
    await waitFor(() => expect(api.fetchTable).toHaveBeenCalled())

    key('P', { shiftKey: true })
    expect(await screen.findByText('Show all commands').catch(() => null)).toBeNull() // the palette command itself is not listed
    await user.keyboard('new sql{Enter}')
    await waitFor(() => expect(screen.getByRole('tab', { name: /Query 1/ })).toBeInTheDocument())

    // While the palette is open, workspace shortcuts are ignored.
    key('p')
    key('w')
    expect(screen.getByRole('tab', { name: /Query 1/ })).toBeInTheDocument()
    await user.keyboard('{Escape}')

    await user.click(screen.getByRole('tab', { name: /users/ }))
    await user.click(screen.getByRole('button', { name: 'Export…' }))
    expect(screen.getByRole('heading', { name: 'Export users' })).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Cancel' }))
    await user.click(screen.getByRole('button', { name: 'Import…' }))
    expect(screen.getByRole('heading', { name: 'Import CSV into public.users' })).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Cancel' }))

    // Settings via Mod+, is handled by App; via the palette here.
    key('P', { shiftKey: true })
    await user.keyboard('settings{Enter}')
    expect(useSettings.getState().open).toBe(true)
  })

  it('asks before closing a query tab with unrun changes (setting)', async () => {
    useSettings.setState({ settings: { ...DEFAULT_SETTINGS, confirmCloseDirtyQuery: true } })
    const user = userEvent.setup()
    render(<Workspace api={api} platform="linux" editor={fakeEditorApi()} />)
    await screen.findByRole('button', { name: /^users/ })
    key('t')
    const tab = await screen.findByRole('tab', { name: /Query 1/ })
    // An empty tab closes at once.
    key('w')
    await waitFor(() => expect(tab).not.toBeInTheDocument())
    key('t')
    const tab2 = await screen.findByRole('tab', { name: /Query 1/ })
    const { EditorView } = await import('@codemirror/view')
    const view = EditorView.findFromDOM(document.querySelector('.cm-editor') as HTMLElement)!
    act(() => view.dispatch({ changes: { from: 0, insert: 'SELECT 1' } }))
    await new Promise((r) => setTimeout(r, 0))
    key('w')
    const dlg = await screen.findByRole('alertdialog')
    expect(dlg).toHaveTextContent('Close query tab?')
    await user.click(within(dlg).getByRole('button', { name: 'Cancel' }))
    expect(tab2).toBeInTheDocument()
    key('w')
    await user.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Close tab' }))
    await waitFor(() => expect(tab2).not.toBeInTheDocument())
  })
})

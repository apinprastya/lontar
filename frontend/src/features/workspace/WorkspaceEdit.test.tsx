import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { makeDetail } from '../../test/structure'

vi.mock('../../../bindings/lontar/internal/services', () => import('../../test/mockBindings'))
vi.mock('../../lib/clipboard', () => ({ copyText: vi.fn(async () => {}) }))
vi.mock('@glideapps/glide-data-grid', async (importOriginal) => {
  const real = await importOriginal<typeof import('@glideapps/glide-data-grid')>()
  const React = await import('react')
  type P = {
    columns: { title: string }[]
    rows: number
    getCellContent(c: [number, number]): { displayData: string }
    onGridSelectionChange(s: unknown): void
    onKeyDown(e: unknown): void
    onCellActivated?(c: [number, number]): void
  }
  return {
    ...real,
    DataEditor: React.forwardRef((p: P, _ref: unknown) => (
      <div
        data-testid="fake-grid"
        tabIndex={0}
        onKeyDown={(e) => p.onKeyDown({ key: e.key, ctrlKey: e.ctrlKey, metaKey: e.metaKey, shiftKey: e.shiftKey, altKey: e.altKey, cancel() {}, preventDefault() {}, stopPropagation() {} })}
      >
        {Array.from({ length: p.rows }, (_, r) => (
          <div key={r} role="row">
            {p.columns.map((_, c) => (
              <span
                key={c}
                data-testid={`cell-${c}-${r}`}
                onClick={() =>
                  p.onGridSelectionChange({
                    current: { cell: [c, r], range: { x: c, y: r, width: 1, height: 1 }, rangeStack: [] },
                    columns: real.CompactSelection.empty(),
                    rows: real.CompactSelection.empty(),
                  })
                }
                onDoubleClick={() => p.onCellActivated?.([c, r])}
              >
                {p.getCellContent([c, r]).displayData}
              </span>
            ))}
          </div>
        ))}
      </div>
    )),
  }
})

import { initialConnectionsState, useConnections } from '../../stores/connections'
import { ConnectionService, makeConnection, makeSession, resetBindings } from '../../test/mockBindings'
import type { WorkspaceApi } from './api'
import type { EditApi } from '../edits/api'
import type { ApplyResult } from '../edits/types'
import type { FetchRequest, Page, TableInfo } from './types'
import Workspace, { SafeModeBadge } from './Workspace'
import { pasteChanges } from './TableTab'
import { displayRows, emptyPending } from '../../stores/pending'

const tables: TableInfo[] = [
  { schema: 'public', name: 'users', type: 'table', estimatedRows: 2 },
  { schema: 'public', name: 'active_users', type: 'view', estimatedRows: null },
]

function page(req: FetchRequest): Page {
  return {
    columns: [
      { name: 'id', type: 'integer', isPK: true, nullable: false },
      { name: 'email', type: 'text', isPK: false, nullable: true },
    ],
    rows: [
      [{ k: 'num', v: req.offset + 1 }, { k: 'str', v: 'a@x' }],
      [{ k: 'num', v: req.offset + 2 }, { k: 'null' }],
    ],
    offset: req.offset,
    limit: req.limit,
    hasMore: true,
    totalCount: null,
    elapsedMs: 3,
  }
}

function fakeApi() {
  return {
    current: vi.fn(async () => ({ database: 'shop', schema: 'public' })),
    listDatabases: vi.fn(async () => ['shop']),
    useDatabase: vi.fn(async (_s: string, db: string) => ({ database: db, schema: 'public' })),
    listSchemas: vi.fn(async () => ['public']),
    listTables: vi.fn(async () => tables),
    fetchTable: vi.fn(async (_s: string, _r: string, req: FetchRequest) => page(req)),
    countRows: vi.fn(async () => 2),
    cancel: vi.fn(async () => true),
    tableInfo: vi.fn(async (_s: string, schema: string, table: string) => makeDetail({ schema, name: table })),
    tableDDL: vi.fn(async (_s: string, _schema: string, table: string) => `CREATE TABLE ${table} ();`),
  } satisfies WorkspaceApi
}

function fakeEdits() {
  return {
    preview: vi.fn(async () => [{ op: 'update' as const, index: 0, sql: "UPDATE \"public\".\"users\" SET \"email\" = 'b@x' WHERE \"id\" = 1;" }]),
    apply: vi.fn(async (): Promise<ApplyResult> => ({ ok: true, result: { deleted: 0, updated: 1, inserted: 0, insertedKeys: [] }, failure: null })),
  } satisfies EditApi
}

/** Sets an editor's text and presses Enter (jsdom: the panel group blocks pointer focus). */
function typeInto(el: HTMLElement, text: string) {
  fireEvent.change(el, { target: { value: text } })
  fireEvent.keyDown(el, { key: 'Enter' })
}

describe('Workspace editing', () => {
  let api: ReturnType<typeof fakeApi>
  let edits: ReturnType<typeof fakeEdits>
  const start = async (safeMode: 'off' | 'confirm' | 'readonly' = 'off', table = /^users/) => {
    useConnections.setState({ ...initialConnectionsState, session: makeSession({ safeMode }), connections: [makeConnection({ id: 'c1' })] })
    const user = userEvent.setup()
    render(<Workspace api={api} edits={edits} platform="linux" />)
    await user.click(await screen.findByRole('button', { name: table }))
    await screen.findByTestId('cell-0-0')
    return user
  }

  beforeEach(() => {
    resetBindings()
    window.localStorage.clear()
    api = fakeApi()
    edits = fakeEdits()
    ConnectionService.Disconnect.mockResolvedValue()
  })
  afterEach(cleanup)

  it('edits a cell, previews and commits', async () => {
    const user = await start()
    expect(screen.queryByTestId('safe-mode')).toBeNull()
    fireEvent.doubleClick(screen.getByTestId('cell-1-0'))
    typeInto(screen.getByRole('textbox', { name: 'Edit email' }), 'b@x')
    expect(screen.getByTestId('pending')).toHaveTextContent('1 pending change')
    expect(screen.getByTestId('cell-1-0')).toHaveTextContent('b@x')
    await user.click(screen.getByRole('button', { name: 'Preview SQL' }))
    expect(await screen.findByTestId('preview-sql')).toHaveTextContent('UPDATE "public"."users"')
    await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Commit' }))
    await waitFor(() => expect(screen.queryByTestId('pending')).toBeNull())
    expect(edits.apply).toHaveBeenCalledTimes(1)
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  it('shows a failed commit and keeps the changes', async () => {
    edits.apply.mockResolvedValue({ ok: false, result: null, failure: { op: 'update', index: 0, kind: 'stale', message: 'update #1 failed: gone.' } })
    const user = await start()
    await user.click(screen.getByTestId('cell-1-1'))
    fireEvent.keyDown(screen.getByTestId('fake-grid'), { key: 'z' })
    typeInto(screen.getByRole('textbox', { name: 'Edit email' }), 'zed')
    // Mod+S commits directly in off mode.
    fireEvent.keyDown(window, { key: 's', ctrlKey: true })
    expect(await screen.findByTestId('commit-error')).toHaveTextContent('update #1 failed: gone.')
    expect(screen.getByTestId('commit-error')).toHaveTextContent('failing row is highlighted')
    expect(screen.getByTestId('pending')).toHaveTextContent('1 pending change')
    await user.click(screen.getByRole('button', { name: 'Discard' }))
    expect(screen.queryByTestId('pending')).toBeNull()
    expect(screen.queryByTestId('commit-error')).toBeNull()
  })

  it('inserts rows and edits in the row detail', async () => {
    const user = await start()
    await user.click(screen.getByRole('button', { name: 'Insert row' }))
    expect(screen.getByTestId('pending')).toHaveTextContent('1 pending change')
    expect(screen.getByTestId('cell-1-2')).toHaveTextContent('DEFAULT')
    await user.click(screen.getByTestId('cell-1-0'))
    await user.click(screen.getByRole('button', { name: 'Row detail' }))
    const detail = screen.getByLabelText('Row detail', { selector: 'aside' })
    await user.click(within(detail).getByRole('button', { name: 'Edit email' }))
    typeInto(within(detail).getByRole('textbox', { name: 'Edit email' }), 'detail@x')
    expect(screen.getByTestId('pending')).toHaveTextContent('2 pending changes')
    expect(within(detail).getByText('detail@x')).toBeInTheDocument()
    // Undo (Mod+Z outside text fields) removes the last change.
    fireEvent.keyDown(window, { key: 'z', ctrlKey: true })
    expect(screen.getByTestId('pending')).toHaveTextContent('1 pending change')
    fireEvent.keyDown(window, { key: 'Z', ctrlKey: true, shiftKey: true })
    expect(screen.getByTestId('pending')).toHaveTextContent('2 pending changes')
    // Cancel an inline edit.
    await user.click(within(detail).getByRole('button', { name: 'Edit email' }))
    fireEvent.keyDown(within(detail).getByRole('textbox'), { key: 'Escape' })
    expect(within(detail).queryByRole('textbox')).toBeNull()
  })

  it('asks before losing changes (paging, disconnect)', async () => {
    const user = await start()
    await user.click(screen.getByRole('button', { name: 'Insert row' }))
    await user.click(screen.getByRole('button', { name: 'Next page' }))
    const dlg = screen.getByRole('alertdialog')
    expect(dlg).toHaveTextContent('1 uncommitted change')
    await user.click(within(dlg).getByRole('button', { name: 'Cancel' }))
    expect(screen.queryByRole('alertdialog')).toBeNull()
    await user.click(screen.getByRole('button', { name: 'Disconnect' }))
    await user.click(within(screen.getByRole('alertdialog')).getByRole('button', { name: 'Discard' }))
    await waitFor(() => expect(ConnectionService.Disconnect).toHaveBeenCalledWith('s1'))
  })

  it('previews first in confirm mode', async () => {
    const user = await start('confirm')
    expect(screen.getByTestId('safe-mode')).toHaveTextContent('Confirm writes')
    await user.click(screen.getByRole('button', { name: 'Insert row' }))
    fireEvent.keyDown(window, { key: 's', ctrlKey: true })
    expect(await screen.findByTestId('preview-sql')).toBeInTheDocument()
    expect(edits.apply).not.toHaveBeenCalled()
    await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Cancel' }))
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  it('disables editing on read-only connections and views', async () => {
    await start('readonly')
    expect(screen.getByTestId('safe-mode')).toHaveTextContent('Read-only')
    expect(screen.getByTestId('readonly-banner')).toHaveTextContent('read-only safe mode')
    expect(screen.queryByRole('button', { name: 'Insert row' })).toBeNull()
    fireEvent.doubleClick(screen.getByTestId('cell-1-0'))
    expect(screen.queryByRole('textbox', { name: 'Edit email' })).toBeNull()
    cleanup()
    await start('off', /^active_users/)
    expect(screen.getByTestId('readonly-banner')).toHaveTextContent('views are read-only')
  })

  it('renders the safe-mode badge', () => {
    const { container, rerender } = render(<SafeModeBadge mode="off" />)
    expect(container).toBeEmptyDOMElement()
    rerender(<SafeModeBadge mode={undefined} />)
    expect(container).toBeEmptyDOMElement()
    rerender(<SafeModeBadge mode="readonly" />)
    expect(screen.getByTestId('safe-mode')).toHaveAttribute('title', 'Read-only: the database rejects writes')
  })

  it('turns pasted TSV into cell changes', () => {
    const cols = page({ offset: 0 } as FetchRequest).columns
    const rows = page({ offset: 0 } as FetchRequest).rows
    const d = displayRows(cols, rows, emptyPending)
    d[1] = { ...d[1], state: 'deleted' }
    d[0] = { ...d[0], locked: [true, false] }
    expect(pasteChanges({ columns: cols }, d, 0, 0, [['9', 'x', 'extra'], ['8', 'y'], ['7', 'z']])).toEqual([
      { ref: { type: 'row', index: 0 }, column: 'email', value: { kind: 'value', text: 'x' } },
    ])
  })
})

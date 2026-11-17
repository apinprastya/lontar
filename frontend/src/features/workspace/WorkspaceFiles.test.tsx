import { act, cleanup, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('../../../bindings/lontar/internal/services', () => import('../../test/mockBindings'))
vi.mock('../grid/DataGrid', () => ({ default: () => <div data-testid="grid" /> }))

const { listeners } = vi.hoisted(() => ({ listeners: new Map<string, (data: unknown) => void>() }))
vi.mock('../../lib/events', () => ({
  onEvent: (name: string, cb: (data: unknown) => void) => {
    listeners.set(name, cb)
    return () => listeners.delete(name)
  },
}))

import { EditorView } from '@codemirror/view'
import { initialConnectionsState, useConnections } from '../../stores/connections'
import { makeConnection, makeSession, resetBindings } from '../../test/mockBindings'
import { fakeEditorApi, fakeSqlFileApi, sqlFile } from '../../test/fakeEditorApi'
import { makeDetail } from '../../test/structure'
import type { WorkspaceApi } from './api'
import Workspace, { FILES_DROPPED } from './Workspace'

function fakeApi(): WorkspaceApi {
  return {
    current: vi.fn(async () => ({ database: 'shop', schema: 'public' })),
    listDatabases: vi.fn(async () => ['shop']),
    useDatabase: vi.fn(async (_s: string, db: string) => ({ database: db, schema: 'public' })),
    listSchemas: vi.fn(async () => ['public']),
    listTables: vi.fn(async () => []),
    fetchTable: vi.fn(),
    countRows: vi.fn(async () => 0),
    cancel: vi.fn(async () => true),
    tableInfo: vi.fn(async () => makeDetail()),
    tableDDL: vi.fn(async () => ''),
  }
}

function fileError(code: string) {
  return Object.assign(new Error(code), { cause: { kind: 'fileError', code, path: '/w/a.sql', message: code } })
}

describe('Workspace with .sql files', () => {
  let files: ReturnType<typeof fakeSqlFileApi>

  beforeEach(() => {
    resetBindings()
    window.localStorage.clear()
    listeners.clear()
    files = fakeSqlFileApi()
    useConnections.setState({ ...initialConnectionsState, session: makeSession(), connections: [makeConnection({ id: 'c1' })] })
  })
  afterEach(cleanup)

  const mount = () => render(<Workspace platform="linux" api={fakeApi()} editor={fakeEditorApi()} files={files} />)
  const tabBar = () => screen.getByRole('tablist', { name: 'Open tabs' })
  /** Types at the start of the SQL editor (jsdom has no contenteditable input). */
  const type = (text: string) => {
    const view = EditorView.findFromDOM(document.querySelector('.cm-editor') as HTMLElement)!
    act(() => view.dispatch({ changes: { from: 0, insert: text } }))
  }

  it('opens a file from the toolbar, marks unsaved changes and saves', async () => {
    const user = userEvent.setup()
    files.open.mockResolvedValueOnce(sqlFile('/w/report.sql', 'SELECT 1;\n'))
    mount()
    await user.click(screen.getByRole('button', { name: 'Open SQL file…' }))
    const tab = await within(tabBar()).findByRole('tab', { name: /report\.sql/ })
    expect(tab).toHaveAttribute('title', expect.stringContaining('/w/report.sql'))
    expect(within(tab).queryByRole('img', { name: 'unsaved changes' })).toBeNull()
    expect(screen.queryByTestId('save-dirty')).toBeNull()

    type('-- more')
    await waitFor(() => expect(within(tab).getByRole('img', { name: 'unsaved changes' })).toBeInTheDocument())
    expect(screen.getByTestId('save-dirty')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Save' })).toHaveAttribute('title', expect.stringContaining('unsaved changes'))

    await user.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(within(tab).queryByRole('img', { name: 'unsaved changes' })).toBeNull())
    expect(files.save).toHaveBeenCalledWith('/w/report.sql', 'report', expect.stringContaining('-- more'), '\n', 1000)
  })

  it('asks Save / Don\'t save / Cancel when a changed file tab closes', async () => {
    const user = userEvent.setup()
    files.open.mockResolvedValue(sqlFile('/w/a.sql', 'one'))
    mount()
    await user.click(screen.getByRole('button', { name: 'Open SQL file…' }))
    await within(tabBar()).findByRole('tab', { name: /a\.sql/ })
    type('X')

    await user.click(screen.getByRole('button', { name: 'Close a.sql' }))
    const ask = await screen.findByRole('alertdialog', { name: 'Save changes to a.sql?' })
    await user.click(within(ask).getByRole('button', { name: 'Cancel' }))
    expect(screen.queryByRole('alertdialog')).toBeNull()
    expect(within(tabBar()).getByRole('tab', { name: /a\.sql/ })).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Close a.sql' }))
    await user.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(screen.queryByRole('tablist', { name: 'Open tabs' })).toBeNull())
    expect(files.save).toHaveBeenCalledTimes(1)

    // Don't save.
    await user.click(screen.getByRole('button', { name: 'Open SQL file…' }))
    await within(tabBar()).findByRole('tab', { name: /a\.sql/ })
    type('Y')
    await user.click(screen.getByRole('button', { name: 'Close a.sql' }))
    await user.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: "Don't save" }))
    await waitFor(() => expect(screen.queryByRole('tablist', { name: 'Open tabs' })).toBeNull())
    expect(files.save).toHaveBeenCalledTimes(1)
  })

  it('offers Overwrite when the file changed on disk', async () => {
    const user = userEvent.setup()
    files.open.mockResolvedValueOnce(sqlFile('/w/a.sql', 'one'))
    files.save.mockRejectedValueOnce(fileError('changed_on_disk'))
    mount()
    await user.click(screen.getByRole('button', { name: 'Open SQL file…' }))
    await within(tabBar()).findByRole('tab', { name: /a\.sql/ })
    type('Z')
    await user.click(screen.getByRole('button', { name: 'Save' }))
    const dlg = await screen.findByRole('alertdialog', { name: 'File changed on disk' })
    expect(dlg).toHaveTextContent('a.sql was changed by another program')
    await user.click(within(dlg).getByRole('button', { name: 'Cancel' }))
    expect(files.save).toHaveBeenCalledTimes(1)

    files.save.mockRejectedValueOnce(fileError('changed_on_disk'))
    await user.click(screen.getByRole('button', { name: 'Save' }))
    await user.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Overwrite' }))
    await waitFor(() => expect(files.save).toHaveBeenCalledTimes(3))
    expect(files.save).toHaveBeenLastCalledWith('/w/a.sql', 'a', expect.any(String), '\n', 0)
  })

  it('shows file errors in a dismissible banner', async () => {
    const user = userEvent.setup()
    files.open.mockRejectedValueOnce(fileError('too_large'))
    mount()
    await user.click(screen.getByRole('button', { name: 'Open SQL file…' }))
    const banner = await screen.findByTestId('file-error')
    expect(banner).toHaveTextContent('too_large')
    await user.click(within(banner).getByRole('button', { name: 'Dismiss' }))
    expect(screen.queryByTestId('file-error')).toBeNull()
  })

  it('opens dropped .sql files and marks the workspace as a drop target', async () => {
    const { container } = mount()
    expect(container.querySelector('section.ws')).toHaveAttribute('data-file-drop-target')
    const drop = listeners.get(FILES_DROPPED)
    expect(drop).toBeDefined()
    await act(async () => {
      drop?.(['/d/one.sql', '/d/two.sql'])
    })
    await waitFor(() => expect(within(tabBar()).getAllByRole('tab')).toHaveLength(2))
    expect(files.read).toHaveBeenCalledWith('/d/one.sql')
    await act(async () => {
      drop?.(null)
    })
    expect(files.read).toHaveBeenCalledTimes(2)
  })
})

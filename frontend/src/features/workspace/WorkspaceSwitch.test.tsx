import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { makeDetail } from '../../test/structure'

vi.mock('../../../bindings/lontar/internal/services', () => import('../../test/mockBindings'))
vi.mock('../../lib/events', () => ({ onEvent: () => () => {} }))
vi.mock('@glideapps/glide-data-grid', async (importOriginal) => {
  const real = await importOriginal<typeof import('@glideapps/glide-data-grid')>()
  return { ...real, DataEditor: (await import('react')).forwardRef(() => <div data-testid="fake-grid" />) }
})

import { initialConnectionsState, useConnections } from '../../stores/connections'
import { useSettings } from '../../stores/settings'
import { DEFAULT_SETTINGS } from '../settings/types'
import { ConnectionService, makeConnection, makeSession, resetBindings } from '../../test/mockBindings'
import type { WorkspaceApi } from './api'
import type { FetchRequest } from './types'
import Workspace from './Workspace'
import { fakeEditorApi } from '../../test/fakeEditorApi'

function fakeApi() {
  return {
    current: vi.fn(async () => ({ database: 'shop', schema: 'public' })),
    listDatabases: vi.fn(async () => ['shop']),
    useDatabase: vi.fn(async () => ({ database: 'shop', schema: 'public' })),
    listSchemas: vi.fn(async () => ['public']),
    listTables: vi.fn(async () => [{ schema: 'public', name: 'users', type: 'table' as const, estimatedRows: 1 }]),
    fetchTable: vi.fn(async (_s: string, _r: string, req: FetchRequest) => ({
      columns: [
        { name: 'id', type: 'integer', isPK: true, nullable: false },
        { name: 'name', type: 'text', isPK: false, nullable: true },
      ],
      rows: [[{ k: 'num' as const, v: 1 }, { k: 'str' as const, v: 'a' }]],
      offset: req.offset,
      limit: req.limit,
      hasMore: false,
      totalCount: 1,
      elapsedMs: 1,
    })),
    countRows: vi.fn(async () => 1),
    cancel: vi.fn(async () => true),
    tableInfo: vi.fn(async () => makeDetail()),
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
const palette = () => document.querySelector('.palette') as HTMLElement | null

describe('switching connections from the workspace', () => {
  let api: ReturnType<typeof fakeApi>

  beforeEach(() => {
    resetBindings()
    window.localStorage.clear()
    api = fakeApi()
    useSettings.setState({ settings: { ...DEFAULT_SETTINGS }, open: false })
    useConnections.setState({
      ...initialConnectionsState,
      session: makeSession(),
      sessions: [makeSession()],
      connections: [makeConnection({ id: 'c1', name: 'Production' }), makeConnection({ id: 'c2', name: 'Staging', color: '#ff0000' })],
    })
  })
  afterEach(cleanup)

  it('switches with Mod+K and keeps the other connection open with its tabs', async () => {
    const user = userEvent.setup()
    render(<Workspace api={api} platform="linux" editor={fakeEditorApi()} />)
    await user.click(await screen.findByRole('button', { name: /^users/ }))
    expect(await screen.findByRole('tab', { name: /users/ })).toBeInTheDocument()
    expect(key('k').defaultPrevented).toBe(true)
    expect(within(palette()!).getByText('Production').closest('[cmdk-item]')).toHaveTextContent('Current')

    ConnectionService.Connect.mockResolvedValueOnce(makeSession({ id: 's2', connectionId: 'c2', name: 'Staging' }))
    await user.keyboard('stag{Enter}')
    await waitFor(() => expect(useConnections.getState().session?.id).toBe('s2'))
    expect(ConnectionService.Connect).toHaveBeenCalledWith('c2')
    expect(ConnectionService.Disconnect).not.toHaveBeenCalled()
    expect(palette()).toBeNull()
    // The workspace loads the new session; Production keeps running.
    await waitFor(() => expect(api.current).toHaveBeenCalledWith('s2'))
    expect(screen.getByRole('button', { name: 'Staging: switch connection' })).toBeInTheDocument()
    expect(screen.getByTestId('open-count')).toHaveTextContent('2')
    expect(screen.queryByRole('tab', { name: /users/ })).toBeNull()

    // Back to Production: no reconnect, the tab is still open.
    key('k')
    const prod = within(palette()!).getByText('Production').closest('[cmdk-item]') as HTMLElement
    expect(prod).toHaveTextContent('Connected')
    await user.click(within(prod).getByText('Production'))
    expect(useConnections.getState().session?.id).toBe('s1')
    expect(ConnectionService.Connect).toHaveBeenCalledTimes(1)
    expect(await screen.findByRole('tab', { name: /users/ })).toBeInTheDocument()
    expect((api.current.mock.calls as unknown as string[][]).filter(([sid]) => sid === 's1')).toHaveLength(1)
  })

  it('opens from the sidebar header and the command palette; the current connection just closes it', async () => {
    const user = userEvent.setup()
    render(<Workspace api={api} platform="linux" editor={fakeEditorApi()} />)
    const header = await screen.findByRole('button', { name: 'Production: switch connection' })
    expect(header).toHaveAttribute('title', 'Switch connection (Ctrl+K)')
    await user.click(header)
    await user.click(within(palette()!).getByText('Production'))
    expect(palette()).toBeNull()
    expect(ConnectionService.Connect).not.toHaveBeenCalled()

    key('p', { shiftKey: true })
    await user.keyboard('switch conn{Enter}')
    expect(palette()).toHaveTextContent('Saved connections')
    await user.keyboard('{Escape}')
    expect(palette()).toBeNull()
  })

  it('keeps the session and shows why when the switch fails', async () => {
    const user = userEvent.setup()
    render(<Workspace api={api} platform="linux" editor={fakeEditorApi()} />)
    await screen.findByRole('button', { name: /^users/ })
    key('k')
    ConnectionService.Connect.mockRejectedValueOnce(new Error('connection refused'))
    await user.click(within(palette()!).getByText('Staging'))
    expect(await within(palette()!).findByRole('alert')).toHaveTextContent('connection refused')
    expect(useConnections.getState().session?.id).toBe('s1')
    expect(ConnectionService.Disconnect).not.toHaveBeenCalled()

    // Closed palette: the error shows as a dismissible banner.
    await user.keyboard('{Escape}')
    expect(screen.getByTestId('connection-error')).toHaveTextContent('connection refused')
    await user.click(screen.getByRole('button', { name: 'Dismiss' }))
    expect(screen.queryByTestId('connection-error')).toBeNull()
    // Reopening starts clean.
    act(() => useConnections.setState({ error: 'old' }))
    key('k')
    expect(within(palette()!).queryByRole('alert')).toBeNull()
  })

  it('keeps staged edits across switches and asks before disconnecting them', async () => {
    const user = userEvent.setup()
    render(<Workspace api={api} platform="linux" editor={fakeEditorApi()} />)
    await user.click(await screen.findByRole('button', { name: /^users/ }))
    await screen.findByTestId('fake-grid')
    key('n') // stage an insert
    await screen.findByTestId('pending')

    key('k')
    ConnectionService.Connect.mockResolvedValueOnce(makeSession({ id: 's2', connectionId: 'c2', name: 'Staging' }))
    await user.click(within(palette()!).getByText('Staging'))
    await waitFor(() => expect(useConnections.getState().session?.id).toBe('s2'))
    expect(screen.queryByRole('alertdialog')).toBeNull()

    // Disconnecting Production from the switcher would drop its edit: ask.
    key('k')
    await user.click(within(palette()!).getByRole('button', { name: 'Disconnect Production' }))
    expect(useConnections.getState().session?.id).toBe('s2') // the click did not switch
    expect(palette()).toBeNull()
    const ask = await screen.findByRole('alertdialog')
    expect(ask).toHaveTextContent('1 staged change')
    await user.click(within(ask).getByRole('button', { name: 'Cancel' }))
    expect(ConnectionService.Disconnect).not.toHaveBeenCalled()

    // Switching back shows the staged edit, still pending.
    key('k')
    await user.click(within(palette()!).getByText('Production'))
    expect(await screen.findByTestId('pending')).toHaveTextContent('1 pending change')

    // Back on Staging, disconnect Production for real.
    key('k')
    await user.click(within(palette()!).getByText('Staging'))
    key('k')
    await user.click(within(palette()!).getByRole('button', { name: 'Disconnect Production' }))
    await user.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Discard and disconnect' }))
    expect(ConnectionService.Disconnect).toHaveBeenCalledWith('s1')
    expect(useConnections.getState().sessions.map((x) => x.id)).toEqual(['s2'])
    expect(screen.queryByTestId('open-count')).toBeNull()
  })

  it('disconnects a clean background session at once, and the toolbar Disconnect falls back to it', async () => {
    const user = userEvent.setup()
    render(<Workspace api={api} platform="linux" editor={fakeEditorApi()} />)
    await screen.findByRole('button', { name: /^users/ })
    key('k')
    ConnectionService.Connect.mockResolvedValueOnce(makeSession({ id: 's2', connectionId: 'c2', name: 'Staging' }))
    await user.click(within(palette()!).getByText('Staging'))
    await waitFor(() => expect(useConnections.getState().session?.id).toBe('s2'))

    // The toolbar Disconnect closes Staging and shows Production again.
    await user.click(screen.getByRole('button', { name: 'Disconnect' }))
    expect(ConnectionService.Disconnect).toHaveBeenCalledWith('s2')
    expect(await screen.findByRole('button', { name: 'Production: switch connection' })).toBeInTheDocument()

    ConnectionService.Connect.mockResolvedValueOnce(makeSession({ id: 's3', connectionId: 'c2', name: 'Staging' }))
    key('k')
    await user.click(within(palette()!).getByText('Staging'))
    await waitFor(() => expect(useConnections.getState().session?.id).toBe('s3'))
    key('k')
    await user.click(within(palette()!).getByRole('button', { name: 'Disconnect Production' }))
    expect(ConnectionService.Disconnect).toHaveBeenCalledWith('s1')
    expect(screen.queryByRole('alertdialog')).toBeNull()
  })

  it('asks about an unknown SSH host key inside the workspace', async () => {
    const user = userEvent.setup()
    render(<Workspace api={api} platform="linux" editor={fakeEditorApi()} />)
    await screen.findByRole('button', { name: /^users/ })
    key('k')
    const info = { kind: 'hostKeyUnknown', host: 'bastion', address: '10.0.0.1:22', keyType: 'ssh-ed25519', fingerprintSHA256: 'SHA256:abc', message: 'unknown host key' }
    ConnectionService.Connect.mockRejectedValueOnce(Object.assign(new Error('unknown host key'), { cause: info }))
    await user.click(within(palette()!).getByText('Staging'))
    const dialog = await screen.findByRole('dialog', { name: /host key/i }).catch(() => screen.findByText(/bastion/))
    expect(dialog).toBeTruthy()
    // The palette stays closed while the host key is asked about.
    expect(palette()).toBeNull()
    act(() => useConnections.getState().dismissHostKey())
    fireEvent.keyDown(window, { key: 'k', ctrlKey: true })
    expect(palette()).not.toBeNull()
  })
})

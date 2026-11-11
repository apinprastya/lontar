import { cleanup, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('../../../bindings/dbedit/internal/services', () => import('../../test/mockBindings'))

import { initialConnectionsState, useConnections } from '../../stores/connections'
import { ConnectionService, makeConnection, makeSession, resetBindings } from '../../test/mockBindings'
import Welcome from './Welcome'

const list = [makeConnection({ id: 'a', name: 'Alpha' }), makeConnection({ id: 'b', name: 'Beta', kind: 'mysql', port: 3306 })]

async function renderWelcome(platform = 'linux') {
  const user = userEvent.setup()
  render(<Welcome platform={platform} />)
  await screen.findByText('Alpha')
  return user
}

describe('Welcome', () => {
  beforeEach(() => {
    resetBindings()
    useConnections.setState(initialConnectionsState)
    ConnectionService.List.mockResolvedValue(list)
  })
  afterEach(cleanup)

  it('loads connections and shows an empty state', async () => {
    ConnectionService.List.mockResolvedValue([])
    render(<Welcome />)
    expect(await screen.findByText('No saved connections yet.')).toBeInTheDocument()
    for (const name of ['Edit', 'Duplicate', 'Delete']) {
      expect(screen.getByRole('button', { name })).toBeDisabled()
    }
  })

  it('shows Loading… before the first load completes', () => {
    ConnectionService.List.mockReturnValue(new Promise(() => {}))
    render(<Welcome />)
    expect(screen.getByText('Loading…')).toBeInTheDocument()
  })

  it('creates a connection from the New dialog', async () => {
    const user = await renderWelcome()
    ConnectionService.Save.mockImplementation(async (c) => ({ ...c, id: 'n' }))
    await user.click(screen.getByRole('button', { name: 'New' }))
    const dialog = screen.getByRole('dialog')
    await user.type(within(dialog).getByLabelText('Name'), 'Gamma')
    await user.click(within(dialog).getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(screen.getByRole('option', { name: /Gamma/ })).toHaveAttribute('aria-selected', 'true')
  })

  it('edits the selected connection and tests it', async () => {
    const user = await renderWelcome()
    ConnectionService.Test.mockResolvedValue('MySQL 8.0.36')
    await user.click(screen.getByText('Beta'))
    await user.click(screen.getByRole('button', { name: 'Edit' }))
    const dialog = screen.getByRole('dialog')
    expect(within(dialog).getByLabelText('Name')).toHaveValue('Beta')
    await user.click(within(dialog).getByRole('button', { name: 'Test' }))
    expect(await within(dialog).findByText('Connection OK · MySQL 8.0.36')).toBeInTheDocument()
    await user.click(within(dialog).getByRole('button', { name: 'Cancel' }))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('duplicates the selected connection', async () => {
    const user = await renderWelcome()
    ConnectionService.Duplicate.mockResolvedValue(makeConnection({ id: 'a2', name: 'Alpha copy' }))
    await user.click(screen.getByText('Alpha'))
    await user.click(screen.getByRole('button', { name: 'Duplicate' }))
    expect(await screen.findByText('Alpha copy')).toBeInTheDocument()
    expect(ConnectionService.Duplicate).toHaveBeenCalledWith('a')
    expect(screen.getByRole('option', { name: /Alpha copy/ })).toHaveAttribute('aria-selected', 'true')
  })

  it('deletes after confirmation only', async () => {
    const user = await renderWelcome()
    ConnectionService.Delete.mockResolvedValue()
    await user.click(screen.getByText('Alpha'))
    await user.click(screen.getByRole('button', { name: 'Delete' }))
    let confirm = screen.getByRole('alertdialog')
    expect(confirm).toHaveTextContent('“Alpha” and its saved password will be removed')
    await user.click(within(confirm).getByRole('button', { name: 'Cancel' }))
    expect(ConnectionService.Delete).not.toHaveBeenCalled()

    await user.click(screen.getByRole('button', { name: 'Delete' }))
    confirm = screen.getByRole('alertdialog')
    await user.keyboard('{Escape}')
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Delete' }))
    await user.click(within(screen.getByRole('alertdialog')).getByRole('button', { name: 'Delete' }))
    await waitFor(() => expect(screen.queryByText('Alpha')).not.toBeInTheDocument())
    expect(ConnectionService.Delete).toHaveBeenCalledWith('a')
    expect(screen.getByRole('button', { name: 'Edit' })).toBeDisabled()
  })

  it('deletes from the keyboard', async () => {
    const user = await renderWelcome()
    await user.click(screen.getByText('Beta'))
    screen.getByRole('listbox').focus()
    await user.keyboard('{Delete}')
    expect(screen.getByRole('alertdialog')).toHaveTextContent('Beta')
  })

  it('connects on double-click and shows errors', async () => {
    const user = await renderWelcome()
    ConnectionService.Connect.mockRejectedValueOnce(new Error('connection refused'))
    await user.dblClick(screen.getByText('Alpha'))
    expect(await screen.findByRole('alert')).toHaveTextContent('connection refused')
    await user.click(screen.getByRole('button', { name: 'Dismiss error' }))
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()

    ConnectionService.Connect.mockResolvedValueOnce(makeSession({ connectionId: 'a' }))
    await user.dblClick(screen.getByText('Alpha'))
    await waitFor(() => expect(useConnections.getState().session?.connectionId).toBe('a'))
  })

  it('saves and connects from the form', async () => {
    const user = await renderWelcome()
    ConnectionService.Save.mockImplementation(async (c) => c)
    ConnectionService.Connect.mockResolvedValue(makeSession({ connectionId: 'a' }))
    await user.click(screen.getByText('Alpha'))
    await user.click(screen.getByRole('button', { name: 'Edit' }))
    await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Connect' }))
    await waitFor(() => expect(ConnectionService.Connect).toHaveBeenCalledWith('a'))
  })

  it('supports Ctrl shortcuts on Linux', async () => {
    const user = await renderWelcome('linux')
    ConnectionService.Duplicate.mockResolvedValue(makeConnection({ id: 'a2', name: 'Alpha copy' }))

    await user.keyboard('{Control>}f{/Control}')
    expect(screen.getByLabelText('Search connections')).toHaveFocus()

    // Edit/Duplicate need a selection.
    await user.keyboard('{Control>}e{/Control}')
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    await user.click(screen.getByText('Alpha'))
    await user.keyboard('{Control>}d{/Control}')
    expect(await screen.findByText('Alpha copy')).toBeInTheDocument()
    await user.keyboard('{Control>}e{/Control}')
    expect(screen.getByRole('heading', { name: 'Edit connection' })).toBeInTheDocument()
    await user.keyboard('{Escape}')

    await user.keyboard('{Control>}n{/Control}')
    expect(screen.getByRole('heading', { name: 'New connection' })).toBeInTheDocument()
    // Shortcuts are inactive while a dialog is open.
    await user.keyboard('{Control>}n{/Control}')
    expect(screen.getAllByRole('dialog')).toHaveLength(1)

    // Meta is not the modifier on Linux; unrelated keys do nothing.
    await user.keyboard('{Escape}{Meta>}n{/Meta}{Control>}q{/Control}')
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'New' })).toHaveAttribute('title', 'Ctrl+N')
  })

  it('uses Cmd on macOS', async () => {
    const user = await renderWelcome('darwin')
    expect(screen.getByRole('button', { name: 'New' })).toHaveAttribute('title', '⌘N')
    await user.keyboard('{Control>}n{/Control}')
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    await user.keyboard('{Meta>}n{/Meta}')
    expect(screen.getByRole('dialog')).toBeInTheDocument()
  })
})

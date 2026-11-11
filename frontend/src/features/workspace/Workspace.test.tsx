import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('../../../bindings/dbedit/internal/services', () => import('../../test/mockBindings'))

import { initialConnectionsState, useConnections } from '../../stores/connections'
import { ConnectionService, makeConnection, makeSession, resetBindings } from '../../test/mockBindings'
import Workspace from './Workspace'

describe('Workspace placeholder', () => {
  beforeEach(() => {
    resetBindings()
    useConnections.setState(initialConnectionsState)
  })
  afterEach(cleanup)

  it('renders nothing without a session', () => {
    const { container } = render(<Workspace />)
    expect(container).toBeEmptyDOMElement()
  })

  it('shows the session and disconnects', async () => {
    useConnections.setState({ session: makeSession(), connections: [makeConnection({ id: 'c1', tag: 'prod' })] })
    ConnectionService.Disconnect.mockResolvedValue()
    const user = userEvent.setup()
    render(<Workspace />)
    expect(screen.getByRole('heading', { name: 'Production' })).toBeInTheDocument()
    expect(screen.getByTestId('server-version')).toHaveTextContent('PostgreSQL 16.4')
    expect(screen.getByText('PostgreSQL')).toBeInTheDocument()
    expect(screen.getByLabelText('Workspace')).toHaveStyle({ borderTopColor: '#f85149' })
    await user.click(screen.getByRole('button', { name: 'Disconnect' }))
    expect(ConnectionService.Disconnect).toHaveBeenCalledWith('s1')
    expect(useConnections.getState().session).toBeNull()
  })

  it('handles a session whose connection is not in the list', () => {
    useConnections.setState({ session: makeSession({ kind: 'sqlite', connectionId: 'gone' }) })
    render(<Workspace />)
    expect(screen.getByText('SQLite')).toBeInTheDocument()
    expect(screen.getByLabelText('Workspace').getAttribute('style')).toBeNull()
  })
})

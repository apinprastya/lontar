import { act, cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

vi.mock('../../../bindings/lontar/internal/services', () => import('../../test/mockBindings'))

import { makeSession } from '../../test/mockBindings'
import { initialConnectionsState, useConnections } from '../../stores/connections'
import { IdleBadge } from './Workspace'

describe('IdleBadge', () => {
  afterEach(cleanup)

  it('shows while the session is suspended', () => {
    useConnections.setState({ ...initialConnectionsState, sessions: [makeSession({ id: 's1' })] })
    render(<IdleBadge sessionId="s1" />)
    expect(screen.queryByTestId('idle-badge')).toBeNull()
    act(() => useConnections.getState().setSuspended('s1', true))
    expect(screen.getByTestId('idle-badge')).toHaveTextContent('Idle')
    expect(screen.getByTestId('idle-badge')).toHaveAttribute('title', expect.stringMatching(/reconnects/))
    act(() => useConnections.getState().setSuspended('s1', false))
    expect(screen.queryByTestId('idle-badge')).toBeNull()
  })
})

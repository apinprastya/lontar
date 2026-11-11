import { cleanup, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('../../bindings/dbedit/internal/services', () => import('../test/mockBindings'))

import { initialConnectionsState, useConnections } from '../stores/connections'
import { AppService, ConnectionService, makeConnection, makeSession, resetBindings } from '../test/mockBindings'
import App, { platformLabel } from './App'

const { Version, Platform } = AppService

describe('App shell', () => {
  beforeEach(() => {
    resetBindings()
    Version.mockReset()
    Platform.mockReset()
    useConnections.setState(initialConnectionsState)
  })
  afterEach(cleanup)

  it('renders the shell and shows version and platform from Go', async () => {
    Version.mockResolvedValue('1.2.3')
    Platform.mockResolvedValue('linux')

    const { container } = render(<App />)

    expect(screen.getByLabelText('Sidebar')).toHaveTextContent('dbedit')
    expect(screen.getByLabelText('Sidebar')).toHaveTextContent('Not connected')
    expect(screen.getByRole('heading', { name: 'Connections' })).toBeInTheDocument()
    expect(screen.getByRole('status')).toHaveTextContent('Loading')

    expect(await screen.findByTestId('app-version')).toHaveTextContent('v1.2.3')
    expect(screen.getByTestId('app-platform')).toHaveTextContent('Linux')
    expect(container.querySelector('.app-shell')).toHaveAttribute('data-platform', 'linux')
    expect(Version).toHaveBeenCalledTimes(1)
    expect(Platform).toHaveBeenCalledTimes(1)
    expect(screen.getByRole('status')).toHaveTextContent('Shortcuts use Ctrl')
  })

  it('switches to the workspace when a session is open', async () => {
    Version.mockResolvedValue('1.0.0')
    Platform.mockResolvedValue('darwin')
    useConnections.setState({ session: makeSession(), connections: [makeConnection({ id: 'c1', color: '#39c5cf' })] })
    render(<App />)
    expect(screen.getByLabelText('Workspace')).toBeInTheDocument()
    expect(screen.getByLabelText('Sidebar')).toHaveTextContent('Production')
    expect(screen.getByTestId('statusbar-session')).toHaveTextContent('PostgreSQL 16.4')
    await waitFor(() => expect(document.querySelector('.app-shell')).toHaveAttribute('data-platform', 'darwin'))
    expect(document.querySelector('.app-shell')).toHaveClass('app-shell-workspace')
    expect(ConnectionService.List).not.toHaveBeenCalled()
  })

  it('shows the sidebar session without a matching connection', () => {
    Version.mockResolvedValue('1.0.0')
    Platform.mockResolvedValue('linux')
    useConnections.setState({ session: makeSession({ connectionId: 'gone', kind: 'mariadb' }) })
    render(<App />)
    expect(screen.getByLabelText('Sidebar')).toHaveTextContent('MariaDB')
  })

  it('shows an error when the backend call fails', async () => {
    Version.mockRejectedValue(new Error('no bridge'))
    Platform.mockResolvedValue('linux')

    render(<App />)

    expect(await screen.findByText(/Backend unavailable: no bridge/)).toBeInTheDocument()
    expect(screen.queryByTestId('app-version')).not.toBeInTheDocument()
  })

  it('stringifies non-Error rejections', async () => {
    Version.mockResolvedValue('1.0.0')
    Platform.mockRejectedValue('boom')

    render(<App />)

    expect(await screen.findByText('Backend unavailable: boom')).toBeInTheDocument()
  })

  it('ignores results that arrive after unmount', async () => {
    let resolve!: (v: string) => void
    Version.mockReturnValue(new Promise<string>((r) => (resolve = r)))
    Platform.mockResolvedValue('darwin')
    let reject!: (e: unknown) => void
    const { unmount } = render(<App />)
    unmount()
    resolve('1.0.0')
    await Promise.resolve()

    // Same for a failing call.
    Version.mockReturnValue(new Promise<string>((_, r) => (reject = r)))
    const second = render(<App />)
    second.unmount()
    reject(new Error('late'))
    await new Promise((r) => setTimeout(r, 0))
    // No act() warnings / state updates on unmounted components means success.
    expect(Version).toHaveBeenCalledTimes(2)
  })
})

describe('platformLabel', () => {
  it.each([
    ['darwin', 'macOS'],
    ['windows', 'Windows'],
    ['linux', 'Linux'],
    ['freebsd', 'freebsd'],
  ])('%s -> %s', (goos, label) => {
    expect(platformLabel(goos)).toBe(label)
  })
})

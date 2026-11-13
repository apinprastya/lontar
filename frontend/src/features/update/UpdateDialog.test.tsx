import { act, cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const progressHandlers: ((d: unknown) => void)[] = []
const off = vi.fn()
vi.mock('../../lib/events', () => ({
  onEvent: (name: string, cb: (d: unknown) => void) => {
    if (name === 'update:progress') progressHandlers.push(cb)
    return off
  },
}))
vi.mock('../../../bindings/lontar/internal/services', () => import('../../test/mockBindings'))

import { UpdateService, resetBindings } from '../../test/mockBindings'
import { useUpdate } from '../../stores/update'
import type { UpdateInfo } from './api'
import UpdateDialog, { megabytes } from './UpdateDialog'

const INFO: UpdateInfo = {
  current: '26.11.1',
  latest: '26.11.2',
  available: true,
  name: 'Lontar 26.11.2',
  notes: '## Changes\n- faster grid',
  url: 'https://github.com/apinprastya/lontar/releases/tag/v26.11.2',
  publishedAt: '2026-11-10T12:00:00Z',
  canInstall: true,
  reason: '',
}

function deferred<T>() {
  let resolve!: (v: T) => void
  let reject!: (e: unknown) => void
  const promise = new Promise<T>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

function renderOpen() {
  return render(<>{useUpdate.getState().open && <UpdateDialog />}</>)
}

describe('UpdateDialog', () => {
  beforeEach(() => {
    resetBindings()
    progressHandlers.length = 0
    off.mockReset()
    useUpdate.setState({ open: false, phase: 'idle', info: null, progress: null, error: null })
  })
  afterEach(cleanup)

  it('checks, downloads with progress, installs and restarts', async () => {
    const user = userEvent.setup()
    UpdateService.Check.mockResolvedValue(INFO)
    const inst = deferred<void>()
    UpdateService.Install.mockReturnValue(inst.promise)
    UpdateService.Restart.mockResolvedValue(undefined)
    UpdateService.OpenReleasePage.mockResolvedValue(undefined)

    await act(() => useUpdate.getState().check())
    renderOpen()
    expect(screen.getByRole('dialog', { name: 'Software update' })).toBeInTheDocument()
    expect(screen.getByText(/is available/)).toHaveTextContent('Lontar 26.11.2 is available. You have 26.11.1.')
    expect(screen.getByLabelText('Release notes')).toHaveTextContent('faster grid')

    await user.click(screen.getByRole('button', { name: 'Release page' }))
    expect(UpdateService.OpenReleasePage).toHaveBeenCalled()

    await user.click(screen.getByRole('button', { name: 'Download and install' }))
    expect(screen.getByText('Starting download…')).toBeInTheDocument()
    act(() => progressHandlers[0]({ done: 1024 * 1024, total: 4 * 1024 * 1024 }))
    expect(screen.getByRole('progressbar', { name: 'Download progress' })).toHaveAttribute('value', '25')
    expect(screen.getByText('1.0 MB of 4.0 MB')).toBeInTheDocument()

    await act(async () => inst.resolve())
    expect(off).toHaveBeenCalled()
    expect(screen.getByText(/is installed/)).toBeInTheDocument()
    // Closing keeps the installed state; reopening shows it again without a new check.
    await user.click(screen.getByRole('button', { name: 'Later' }))
    expect(useUpdate.getState()).toMatchObject({ open: false, phase: 'installed' })
    await act(() => useUpdate.getState().check())
    expect(UpdateService.Check).toHaveBeenCalledTimes(1)
    cleanup()
    renderOpen()
    await user.click(screen.getByRole('button', { name: 'Restart now' }))
    expect(UpdateService.Restart).toHaveBeenCalled()
  })

  it('reports up to date and closes with Escape', async () => {
    const user = userEvent.setup()
    UpdateService.Check.mockResolvedValue({ ...INFO, available: false, canInstall: false, current: '26.11.2' })
    await act(() => useUpdate.getState().check())
    renderOpen()
    expect(screen.getByText(/You are up to date/)).toHaveTextContent('Lontar 26.11.2 is the latest version.')
    expect(screen.queryByRole('button', { name: 'Download and install' })).toBeNull()
    await user.keyboard('{Escape}')
    expect(useUpdate.getState()).toMatchObject({ open: false, phase: 'idle', info: null })
  })

  it('mentions a newer current build than the latest release', async () => {
    UpdateService.Check.mockResolvedValue({ ...INFO, available: false, canInstall: false, current: '26.11.3-rc.1', publishedAt: '' })
    await act(() => useUpdate.getState().check())
    renderOpen()
    expect(screen.getByText(/latest release: 26.11.2/)).toBeInTheDocument()
  })

  it('explains why it cannot install and offers the release page', async () => {
    UpdateService.Check.mockResolvedValue({ ...INFO, canInstall: false, reason: 'Update it with your package manager.', notes: '', publishedAt: 'bogus' })
    await act(() => useUpdate.getState().check())
    renderOpen()
    expect(screen.getByText('Update it with your package manager.')).toBeInTheDocument()
    expect(screen.queryByLabelText('Release notes')).toBeNull()
    expect(screen.queryByText(/Released/)).toBeNull()
    expect(screen.queryByRole('button', { name: 'Download and install' })).toBeNull()
    expect(screen.getByRole('button', { name: 'Release page' })).toBeInTheDocument()
    // install() does nothing when the backend said it cannot.
    await act(() => useUpdate.getState().install())
    expect(UpdateService.Install).not.toHaveBeenCalled()
  })

  it('shows check errors with a retry', async () => {
    const user = userEvent.setup()
    UpdateService.Check.mockRejectedValueOnce(new Error('offline'))
    await act(() => useUpdate.getState().check())
    renderOpen()
    expect(screen.getByRole('alert')).toHaveTextContent('offline')
    UpdateService.Check.mockResolvedValueOnce(INFO)
    await user.click(screen.getByRole('button', { name: 'Try again' }))
    expect(screen.queryByRole('alert')).toBeNull()
    expect(screen.getByRole('button', { name: 'Download and install' })).toBeInTheDocument()
  })

  it('shows install errors, but not after a cancel', async () => {
    const user = userEvent.setup()
    UpdateService.Check.mockResolvedValue(INFO)
    UpdateService.Install.mockRejectedValueOnce(new Error('the downloaded file does not match the signed checksum'))
    await act(() => useUpdate.getState().check())
    renderOpen()
    await user.click(screen.getByRole('button', { name: 'Download and install' }))
    expect(screen.getByRole('alert')).toHaveTextContent('signed checksum')

    const inst = deferred<void>()
    UpdateService.Install.mockReturnValueOnce(inst.promise)
    UpdateService.Cancel.mockImplementation(async () => {
      inst.reject(new Error('context canceled'))
      return true
    })
    await user.click(screen.getByRole('button', { name: 'Download and install' }))
    act(() => progressHandlers[1]({ done: 10, total: 0 }))
    expect(screen.getByText('0.0 MB')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Cancel download' }))
    expect(UpdateService.Cancel).toHaveBeenCalled()
    expect(screen.queryByRole('alert')).toBeNull()
    expect(screen.getByRole('button', { name: 'Download and install' })).toBeInTheDocument()
    // Cancel outside an install is a no-op.
    await act(() => useUpdate.getState().cancel())
    expect(UpdateService.Cancel).toHaveBeenCalledTimes(1)
  })

  it('shows restart and release page errors', async () => {
    UpdateService.Restart.mockRejectedValue(new Error('restart is not available'))
    UpdateService.OpenReleasePage.mockRejectedValue(new Error('no browser'))
    useUpdate.setState({ open: true, phase: 'installed', info: INFO })
    renderOpen()
    await act(() => useUpdate.getState().restart())
    expect(screen.getByRole('alert')).toHaveTextContent('restart is not available')
    await act(() => useUpdate.getState().openReleasePage())
    expect(screen.getByRole('alert')).toHaveTextContent('no browser')
  })

  it('rejects an empty check result', async () => {
    UpdateService.Check.mockResolvedValue(null)
    await act(() => useUpdate.getState().check())
    expect(useUpdate.getState().error).toBe('no update information')
  })

  it('formats megabytes', () => {
    expect(megabytes(0)).toBe('0.0 MB')
    expect(megabytes(1536 * 1024)).toBe('1.5 MB')
  })
})

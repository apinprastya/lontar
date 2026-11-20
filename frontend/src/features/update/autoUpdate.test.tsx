import { act, cleanup, render, renderHook, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('../../lib/events', () => ({ onEvent: () => () => {} }))
vi.mock('../../../bindings/lontar/internal/services', () => import('../../test/mockBindings'))

import { UpdateService, resetBindings } from '../../test/mockBindings'
import { resetDismissed, useUpdate } from '../../stores/update'
import type { UpdateInfo } from './api'
import { AUTO_UPDATE_DELAY_MS, AUTO_UPDATE_INTERVAL_MS, useAutoUpdate } from './autoUpdate'
import UpdateDialog from './UpdateDialog'
import UpdateNotice from './UpdateNotice'

const INFO: UpdateInfo = {
  current: '26.11.1',
  latest: '26.11.2',
  available: true,
  name: 'Lontar 26.11.2',
  notes: 'notes',
  url: 'https://github.com/apinprastya/lontar/releases/tag/v26.11.2',
  publishedAt: '2026-11-10T12:00:00Z',
  canInstall: true,
  reason: '',
}

const MANUAL: UpdateInfo = { ...INFO, canInstall: false, reason: 'Installed by a package manager.' }

const auto = () => act(() => useUpdate.getState().autoUpdate())

function Shell() {
  const open = useUpdate((s) => s.open)
  return (
    <>
      {open && <UpdateDialog />}
      <UpdateNotice />
    </>
  )
}

describe('automatic updates', () => {
  beforeEach(() => {
    resetBindings()
    resetDismissed()
    useUpdate.setState({ open: false, phase: 'idle', info: null, progress: null, error: null, notice: null })
  })
  afterEach(() => {
    cleanup()
    vi.useRealTimers()
  })

  it('installs in the background and offers a restart', async () => {
    const user = userEvent.setup()
    UpdateService.AutoCheck.mockResolvedValue(INFO)
    UpdateService.Install.mockResolvedValue(undefined)
    UpdateService.Restart.mockResolvedValue(undefined)
    render(<Shell />)

    await auto()
    expect(UpdateService.Install).toHaveBeenCalledTimes(1)
    expect(useUpdate.getState().phase).toBe('installed')
    const notice = screen.getByRole('status', { name: 'Software update' })
    expect(notice).toHaveTextContent('Lontar 26.11.2 is installed. Restart to use it.')

    // Opening the notice shows the dialog with the restart warning.
    await user.click(screen.getByRole('button', { name: 'Restart…' }))
    expect(screen.queryByRole('status', { name: 'Software update' })).not.toBeInTheDocument()
    expect(screen.getByText(/staged changes/)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Restart now' }))
    expect(UpdateService.Restart).toHaveBeenCalled()

    // An installed update is not checked again.
    await auto()
    expect(UpdateService.AutoCheck).toHaveBeenCalledTimes(1)
  })

  it('dismisses the installed notice but keeps the update', async () => {
    const user = userEvent.setup()
    UpdateService.AutoCheck.mockResolvedValue(INFO)
    UpdateService.Install.mockResolvedValue(undefined)
    render(<Shell />)
    await auto()
    await user.click(screen.getByRole('button', { name: 'Dismiss update notice' }))
    expect(screen.queryByRole('status', { name: 'Software update' })).not.toBeInTheDocument()
    expect(useUpdate.getState().phase).toBe('installed')
  })

  it('stays silent when up to date, offline or a development build', async () => {
    render(<Shell />)
    UpdateService.AutoCheck.mockResolvedValue({ ...INFO, available: false, latest: '26.11.1' })
    await auto()
    UpdateService.AutoCheck.mockResolvedValue(null)
    await auto()
    UpdateService.AutoCheck.mockRejectedValue(new Error('offline'))
    await auto()
    expect(UpdateService.Install).not.toHaveBeenCalled()
    expect(useUpdate.getState()).toMatchObject({ phase: 'idle', notice: null, open: false, error: null })
    expect(screen.queryByRole('status')).not.toBeInTheDocument()
  })

  it('only announces a release this installation cannot install, once per version', async () => {
    const user = userEvent.setup()
    UpdateService.AutoCheck.mockResolvedValue(MANUAL)
    render(<Shell />)

    await auto()
    expect(UpdateService.Install).not.toHaveBeenCalled()
    expect(screen.getByRole('status', { name: 'Software update' })).toHaveTextContent('Lontar 26.11.2 is available.')
    await user.click(screen.getByRole('button', { name: 'Details' }))
    expect(screen.getByRole('dialog', { name: 'Software update' })).toHaveTextContent('Installed by a package manager.')
    await user.click(screen.getByRole('button', { name: 'Close' }))
    expect(useUpdate.getState().phase).toBe('idle')

    await auto()
    await user.click(screen.getByRole('button', { name: 'Dismiss update notice' }))
    expect(useUpdate.getState()).toMatchObject({ phase: 'idle', notice: null, info: null })
    await auto()
    expect(screen.queryByRole('status')).not.toBeInTheDocument()

    UpdateService.AutoCheck.mockResolvedValue({ ...MANUAL, latest: '26.12.1' })
    await auto()
    expect(screen.getByRole('status', { name: 'Software update' })).toHaveTextContent('Lontar 26.12.1 is available.')
  })

  it('announces a failed background install with its error', async () => {
    const user = userEvent.setup()
    UpdateService.AutoCheck.mockResolvedValue(INFO)
    UpdateService.Install.mockRejectedValue(new Error('permission denied'))
    render(<Shell />)
    await auto()
    expect(screen.getByRole('status', { name: 'Software update' })).toHaveTextContent('is available.')
    await user.click(screen.getByRole('button', { name: 'Details' }))
    expect(screen.getByRole('alert')).toHaveTextContent('permission denied')
    expect(screen.getByRole('button', { name: 'Download and install' })).toBeInTheDocument()

    // Once dismissed, the same failure is not announced again.
    await user.click(screen.getByRole('button', { name: 'Close' }))
    await auto()
    await user.click(screen.getByRole('button', { name: 'Dismiss update notice' }))
    await auto()
    expect(screen.queryByRole('status')).not.toBeInTheDocument()
    expect(useUpdate.getState().phase).toBe('idle')
  })

  it('leaves the outcome to the dialog when it was opened during the download', async () => {
    UpdateService.AutoCheck.mockResolvedValue(INFO)
    let finish!: () => void
    UpdateService.Install.mockReturnValue(new Promise<void>((res) => (finish = res)))
    const run = useUpdate.getState().autoUpdate()
    await vi.waitFor(() => expect(useUpdate.getState().phase).toBe('installing'))
    // A manual check while installing just opens the dialog.
    await useUpdate.getState().check()
    expect(UpdateService.Check).not.toHaveBeenCalled()
    finish()
    await run
    expect(useUpdate.getState()).toMatchObject({ open: true, phase: 'installed', notice: null })
  })

  it('resets when the background download is cancelled from the dialog', async () => {
    UpdateService.AutoCheck.mockResolvedValue(INFO)
    let fail!: (e: Error) => void
    UpdateService.Install.mockReturnValue(new Promise<void>((_, rej) => (fail = rej)))
    UpdateService.Cancel.mockResolvedValue(true)
    const run = useUpdate.getState().autoUpdate()
    await vi.waitFor(() => expect(useUpdate.getState().phase).toBe('installing'))
    useUpdate.setState({ open: true })
    await useUpdate.getState().cancel()
    useUpdate.setState({ open: false })
    fail(new Error('context canceled'))
    await run
    expect(useUpdate.getState()).toMatchObject({ phase: 'idle', notice: null, info: null, error: null })
  })

  it('skips the check while the dialog is open or the user checks meanwhile', async () => {
    useUpdate.setState({ open: true })
    await auto()
    expect(UpdateService.AutoCheck).not.toHaveBeenCalled()

    useUpdate.setState({ open: false })
    UpdateService.AutoCheck.mockImplementation(async () => {
      useUpdate.setState({ open: true, phase: 'checking' })
      return INFO
    })
    await auto()
    expect(UpdateService.Install).not.toHaveBeenCalled()
    expect(useUpdate.getState().notice).toBeNull()
  })

  it('schedules checks while enabled', () => {
    vi.useFakeTimers()
    const original = useUpdate.getState().autoUpdate
    const autoUpdate = vi.fn(async () => {})
    useUpdate.setState({ autoUpdate })
    const { rerender, unmount } = renderHook(({ on }) => useAutoUpdate(on), { initialProps: { on: false } })
    vi.advanceTimersByTime(AUTO_UPDATE_INTERVAL_MS)
    expect(autoUpdate).not.toHaveBeenCalled()

    rerender({ on: true })
    vi.advanceTimersByTime(AUTO_UPDATE_DELAY_MS)
    expect(autoUpdate).toHaveBeenCalledTimes(1)
    vi.advanceTimersByTime(AUTO_UPDATE_INTERVAL_MS)
    expect(autoUpdate).toHaveBeenCalledTimes(2)

    unmount()
    vi.advanceTimersByTime(AUTO_UPDATE_INTERVAL_MS)
    expect(autoUpdate).toHaveBeenCalledTimes(2)
    useUpdate.setState({ autoUpdate: original })
  })
})

import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { Version, Platform } = vi.hoisted(() => ({
  Version: vi.fn<() => Promise<string>>(),
  Platform: vi.fn<() => Promise<string>>(),
}))

vi.mock('../../bindings/dbedit/internal/services', () => ({
  AppService: { Version, Platform },
}))

import App, { platformLabel } from './App'

describe('App shell', () => {
  beforeEach(() => {
    Version.mockReset()
    Platform.mockReset()
  })
  afterEach(cleanup)

  it('renders the shell and shows version and platform from Go', async () => {
    Version.mockResolvedValue('1.2.3')
    Platform.mockResolvedValue('linux')

    const { container } = render(<App />)

    expect(screen.getByRole('heading', { name: 'dbedit' })).toBeInTheDocument()
    expect(screen.getByLabelText('Sidebar')).toHaveTextContent('No connections')
    expect(screen.getByRole('status')).toHaveTextContent('Loading')

    expect(await screen.findByTestId('app-version')).toHaveTextContent('v1.2.3')
    expect(screen.getByTestId('app-platform')).toHaveTextContent('Linux')
    expect(container.querySelector('.app-shell')).toHaveAttribute('data-platform', 'linux')
    expect(Version).toHaveBeenCalledTimes(1)
    expect(Platform).toHaveBeenCalledTimes(1)
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

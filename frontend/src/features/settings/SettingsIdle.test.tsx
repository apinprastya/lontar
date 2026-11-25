import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('../../../bindings/lontar/internal/services', () => import('../../test/mockBindings'))

import { SettingsService, resetBindings } from '../../test/mockBindings'
import { useSettings } from '../../stores/settings'
import SettingsDialog, { idleLabel } from './SettingsDialog'
import { DEFAULT_SETTINGS } from './types'

describe('Settings › Connections', () => {
  beforeEach(() => {
    resetBindings()
    useSettings.setState({ settings: DEFAULT_SETTINGS, error: null, open: true })
  })
  afterEach(cleanup)

  it('saves the idle disconnect timeout', async () => {
    const user = userEvent.setup()
    render(<SettingsDialog />)
    await user.click(screen.getByRole('tab', { name: 'Connections' }))
    const select = screen.getByRole('combobox', { name: 'Disconnect when idle for' })
    expect(select).toHaveValue('0')
    expect(select).toHaveAccessibleDescription(/reconnects on the next action/)
    await user.selectOptions(select, '30')
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(SettingsService.Save).toHaveBeenCalledWith({ ...DEFAULT_SETTINGS, idleDisconnectMinutes: 30 })
  })

  it('keeps a value set outside the presets', async () => {
    useSettings.setState({ settings: { ...DEFAULT_SETTINGS, idleDisconnectMinutes: 45 } })
    const user = userEvent.setup()
    render(<SettingsDialog />)
    await user.click(screen.getByRole('tab', { name: 'Connections' }))
    expect(screen.getByRole('combobox', { name: 'Disconnect when idle for' })).toHaveValue('45')
    expect(screen.getByRole('option', { name: '45 minutes' })).toBeInTheDocument()
  })

  it('labels the choices', () => {
    expect(idleLabel(0)).toBe('Never')
    expect(idleLabel(15)).toBe('15 minutes')
    expect(idleLabel(60)).toBe('1 hour')
    expect(idleLabel(240)).toBe('4 hours')
  })
})

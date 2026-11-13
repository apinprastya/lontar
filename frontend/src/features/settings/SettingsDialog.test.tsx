import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('../../../bindings/lontar/internal/services', () => import('../../test/mockBindings'))

import { SettingsService, UpdateService, resetBindings } from '../../test/mockBindings'
import { useUpdate } from '../../stores/update'
import { useSettings } from '../../stores/settings'
import SettingsDialog from './SettingsDialog'
import { DEFAULT_SETTINGS } from './types'

describe('SettingsDialog', () => {
  beforeEach(() => {
    resetBindings()
    useSettings.setState({ settings: DEFAULT_SETTINGS, error: null, open: true })
  })
  afterEach(cleanup)

  it('edits and saves every setting (clamped)', async () => {
    const user = userEvent.setup()
    render(<SettingsDialog platform="linux" />)
    expect(screen.getByRole('radio', { name: 'System' })).toBeChecked()
    await user.click(screen.getByRole('radio', { name: 'Light' }))
    const font = screen.getByLabelText('Editor font size')
    await user.clear(font)
    await user.type(font, '99')
    const page = screen.getByLabelText('Rows per table page')
    await user.clear(page)
    await user.type(page, '500')
    const max = screen.getByLabelText('Editor max rows')
    await user.clear(max)
    await user.type(max, '2000')
    await user.click(screen.getByRole('checkbox', { name: /Ask before closing/ }))
    await user.selectOptions(screen.getByLabelText('Delimiter'), ';')
    await user.selectOptions(screen.getByLabelText('NULL as'), 'custom')
    await user.type(screen.getByLabelText('NULL text'), 'NULL')
    await user.click(screen.getByRole('checkbox', { name: 'Header row' }))
    await user.click(screen.getByRole('checkbox', { name: 'CRLF line endings' }))
    await user.click(screen.getByRole('checkbox', { name: 'UTF-8 BOM (Excel)' }))
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(SettingsService.Save).toHaveBeenCalledWith({
      ...DEFAULT_SETTINGS,
      theme: 'light',
      editorFontSize: 32,
      gridPageSize: 500,
      editorMaxRows: 2000,
      confirmCloseDirtyQuery: true,
      csvExport: { delimiter: ';', header: false, quote: '"', null: 'custom', nullText: 'NULL', crlf: true, bom: true },
    })
    expect(useSettings.getState().open).toBe(false)
    expect(useSettings.getState().settings.theme).toBe('light')
  })

  it('restores defaults, shows errors and cancels', async () => {
    const user = userEvent.setup()
    useSettings.setState({ settings: { ...DEFAULT_SETTINGS, theme: 'dark', gridPageSize: 50 } })
    SettingsService.Save.mockRejectedValue(new Error('disk full'))
    render(<SettingsDialog />)
    await user.click(screen.getByRole('button', { name: 'Restore defaults' }))
    expect(screen.getByRole('radio', { name: 'System' })).toBeChecked()
    expect(screen.getByLabelText('Rows per table page')).toHaveValue(300)
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('disk full')
    expect(useSettings.getState().open).toBe(true)
    await user.keyboard('{Escape}')
    expect(useSettings.getState().open).toBe(false)
    useSettings.setState({ open: true })
    await user.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(useSettings.getState().open).toBe(false)
  })

  it('closes and checks for updates', async () => {
    UpdateService.Check.mockReturnValue(new Promise(() => {}))
    const user = userEvent.setup()
    render(<SettingsDialog platform="linux" />)
    await user.click(screen.getByRole('button', { name: 'Check for updates…' }))
    expect(useSettings.getState().open).toBe(false)
    expect(useUpdate.getState()).toMatchObject({ open: true, phase: 'checking' })
    expect(UpdateService.Check).toHaveBeenCalled()
    useUpdate.setState({ open: false, phase: 'idle' })
  })
})

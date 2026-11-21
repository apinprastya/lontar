import { cleanup, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('../../../bindings/lontar/internal/services', () => import('../../test/mockBindings'))

import { SettingsService, UpdateService, resetBindings } from '../../test/mockBindings'
import { useUpdate } from '../../stores/update'
import { useSettings } from '../../stores/settings'
import SettingsDialog, { SECTIONS } from './SettingsDialog'
import { DEFAULT_SETTINGS } from './types'

const tab = (name: string) => screen.getByRole('tab', { name })

describe('SettingsDialog', () => {
  beforeEach(() => {
    resetBindings()
    // The dialog reopens on the last section; start each test on the first.
    render(<SettingsDialog />)
    tab('Appearance').click()
    cleanup()
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
    await user.click(tab('Data & export'))
    const page = screen.getByLabelText('Rows per page')
    await user.clear(page)
    await user.type(page, '500')
    await user.selectOptions(screen.getByLabelText('Delimiter'), ';')
    await user.selectOptions(screen.getByLabelText('NULL as'), 'custom')
    await user.type(screen.getByLabelText('NULL text'), 'NULL')
    await user.click(screen.getByRole('checkbox', { name: 'Header row' }))
    await user.click(screen.getByRole('checkbox', { name: 'CRLF line endings' }))
    await user.click(screen.getByRole('checkbox', { name: 'UTF-8 BOM (Excel)' }))
    await user.click(tab('SQL editor'))
    const max = screen.getByLabelText('Max rows')
    await user.clear(max)
    await user.type(max, '2000')
    await user.click(screen.getByRole('checkbox', { name: /Ask before closing/ }))
    await user.selectOptions(screen.getByRole('combobox', { name: 'Keyword case' }), 'lower')
    await user.selectOptions(screen.getByRole('combobox', { name: 'Indent' }), '4')
    await user.click(tab('Updates'))
    expect(screen.getByRole('checkbox', { name: 'Install updates automatically' })).toBeChecked()
    await user.click(screen.getByRole('checkbox', { name: 'Install updates automatically' }))
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(SettingsService.Save).toHaveBeenCalledWith({
      ...DEFAULT_SETTINGS,
      theme: 'light',
      editorFontSize: 32,
      gridPageSize: 500,
      editorMaxRows: 2000,
      confirmCloseDirtyQuery: true,
      autoUpdate: false,
      csvExport: { delimiter: ';', header: false, quote: '"', null: 'custom', nullText: 'NULL', crlf: true, bom: true },
      sqlFormat: { keywordCase: 'lower', indent: 4 },
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
    await user.click(tab('Data & export'))
    expect(screen.getByLabelText('Rows per page')).toHaveValue(300)
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('disk full')
    expect(useSettings.getState().open).toBe(true)
    await user.keyboard('{Escape}')
    expect(useSettings.getState().open).toBe(false)
    useSettings.setState({ open: true })
    await user.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(useSettings.getState().open).toBe(false)
  })

  it('shows the SQL format defaults for settings without them (older backend)', async () => {
    const user = userEvent.setup()
    const { sqlFormat: _omit, ...old } = DEFAULT_SETTINGS
    useSettings.setState({ settings: old as typeof DEFAULT_SETTINGS })
    render(<SettingsDialog />)
    await user.click(tab('SQL editor'))
    expect(screen.getByRole('combobox', { name: 'Keyword case' })).toHaveValue('upper')
    await user.selectOptions(screen.getByRole('combobox', { name: 'Keyword case' }), 'preserve')
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(SettingsService.Save).toHaveBeenCalledWith(expect.objectContaining({ sqlFormat: { keywordCase: 'preserve', indent: 2 } }))
  })

  it('closes and checks for updates', async () => {
    UpdateService.Check.mockReturnValue(new Promise(() => {}))
    const user = userEvent.setup()
    render(<SettingsDialog platform="linux" version="2026.11.1" />)
    await user.click(tab('Updates'))
    expect(screen.getByTestId('settings-version')).toHaveTextContent('v2026.11.1')
    await user.click(screen.getByRole('button', { name: 'Check for updates…' }))
    expect(useSettings.getState().open).toBe(false)
    expect(useUpdate.getState()).toMatchObject({ open: true, phase: 'checking' })
    expect(UpdateService.Check).toHaveBeenCalled()
    useUpdate.setState({ open: false, phase: 'idle' })
  })

  it('shows one section at a time and moves between them with the keyboard', async () => {
    const user = userEvent.setup()
    render(<SettingsDialog />)
    const list = screen.getByRole('tablist', { name: 'Settings sections' })
    expect(within(list).getAllByRole('tab').map((t) => t.textContent)).toEqual(SECTIONS.map((s) => s.label))
    expect(tab('Appearance')).toHaveAttribute('aria-selected', 'true')
    expect(tab('Appearance')).toHaveFocus()
    expect(screen.getByRole('tabpanel')).toHaveAccessibleName('Appearance')
    expect(screen.queryByLabelText('Rows per page')).toBeNull()

    await user.keyboard('{ArrowDown}')
    expect(tab('SQL editor')).toHaveFocus()
    expect(screen.getByLabelText('Max rows')).toBeInTheDocument()
    await user.keyboard('{ArrowUp}{ArrowUp}')
    expect(tab('Updates')).toHaveAttribute('aria-selected', 'true')
    expect(screen.queryByTestId('settings-version')).toBeNull() // no version given
    await user.keyboard('{Home}')
    expect(tab('Appearance')).toHaveFocus()
    await user.keyboard('{End}')
    expect(tab('Updates')).toHaveFocus()
    await user.keyboard('{ArrowDown}')
    expect(tab('Appearance')).toHaveFocus()
    await user.keyboard('x') // other keys are ignored
    expect(tab('Appearance')).toHaveAttribute('aria-selected', 'true')
  })

  it('reopens on the last section and keeps unsaved edits across sections', async () => {
    const user = userEvent.setup()
    const { unmount } = render(<SettingsDialog />)
    await user.click(tab('Data & export'))
    await user.selectOptions(screen.getByLabelText('Delimiter'), '|')
    await user.click(tab('Appearance'))
    await user.click(tab('Data & export'))
    expect(screen.getByLabelText('Delimiter')).toHaveValue('|')
    unmount()
    render(<SettingsDialog />)
    expect(tab('Data & export')).toHaveAttribute('aria-selected', 'true')
    expect(tab('Data & export')).toHaveFocus()
  })

  it('shows the MCP setup in the AI assistants section', async () => {
    const user = userEvent.setup()
    render(<SettingsDialog />)
    await user.click(tab('AI assistants'))
    expect(screen.getByText(/Let Claude list, explore and query/)).toBeInTheDocument()
  })
})

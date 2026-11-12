import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { commands, SHORTCUTS, shortcutText } from '../workspace/keymap'
import CommandPalette, { tableValue, type CommandPaletteProps, type PaletteTable } from './CommandPalette'

const tables: PaletteTable[] = [
  { schema: 'public', name: 'users', type: 'table', current: true },
  { schema: 'public', name: 'orders', type: 'table', current: true },
  { schema: 'public', name: 'active_users', type: 'view', current: true },
  { schema: 'audit', name: 'user_log', type: 'table', current: false },
]

function setup(over: Partial<CommandPaletteProps> = {}) {
  const props: CommandPaletteProps = {
    mode: 'tables',
    platform: 'linux',
    tables,
    onOpenTable: vi.fn(),
    onCommand: vi.fn(),
    onModeChange: vi.fn(),
    onClose: vi.fn(),
    ...over,
  }
  const user = userEvent.setup()
  const r = render(<CommandPalette {...props} />)
  return { props, user, rerender: (p: Partial<CommandPaletteProps>) => r.rerender(<CommandPalette {...props} {...p} />) }
}

const items = () => screen.getAllByRole('option').map((o) => o.textContent)

describe('CommandPalette', () => {
  afterEach(cleanup)

  it('fuzzy-filters tables and opens data or structure', async () => {
    const { props, user } = setup()
    expect(items()).toEqual(['Tusers', 'Torders', 'Vactive_usersview', 'Taudit.user_log'])
    // Fuzzy: "acus" finds active_users.
    await user.keyboard('acus')
    expect(items()[0]).toBe('Vactive_usersview')
    await user.clear(screen.getByRole('combobox'))
    await user.keyboard('ord')
    expect(items()[0]).toBe('Torders')
    expect(items()).not.toContain('Tusers')
    await user.keyboard('{Enter}')
    expect(props.onOpenTable).toHaveBeenCalledWith(tables[1], 'data')
    await user.keyboard('{Control>}{Enter}{/Control}')
    expect(props.onOpenTable).toHaveBeenLastCalledWith(tables[1], 'structure')
    await user.clear(screen.getByRole('combobox'))
    await user.keyboard('zzzz')
    expect(screen.getByText('No matching table or view.')).toBeInTheDocument()
    await user.keyboard('{Escape}')
    expect(props.onClose).toHaveBeenCalled()
  })

  it('switches to commands with ">" and back with Backspace', async () => {
    const { props, user, rerender } = setup({ loading: true })
    expect(screen.getByText('Loading other schemas…')).toBeInTheDocument()
    await user.keyboard('>')
    expect(props.onModeChange).toHaveBeenCalledWith('commands')
    rerender({ mode: 'commands' })
    await user.keyboard('{Backspace}')
    expect(props.onModeChange).toHaveBeenLastCalledWith('tables')
  })

  it('lists every command of the keymap registry with its shortcut', async () => {
    const disabled = (id: string) => id === 'commit'
    const { props, user } = setup({ mode: 'commands', disabled })
    const want = commands().map((c) => c.description + shortcutText(c, 'linux'))
    expect(items()).toEqual(want)
    expect(screen.getByText('Ctrl+Shift+Enter')).toBeInTheDocument()
    expect(screen.getByRole('option', { name: /Commit staged changes/ })).toHaveAttribute('aria-disabled', 'true')
    await user.keyboard('settings{Enter}')
    expect(props.onCommand).toHaveBeenCalledWith('settings')
    // Mod+Enter does nothing special in command mode.
    await user.keyboard('{Control>}{Enter}{/Control}')
    expect(props.onOpenTable).not.toHaveBeenCalled()
    // Clicking the backdrop closes.
    await user.click(document.querySelector('.palette-backdrop')!)
    expect(props.onClose).toHaveBeenCalled()
  })

  it('shows macOS shortcuts and helper values', () => {
    setup({ mode: 'commands', platform: 'darwin', list: SHORTCUTS.filter((s) => s.id === 'runAll') })
    expect(screen.getByText('⇧⌘Enter')).toBeInTheDocument()
    expect(tableValue({ schema: 's', name: 'n' })).toBe('table:s\u0000n')
  })
})

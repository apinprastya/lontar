import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { DRIVERS, makeConnection } from '../../test/mockBindings'
import ConnectionForm, { ConnectionFormProps, validateDraft } from './ConnectionForm'
import type { Connection } from './types'

function setup(over: Partial<ConnectionFormProps> = {}) {
  const props: ConnectionFormProps = {
    drivers: DRIVERS,
    platform: 'linux',
    onSave: vi.fn(async (c: Connection) => ({ ...c, id: c.id || 'new-id' })),
    onTest: vi.fn(async () => 'PostgreSQL 16.4'),
    onConnect: vi.fn(),
    onClose: vi.fn(),
    ...over,
  }
  const user = userEvent.setup()
  render(<ConnectionForm {...props} />)
  return { props, user }
}

const field = (name: string) => screen.getByLabelText(name) as HTMLInputElement

describe('ConnectionForm', () => {
  afterEach(cleanup)

  it('creates a server connection with defaults', async () => {
    const { props, user } = setup()
    expect(screen.getByRole('heading', { name: 'New connection' })).toBeInTheDocument()
    expect(field('Name')).toHaveFocus()
    expect(field('Port').value).toBe('5432')
    expect(field('Host').value).toBe('localhost')

    await user.type(field('Name'), 'Local PG')
    await user.type(field('User'), 'me')
    await user.type(field('Database'), 'app')
    await user.click(screen.getByRole('button', { name: 'Save' }))

    expect(props.onSave).toHaveBeenCalledWith(
      expect.objectContaining({ id: '', name: 'Local PG', kind: 'postgres', host: 'localhost', port: 5432, user: 'me', database: 'app', tag: 'none', color: '' }),
      null,
    )
    expect(props.onClose).toHaveBeenCalled()
  })

  it('switches fields and default port with the driver', async () => {
    const { user } = setup()
    await user.selectOptions(field('Type'), 'mysql')
    expect(field('Port').value).toBe('3306')
    await user.selectOptions(field('Type'), 'sqlite')
    expect(screen.queryByLabelText('Host')).not.toBeInTheDocument()
    expect(screen.queryByLabelText('Password')).not.toBeInTheDocument()
    expect(field('Database file')).toBeInTheDocument()
    expect(field('Read-only')).toBeInTheDocument()
    await user.selectOptions(field('Type'), 'postgres')
    expect(field('Port').value).toBe('5432')
  })

  it('saves SQLite connections with read-only and create options', async () => {
    const { props, user } = setup()
    await user.selectOptions(field('Type'), 'sqlite')
    await user.type(field('Name'), 'File')
    await user.type(field('Database file'), '/tmp/x.db')
    await user.click(field('Create file if missing'))
    await user.click(field('Read-only'))
    expect(field('Create file if missing')).toBeDisabled()
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(props.onSave).toHaveBeenCalledWith(
      expect.objectContaining({ kind: 'sqlite', filePath: '/tmp/x.db', readOnly: true, createIfMissing: true, host: '', port: 0 }),
      null,
    )
  })

  it('validates required fields and port range', async () => {
    const { props, user } = setup()
    await user.clear(field('Host'))
    await user.clear(field('Port'))
    await user.type(field('Port'), '70000')
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(screen.getByText('Name is required')).toBeInTheDocument()
    expect(screen.getByText('Host is required')).toBeInTheDocument()
    expect(screen.getByText('Port must be between 1 and 65535')).toBeInTheDocument()
    expect(field('Name')).toHaveAttribute('aria-invalid', 'true')
    expect(props.onSave).not.toHaveBeenCalled()

    // Editing a field clears its error.
    await user.type(field('Name'), 'x')
    expect(screen.queryByText('Name is required')).not.toBeInTheDocument()

    // Test is validated too.
    await user.click(screen.getByRole('button', { name: 'Test' }))
    expect(props.onTest).not.toHaveBeenCalled()
  })

  it('validateDraft covers SQLite and port edge cases', () => {
    const base = { name: 'n', kind: 'postgres' as const, host: 'h', port: '1', user: '', database: '', filePath: '', readOnly: false, createIfMissing: false, tag: 'none' as const, color: '' }
    expect(validateDraft(base)).toEqual({})
    expect(validateDraft({ ...base, port: '65535' })).toEqual({})
    expect(validateDraft({ ...base, port: '0' }).port).toBeDefined()
    expect(validateDraft({ ...base, port: '12a' }).port).toBeDefined()
    expect(validateDraft({ ...base, kind: 'sqlite' }).filePath).toBe('Database file is required')
    expect(validateDraft({ ...base, kind: 'sqlite', filePath: 'a.db', port: '' })).toEqual({})
  })

  it('sends a nil password when unchanged and shows the saved placeholder', async () => {
    const initial = makeConnection({ hasPassword: true, color: '#a371f7', options: { sslmode: 'require' } })
    const { props, user } = setup({ initial })
    expect(screen.getByRole('heading', { name: 'Edit connection' })).toBeInTheDocument()
    expect(field('Password')).toHaveAttribute('placeholder', '•••••••• saved')
    expect(field('Password').value).toBe('')
    expect(screen.getByRole('radio', { name: '#a371f7' })).toHaveAttribute('aria-checked', 'true')

    await user.clear(field('Name'))
    await user.type(field('Name'), 'Renamed')
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(props.onSave).toHaveBeenCalledWith(
      expect.objectContaining({ id: 'c1', name: 'Renamed', options: { sslmode: 'require' } }),
      null,
    )
  })

  it('sends a changed password', async () => {
    const { props, user } = setup({ initial: makeConnection() })
    await user.type(field('Password'), `p@ss:w/rd'"%`)
    expect(field('Password')).toHaveAttribute('placeholder', '')
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(props.onSave).toHaveBeenCalledWith(expect.anything(), `p@ss:w/rd'"%`)
  })

  it('clears a saved password with an empty string', async () => {
    const { props, user } = setup({ initial: makeConnection() })
    await user.click(screen.getByRole('button', { name: 'Clear saved' }))
    expect(screen.getByText('Saved password will be removed')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Clear saved' })).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(props.onSave).toHaveBeenCalledWith(expect.anything(), '')
  })

  it('has no saved placeholder without a stored password', () => {
    setup({ initial: makeConnection({ hasPassword: false }) })
    expect(field('Password')).toHaveAttribute('placeholder', '')
    expect(screen.queryByRole('button', { name: 'Clear saved' })).not.toBeInTheDocument()
  })

  it('shows a successful test with the server version', async () => {
    let resolve!: (v: string) => void
    const onTest = vi.fn(() => new Promise<string>((r) => (resolve = r)))
    const { user } = setup({ initial: makeConnection(), onTest })
    await user.click(screen.getByRole('button', { name: 'Test' }))
    expect(screen.getByText('Testing…')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Test' })).toBeDisabled()
    expect(onTest).toHaveBeenCalledWith(expect.objectContaining({ id: 'c1' }), null)
    resolve('PostgreSQL 16.4')
    expect(await screen.findByText('Connection OK · PostgreSQL 16.4')).toBeInTheDocument()

    // Changing a field resets the result.
    await user.type(field('Database'), 'x')
    expect(screen.queryByText(/Connection OK/)).not.toBeInTheDocument()
  })

  it('shows a failed test', async () => {
    const onTest = vi.fn(async () => {
      throw new Error('password authentication failed for user "app"')
    })
    const { user } = setup({ onTest })
    await user.type(field('Name'), 'x')
    await user.type(field('Password'), 'typed')
    await user.click(screen.getByRole('button', { name: 'Test' }))
    expect(await screen.findByText('password authentication failed for user "app"')).toBeInTheDocument()
    expect(onTest).toHaveBeenCalledWith(expect.anything(), 'typed')
  })

  it('shows save errors and stays open', async () => {
    const onSave = vi.fn(async () => {
      throw new Error('keychain: no secret service')
    })
    const { props, user } = setup({ onSave })
    await user.type(field('Name'), 'x')
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('keychain: no secret service')
    expect(props.onClose).not.toHaveBeenCalled()
    await user.click(screen.getByRole('button', { name: 'Connect' }))
    expect(props.onConnect).not.toHaveBeenCalled()
  })

  it('saves then connects', async () => {
    const { props, user } = setup()
    await user.type(field('Name'), 'x')
    await user.click(screen.getByRole('button', { name: 'Connect' }))
    await waitFor(() => expect(props.onConnect).toHaveBeenCalledWith(expect.objectContaining({ id: 'new-id' })))
    expect(props.onClose).toHaveBeenCalled()
  })

  it('supports Escape, Mod+Enter, tag and colour selection', async () => {
    const { props, user } = setup()
    await user.type(field('Name'), 'x')
    await user.selectOptions(field('Tag'), 'staging')
    await user.click(screen.getByRole('radio', { name: '#39c5cf' }))
    await user.click(screen.getByRole('radio', { name: 'Tag colour' }))
    await user.keyboard('{Control>}{Enter}{/Control}')
    await waitFor(() => expect(props.onConnect).toHaveBeenCalledWith(expect.objectContaining({ tag: 'staging', color: '' })))

    cleanup()
    const second = setup()
    await second.user.keyboard('{Escape}')
    expect(second.props.onClose).toHaveBeenCalled()
  })

  it('falls back to built-in kinds without driver info', async () => {
    const { user } = setup({ drivers: [] })
    expect(screen.getAllByRole('option').map((o) => o.textContent)).toEqual(expect.arrayContaining(['PostgreSQL', 'MySQL', 'MariaDB', 'SQLite']))
    await user.selectOptions(field('Type'), 'mariadb')
    expect(field('Port').value).toBe('3306')
  })

  it('opens an existing SQLite connection', () => {
    setup({ initial: makeConnection({ kind: 'sqlite', host: undefined, port: undefined, filePath: '/data/a.db', readOnly: true }) })
    expect(field('Database file').value).toBe('/data/a.db')
    expect(field('Read-only')).toBeChecked()
  })
})

import { cleanup, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { DRIVERS, makeConnection } from '../../test/mockBindings'
import ConnectionForm, { type ConnectionFormProps } from './ConnectionForm'
import HostKeyDialog from './HostKeyDialog'
import { NO_SECRETS, sslModes, type Connection } from './types'

const unknownKey = {
  kind: 'hostKeyUnknown' as const,
  host: '[bastion]:2222',
  address: 'bastion:2222',
  keyType: 'ssh-ed25519',
  fingerprintSHA256: 'SHA256:abc',
  message: "the authenticity of SSH host [bastion]:2222 can't be established",
}

function setup(over: Partial<ConnectionFormProps> = {}) {
  const props: ConnectionFormProps = {
    drivers: DRIVERS,
    platform: 'linux',
    onSave: vi.fn(async (c: Connection) => ({ ...c, id: c.id || 'new-id' })),
    onTest: vi.fn(async () => 'PostgreSQL 16.4'),
    onConnect: vi.fn(),
    onClose: vi.fn(),
    onTrustHostKey: vi.fn(async () => {}),
    pickFile: vi.fn(async () => '/home/me/.ssh/id_ed25519'),
    ...over,
  }
  const user = userEvent.setup()
  render(<ConnectionForm {...props} />)
  return { props, user }
}

const field = (name: string) => screen.getByLabelText(name) as HTMLInputElement
const tab = (name: string) => screen.getByRole('tab', { name: new RegExp(`^${name}`) })

describe('ConnectionForm SSH and SSL', () => {
  afterEach(cleanup)

  it('configures an SSH tunnel with a key and passphrase', async () => {
    const { props, user } = setup()
    await user.type(field('Name'), 'Via bastion')
    await user.click(tab('SSH'))
    expect(field('SSH host')).toBeDisabled()
    await user.click(screen.getByRole('checkbox', { name: /SSH tunnel/ }))
    // Required fields are checked and the SSH tab is shown with the errors.
    await user.click(tab('General'))
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(props.onSave).not.toHaveBeenCalled()
    expect(tab('SSH')).toHaveAttribute('aria-selected', 'true')
    expect(screen.getByText('SSH host is required')).toBeInTheDocument()
    expect(screen.getByText('SSH user is required')).toBeInTheDocument()

    await user.type(field('SSH host'), 'bastion')
    await user.clear(field('SSH port'))
    await user.type(field('SSH port'), '2222')
    await user.type(field('SSH user'), 'jump')
    await user.selectOptions(field('Authentication'), 'key')
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(screen.getByText('Private key file is required')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Browse for private key' }))
    expect(props.pickFile).toHaveBeenCalledWith('Choose an SSH private key', expect.any(Array))
    expect(field('Private key').value).toBe('/home/me/.ssh/id_ed25519')
    await user.type(field('Passphrase'), 's3cret')
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(props.onSave).toHaveBeenCalledWith(
      expect.objectContaining({ ssh: { enabled: true, host: 'bastion', port: 2222, user: 'jump', authMethod: 'key', keyPath: '/home/me/.ssh/id_ed25519' } }),
      { password: null, sshPassword: null, sshPassphrase: 's3cret' },
    )
  })

  it('keeps and clears saved SSH secrets; agent auth', async () => {
    const initial = makeConnection({
      ssh: { enabled: true, host: 'b', port: 22, user: 'u', authMethod: 'password' },
      hasSshPassword: true,
    })
    const { props, user } = setup({ initial, platform: 'windows' })
    await user.click(tab('SSH'))
    expect(field('SSH password')).toHaveAttribute('placeholder', '•••••••• saved')
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(props.onSave).toHaveBeenLastCalledWith(expect.anything(), NO_SECRETS)
    await user.click(screen.getByRole('button', { name: 'Clear saved ssh password' }))
    expect(screen.getByText('Saved ssh password will be removed')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(props.onSave).toHaveBeenLastCalledWith(expect.anything(), { ...NO_SECRETS, sshPassword: '' })
    await user.selectOptions(field('Authentication'), 'agent')
    expect(screen.getByText(/OpenSSH agent service/)).toBeInTheDocument()
  })

  it('sets SSL modes per engine and certificate files', async () => {
    const pickFile = vi.fn(async () => '/certs/ca.pem')
    const { props, user } = setup({ pickFile })
    await user.type(field('Name'), 'tls')
    await user.click(tab('SSL'))
    const modes = () => Array.from((field('SSL mode') as unknown as HTMLSelectElement).options).map((o) => o.value)
    expect(modes()).toEqual(['', 'disable', 'require', 'verify-ca', 'verify-full'])
    expect(screen.getByText(/Uses the sslmode driver option/)).toBeInTheDocument()
    await user.selectOptions(field('SSL mode'), 'verify-full')
    expect(screen.getByText(/Overrides any sslmode/)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Browse for ca certificate' }))
    expect(field('CA certificate').value).toBe('/certs/ca.pem')
    await user.type(field('Client certificate'), '/certs/c.pem')
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(screen.getByText('Give both the client certificate and its key')).toBeInTheDocument()
    await user.type(field('Client key'), '/certs/k.pem')
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(props.onSave).toHaveBeenCalledWith(
      expect.objectContaining({ ssl: { mode: 'verify-full', caFile: '/certs/ca.pem', certFile: '/certs/c.pem', keyFile: '/certs/k.pem' } }),
      NO_SECRETS,
    )
    // MySQL adds "preferred"; switching to PostgreSQL drops it.
    await user.click(tab('General'))
    await user.selectOptions(field('Type'), 'mysql')
    await user.click(tab('SSL'))
    expect(modes()).toContain('preferred')
    await user.selectOptions(field('SSL mode'), 'preferred')
    await user.click(tab('General'))
    await user.selectOptions(field('Type'), 'postgres')
    await user.click(tab('SSL'))
    expect(field('SSL mode')).toHaveValue('')
    // SQLite has no SSH/SSL tabs.
    await user.click(tab('General'))
    await user.selectOptions(field('Type'), 'sqlite')
    expect(screen.queryByRole('tab', { name: 'SSH' })).not.toBeInTheDocument()
    expect(sslModes('sqlite')).toEqual([])
  })

  it('reports a file picker error', async () => {
    const { user } = setup({ pickFile: vi.fn(async () => Promise.reject(new Error('file dialogs are not available; type the path instead'))) })
    await user.click(tab('SSL'))
    await user.click(screen.getByRole('button', { name: 'Browse for ca certificate' }))
    expect(await screen.findByText(/type the path instead/)).toBeInTheDocument()
    // A cancelled picker leaves the field alone.
  })

  it('asks to trust an unknown host key on Test, then tests again', async () => {
    const onTest = vi.fn().mockRejectedValueOnce({ message: unknownKey.message, cause: unknownKey }).mockResolvedValueOnce('PostgreSQL 16.4')
    const { props, user } = setup({ onTest })
    await user.type(field('Name'), 'x')
    await user.click(screen.getByRole('button', { name: 'Test' }))
    const dialog = await screen.findByRole('alertdialog')
    expect(dialog).toHaveTextContent('Unknown SSH host')
    expect(screen.getByTestId('hostkey-fingerprint')).toHaveTextContent('SHA256:abc')
    await user.click(screen.getByRole('button', { name: 'Trust and connect' }))
    expect(props.onTrustHostKey).toHaveBeenCalledWith(unknownKey)
    await waitFor(() => expect(screen.getByText('Connection OK · PostgreSQL 16.4')).toBeInTheDocument())
    expect(onTest).toHaveBeenCalledTimes(2)
  })

  it('handles trust failures and cancelling', async () => {
    const onTest = vi.fn().mockRejectedValue({ message: 'x', cause: JSON.stringify(unknownKey) })
    const onTrustHostKey = vi.fn(async () => Promise.reject(new Error('no host key with this fingerprint')))
    const { user } = setup({ onTest, onTrustHostKey })
    await user.type(field('Name'), 'x')
    await user.click(screen.getByRole('button', { name: 'Test' }))
    const dlg = await screen.findByRole('alertdialog')
    await user.click(within(dlg).getByRole('button', { name: 'Cancel' }))
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Test' }))
    await user.click(await screen.findByRole('button', { name: 'Trust and connect' }))
    expect(await screen.findByText('no host key with this fingerprint')).toBeInTheDocument()
  })
})

describe('HostKeyDialog', () => {
  afterEach(cleanup)

  it('shows a changed key as a warning without Accept', async () => {
    const onCancel = vi.fn()
    const user = userEvent.setup()
    render(
      <HostKeyDialog
        info={{ ...unknownKey, kind: 'hostKeyChanged', known: ['/home/u/.ssh/known_hosts:3'] }}
        onAccept={vi.fn()}
        onCancel={onCancel}
      />,
    )
    expect(screen.getByRole('alertdialog')).toHaveTextContent('SSH host key has changed!')
    expect(screen.getByRole('alertdialog')).toHaveTextContent('/home/u/.ssh/known_hosts:3')
    expect(screen.queryByRole('button', { name: /Trust/ })).not.toBeInTheDocument()
    await user.keyboard('{Escape}')
    expect(onCancel).toHaveBeenCalled()
    await user.click(screen.getByRole('button', { name: 'Close' }))
    expect(onCancel).toHaveBeenCalledTimes(2)
  })
})

import { cleanup, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { DRIVERS, makeConnection } from '../../test/mockBindings'
import ConnectionForm, { type ConnectionFormProps } from './ConnectionForm'
import HostKeyDialog from './HostKeyDialog'
import { afterSave, emptyHop, toHopDrafts } from './JumpHosts'
import type { Connection } from './types'

function setup(over: Partial<ConnectionFormProps> = {}) {
  const props: ConnectionFormProps = {
    drivers: DRIVERS,
    platform: 'linux',
    onSave: vi.fn(async (c: Connection) => ({ ...c, id: c.id || 'new-id' })),
    onTest: vi.fn(async () => 'PostgreSQL 16.4'),
    onConnect: vi.fn(),
    onClose: vi.fn(),
    onTrustHostKey: vi.fn(async () => {}),
    pickFile: vi.fn(async () => '/home/me/.ssh/hop_key'),
    ...over,
  }
  const user = userEvent.setup()
  render(<ConnectionForm {...props} />)
  return { props, user }
}

const tab = (name: string) => screen.getByRole('tab', { name: new RegExp(`^${name}`) })
const box = (name: string) => screen.getByRole('textbox', { name }) as HTMLInputElement
const hop = (n: number) => screen.getByRole('group', { name: `Jump host ${n}` })
const hostOf = (n: number) => within(hop(n)).getByRole('textbox', { name: `Jump host ${n} host` }) as HTMLInputElement

describe('ConnectionForm jump hosts', () => {
  afterEach(cleanup)

  it('adds, removes, reorders and saves jump hosts with their secrets', async () => {
    const { props, user } = setup()
    await user.type(screen.getByLabelText('Name'), 'Two bastions')
    await user.click(tab('SSH'))
    // Disabled until the tunnel is on.
    expect(screen.getByRole('button', { name: 'Add jump host' })).toBeDisabled()
    await user.click(screen.getByRole('checkbox', { name: /SSH tunnel/ }))
    await user.type(box('SSH host'), 'gw')
    await user.type(box('SSH user'), 'final')

    const add = screen.getByRole('button', { name: 'Add jump host' })
    await user.click(add)
    await user.click(add)
    await user.click(add)
    expect(add).toBeDisabled()
    expect(screen.getByText('At most 3 jump hosts.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Move jump host 1 up' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Move jump host 3 down' })).toBeDisabled()

    // Empty hops are refused, on the SSH tab, per field.
    await user.click(tab('General'))
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(props.onSave).not.toHaveBeenCalled()
    expect(tab('SSH')).toHaveAttribute('aria-selected', 'true')
    expect(screen.getByText('Check the jump hosts')).toBeInTheDocument()
    expect(within(hop(2)).getByText('Host is required')).toBeInTheDocument()
    expect(within(hop(2)).getByText('User is required')).toBeInTheDocument()
    expect(hostOf(1)).toHaveAttribute('aria-invalid', 'true')

    await user.type(hostOf(1), 'b1')
    expect(screen.queryByText('Host is required')).not.toBeInTheDocument()
    await user.type(box('Jump host 1 user'), 'u1')
    await user.type(screen.getByLabelText('Jump host 1 password'), 'pw-1')
    await user.type(hostOf(2), 'b2')
    await user.clear(box('Jump host 2 port'))
    await user.type(box('Jump host 2 port'), '2222')
    await user.type(box('Jump host 2 user'), 'u2')
    await user.selectOptions(screen.getByRole('combobox', { name: 'Jump host 2 authentication' }), 'key')
    await user.click(screen.getByRole('button', { name: 'Browse for jump host 2 private key' }))
    expect(props.pickFile).toHaveBeenCalledWith('Choose an SSH private key for jump host 2', expect.any(Array))
    expect(box('Jump host 2 private key').value).toBe('/home/me/.ssh/hop_key')
    await user.type(screen.getByLabelText('Jump host 2 passphrase'), 'phrase-2')
    await user.type(hostOf(3), 'b3')

    // Remove the third, then move the second up: secrets move with it.
    await user.click(screen.getByRole('button', { name: 'Remove jump host 3' }))
    expect(screen.queryByRole('group', { name: 'Jump host 3' })).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Add jump host' })).toBeEnabled()
    await user.click(screen.getByRole('button', { name: 'Move jump host 2 up' }))
    expect(hostOf(1).value).toBe('b2')
    expect(hostOf(2).value).toBe('b1')
    expect((screen.getByLabelText('Jump host 1 passphrase') as HTMLInputElement).value).toBe('phrase-2')
    expect((screen.getByLabelText('Jump host 2 password') as HTMLInputElement).value).toBe('pw-1')
    // And back down and up again.
    await user.click(screen.getByRole('button', { name: 'Move jump host 1 down' }))
    expect(hostOf(1).value).toBe('b1')
    await user.click(screen.getByRole('button', { name: 'Move jump host 2 up' }))

    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(props.onSave).toHaveBeenCalledWith(
      expect.objectContaining({
        ssh: expect.objectContaining({
          host: 'gw',
          jumps: [
            { id: '', host: 'b2', port: 2222, user: 'u2', authMethod: 'key', keyPath: '/home/me/.ssh/hop_key' },
            { id: '', host: 'b1', port: 22, user: 'u1', authMethod: 'password', keyPath: '' },
          ],
        }),
      }),
      {
        password: null,
        sshPassword: null,
        sshPassphrase: null,
        jumps: [
          { password: null, passphrase: 'phrase-2' },
          { password: 'pw-1', passphrase: null },
        ],
      },
    )
  })

  it('keeps saved hop secrets by id across reorder and a second save', async () => {
    const initial = makeConnection({
      ssh: {
        enabled: true,
        host: 'gw',
        port: 22,
        user: 'u',
        authMethod: 'agent',
        jumps: [
          { id: 'h1', host: 'b1', port: 22, user: 'u1', authMethod: 'password', hasPassword: true },
          { id: 'h2', host: 'b2', port: 22, user: 'u2', authMethod: 'agent' },
        ],
      },
    })
    let saves = 0
    const onSave = vi.fn(async (c: Connection) => {
      saves++
      // The backend assigns an id to a new hop and reports flags.
      return { ...c, ssh: { ...c.ssh!, jumps: c.ssh!.jumps!.map((h) => ({ ...h, id: h.id || `new-${saves}`, hasPassword: h.authMethod === 'password' })) } }
    })
    const { user } = setup({ initial, onSave })
    await user.click(tab('SSH'))
    expect(screen.getByLabelText('Jump host 1 password')).toHaveAttribute('placeholder', '•••••••• saved')
    await user.click(screen.getByRole('button', { name: 'Move jump host 1 down' }))
    expect(screen.getByLabelText('Jump host 2 password')).toHaveAttribute('placeholder', '•••••••• saved')
    await user.click(screen.getByRole('button', { name: 'Clear saved jump host 2 password' }))
    expect(screen.getByText('Saved password will be removed')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Add jump host' }))
    await user.type(hostOf(3), 'b3')
    await user.type(box('Jump host 3 user'), 'u3')
    await user.type(screen.getByLabelText('Jump host 3 password'), 'pw-3')
    await user.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(onSave).toHaveBeenCalledTimes(1))
    const [c1, s1] = onSave.mock.calls[0] as unknown as [Connection, unknown]
    expect(c1.ssh!.jumps!.map((h) => h.id)).toEqual(['h2', 'h1', ''])
    expect(s1).toMatchObject({ jumps: [{ password: null }, { password: '' }, { password: 'pw-3' }] })

    // The form now knows the new hop's id: a second save keeps every
    // secret (null) and sends the ids.
    await user.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(onSave).toHaveBeenCalledTimes(2))
    const [c2, s2] = onSave.mock.calls[1] as unknown as [Connection, unknown]
    expect(c2.ssh!.jumps!.map((h) => h.id)).toEqual(['h2', 'h1', 'new-1'])
    expect(s2).toMatchObject({ jumps: [{ password: null, passphrase: null }, { password: null, passphrase: null }, { password: null, passphrase: null }] })
    expect(screen.getByLabelText('Jump host 3 password')).toHaveAttribute('placeholder', '•••••••• saved')

    // Removing every hop drops "jumps" from the saved settings.
    for (let i = 0; i < 3; i++) await user.click(screen.getByRole('button', { name: 'Remove jump host 1' }))
    await user.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(onSave).toHaveBeenCalledTimes(3))
    const [c3, s3] = onSave.mock.calls[2] as unknown as [Connection, Record<string, unknown>]
    expect(c3.ssh).not.toHaveProperty('jumps')
    expect(s3).not.toHaveProperty('jumps')
  })

  it('reports a key picker error for a hop and ignores a cancelled one', async () => {
    const pickFile = vi.fn().mockResolvedValueOnce('').mockRejectedValueOnce(new Error('file dialogs are not available'))
    const initial = makeConnection({ ssh: { enabled: true, host: 'gw', user: 'u', jumps: [{ id: 'h1', host: 'b', user: 'u', authMethod: 'key', keyPath: '~/k' }] } })
    const { user } = setup({ initial, pickFile })
    await user.click(tab('SSH'))
    await user.click(screen.getByRole('button', { name: 'Browse for jump host 1 private key' }))
    expect(box('Jump host 1 private key').value).toBe('~/k')
    await user.click(screen.getByRole('button', { name: 'Browse for jump host 1 private key' }))
    expect(await screen.findByText('file dialogs are not available')).toBeInTheDocument()
    // An empty key path is required.
    await user.clear(box('Jump host 1 private key'))
    await user.clear(box('Jump host 1 port'))
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(within(hop(1)).getByText('Private key file is required')).toBeInTheDocument()
    expect(within(hop(1)).getByText('Port must be between 1 and 65535')).toBeInTheDocument()
  })

  it('draft helpers', () => {
    expect(toHopDrafts(null)).toEqual([])
    const [d] = toHopDrafts([{}])
    expect(d).toMatchObject({ id: '', host: '', port: '22', auth: 'password', hasPassword: false })
    expect(emptyHop().key).not.toBe(emptyHop().key)
    // Without a saved counterpart, the draft keeps its id and has no flags.
    expect(afterSave([{ ...d, id: 'x', hasPassword: true }], null)[0]).toMatchObject({ id: 'x', hasPassword: false })
  })
})

describe('HostKeyDialog hop label', () => {
  afterEach(cleanup)

  const info = {
    kind: 'hostKeyUnknown' as const,
    host: '[b2]:2222',
    address: 'b2:2222',
    keyType: 'ssh-ed25519',
    fingerprintSHA256: 'SHA256:hop',
    message: "jump host 2 (b2): the authenticity of SSH host [b2]:2222 can't be established",
    hop: 2,
    hopLabel: 'jump host 2 (b2)',
  }

  it('names the hop that presented the key', () => {
    render(<HostKeyDialog info={info} onAccept={vi.fn()} onCancel={vi.fn()} />)
    expect(screen.getByTestId('hostkey-hop')).toHaveTextContent('On jump host 2 (b2) of the SSH chain.')
    expect(screen.getByRole('alertdialog')).toHaveTextContent('Unknown SSH host')
  })

  it('names the hop of a changed key; no label without jump hosts', () => {
    const { unmount } = render(<HostKeyDialog info={{ ...info, kind: 'hostKeyChanged', known: ['kh:1'] }} onAccept={vi.fn()} onCancel={vi.fn()} />)
    expect(screen.getByTestId('hostkey-hop')).toHaveTextContent('jump host 2 (b2)')
    unmount()
    render(<HostKeyDialog info={{ ...info, hop: 0, hopLabel: undefined }} onAccept={vi.fn()} onCancel={vi.fn()} />)
    expect(screen.queryByTestId('hostkey-hop')).not.toBeInTheDocument()
  })

  it('shows the hop from a Test in the form', async () => {
    const onTest = vi.fn().mockRejectedValueOnce({ message: info.message, cause: info })
    const { user } = setup({ onTest })
    await user.type(screen.getByLabelText('Name'), 'x')
    await user.click(screen.getByRole('button', { name: 'Test' }))
    const dialog = await screen.findByRole('alertdialog')
    expect(within(dialog).getByTestId('hostkey-hop')).toHaveTextContent('jump host 2 (b2)')
  })
})

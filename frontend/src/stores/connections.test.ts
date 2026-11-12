import { beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('../../bindings/dbedit/internal/services', () => import('../test/mockBindings'))

import { ConnectionService, DRIVERS, makeConnection, makeSession, resetBindings } from '../test/mockBindings'
import { initialConnectionsState, useConnections } from './connections'
import { NO_SECRETS } from '../features/connections/types'

const st = () => useConnections.getState()

describe('connections store', () => {
  beforeEach(() => {
    resetBindings()
    useConnections.setState(initialConnectionsState)
  })

  it('loads drivers and connections sorted by name', async () => {
    ConnectionService.List.mockResolvedValue([makeConnection({ id: 'b', name: 'beta' }), makeConnection({ id: 'a', name: 'Alpha' })])
    const p = st().load()
    expect(st().loading).toBe(true)
    await p
    expect(st().loading).toBe(false)
    expect(st().drivers).toEqual(DRIVERS)
    expect(st().connections.map((c) => c.name)).toEqual(['Alpha', 'beta'])
  })

  it('treats null lists from Go as empty', async () => {
    ConnectionService.List.mockResolvedValue(null)
    ConnectionService.Drivers.mockResolvedValue(null)
    await st().load()
    expect(st().connections).toEqual([])
    expect(st().drivers).toEqual([])
  })

  it('reports load errors', async () => {
    ConnectionService.List.mockRejectedValue(new Error('corrupt file'))
    await st().load()
    expect(st().error).toBe('corrupt file')
    expect(st().loading).toBe(false)
    st().clearError()
    expect(st().error).toBeNull()
  })

  it('saves new and updated connections', async () => {
    const c = makeConnection({ id: '', name: 'zed' })
    ConnectionService.Save.mockResolvedValueOnce({ ...c, id: 'z' })
    const saved = await st().save(c, { ...NO_SECRETS, password: 'pw' })
    expect(saved.id).toBe('z')
    expect(ConnectionService.Save).toHaveBeenCalledWith(c, { ...NO_SECRETS, password: 'pw' })

    ConnectionService.Save.mockResolvedValueOnce({ ...c, id: 'z', name: 'Aaa' })
    await st().save({ ...c, id: 'z', name: 'Aaa' }, NO_SECRETS)
    expect(ConnectionService.Save).toHaveBeenLastCalledWith(expect.objectContaining({ id: 'z' }), NO_SECRETS)
    expect(st().connections.map((x) => x.name)).toEqual(['Aaa'])
  })

  it('propagates save errors to the caller', async () => {
    ConnectionService.Save.mockRejectedValue(new Error('name is required'))
    await expect(st().save(makeConnection(), NO_SECRETS)).rejects.toThrow('name is required')
  })

  it('removes connections and resyncs on error', async () => {
    useConnections.setState({ connections: [makeConnection({ id: 'a' }), makeConnection({ id: 'b' })] })
    ConnectionService.Delete.mockResolvedValue()
    await st().remove('a')
    expect(st().connections.map((c) => c.id)).toEqual(['b'])

    ConnectionService.Delete.mockRejectedValue(new Error('keychain locked'))
    ConnectionService.List.mockResolvedValue([])
    await st().remove('b')
    expect(st().error).toBe('keychain locked')
    expect(ConnectionService.List).toHaveBeenCalled()
    expect(st().connections).toEqual([])
  })

  it('duplicates connections', async () => {
    useConnections.setState({ connections: [makeConnection({ id: 'a', name: 'A' })] })
    ConnectionService.Duplicate.mockResolvedValueOnce(makeConnection({ id: 'b', name: 'A copy' }))
    const copy = await st().duplicate('a')
    expect(copy?.id).toBe('b')
    expect(st().connections.map((c) => c.name)).toEqual(['A', 'A copy'])

    ConnectionService.Duplicate.mockRejectedValueOnce(new Error('nope'))
    expect(await st().duplicate('a')).toBeNull()
    expect(st().error).toBe('nope')
  })

  it('tests connections', async () => {
    ConnectionService.Test.mockResolvedValue('MySQL 8.0.36')
    await expect(st().test(makeConnection(), NO_SECRETS)).resolves.toBe('MySQL 8.0.36')
    expect(ConnectionService.Test).toHaveBeenCalledWith(expect.objectContaining({ id: 'c1' }), NO_SECRETS)
  })

  it('connects and disconnects', async () => {
    ConnectionService.Connect.mockResolvedValue(makeSession())
    const p = st().connect('c1')
    expect(st().connectingId).toBe('c1')
    expect(await p).toBe(true)
    expect(st().session?.serverVersion).toBe('PostgreSQL 16.4')
    expect(st().connectingId).toBeNull()

    ConnectionService.Disconnect.mockResolvedValue()
    await st().disconnect()
    expect(ConnectionService.Disconnect).toHaveBeenCalledWith('s1')
    expect(st().session).toBeNull()

    // No session: nothing to do.
    await st().disconnect()
    expect(ConnectionService.Disconnect).toHaveBeenCalledTimes(1)
  })

  it('reports connect and disconnect errors', async () => {
    ConnectionService.Connect.mockRejectedValue(new Error('password authentication failed'))
    expect(await st().connect('c1')).toBe(false)
    expect(st().error).toBe('password authentication failed')
    expect(st().session).toBeNull()

    useConnections.setState({ session: makeSession() })
    ConnectionService.Disconnect.mockRejectedValue(new Error('session not found'))
    await st().disconnect()
    expect(st().session).toBeNull()
    expect(st().error).toBe('session not found')
  })
})

describe('SSH host keys on connect', () => {
  const info = { kind: 'hostKeyUnknown', host: 'b', address: 'b:22', keyType: 'ssh-ed25519', fingerprintSHA256: 'SHA256:x', message: 'unknown host' }

  beforeEach(() => {
    resetBindings()
    useConnections.setState({ ...initialConnectionsState })
  })

  it('prompts for an unknown key, accepts it and connects again', async () => {
    ConnectionService.Connect.mockRejectedValueOnce({ message: 'unknown host', cause: info }).mockResolvedValueOnce(makeSession())
    expect(await st().connect('c1')).toBe(false)
    expect(st().hostKey).toEqual({ info, connectionId: 'c1', busy: false })
    expect(st().error).toBeNull()
    expect(await st().acceptHostKey()).toBe(true)
    expect(ConnectionService.AcceptHostKey).toHaveBeenCalledWith('b', 'SHA256:x')
    expect(st().session?.id).toBe('s1')
    expect(st().hostKey).toBeNull()
    expect(await st().acceptHostKey()).toBe(false)
  })

  it('reports changed keys as errors and handles accept failures', async () => {
    ConnectionService.Connect.mockRejectedValueOnce({ message: 'WARNING: CHANGED', cause: { ...info, kind: 'hostKeyChanged' } })
    await st().connect('c1')
    expect(st().hostKey).toBeNull()
    expect(st().error).toBe('WARNING: CHANGED')

    ConnectionService.Connect.mockRejectedValueOnce({ message: 'unknown host', cause: info })
    await st().connect('c1')
    st().dismissHostKey()
    expect(st().hostKey).toBeNull()

    ConnectionService.Connect.mockRejectedValueOnce({ message: 'unknown host', cause: info })
    await st().connect('c1')
    ConnectionService.AcceptHostKey.mockRejectedValueOnce(new Error('no pending key'))
    expect(await st().acceptHostKey()).toBe(false)
    expect(st().error).toBe('no pending key')

    await st().trustHostKey(info as never)
    expect(ConnectionService.AcceptHostKey).toHaveBeenLastCalledWith('b', 'SHA256:x')
  })
})

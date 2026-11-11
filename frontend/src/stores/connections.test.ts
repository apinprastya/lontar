import { beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('../../bindings/dbedit/internal/services', () => import('../test/mockBindings'))

import { ConnectionService, DRIVERS, makeConnection, makeSession, resetBindings } from '../test/mockBindings'
import { initialConnectionsState, useConnections } from './connections'

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
    const saved = await st().save(c, 'pw')
    expect(saved.id).toBe('z')
    expect(ConnectionService.Save).toHaveBeenCalledWith(c, 'pw')

    ConnectionService.Save.mockResolvedValueOnce({ ...c, id: 'z', name: 'Aaa' })
    await st().save({ ...c, id: 'z', name: 'Aaa' }, null)
    expect(ConnectionService.Save).toHaveBeenLastCalledWith(expect.objectContaining({ id: 'z' }), null)
    expect(st().connections.map((x) => x.name)).toEqual(['Aaa'])
  })

  it('propagates save errors to the caller', async () => {
    ConnectionService.Save.mockRejectedValue(new Error('name is required'))
    await expect(st().save(makeConnection(), null)).rejects.toThrow('name is required')
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
    await expect(st().test(makeConnection(), null)).resolves.toBe('MySQL 8.0.36')
    expect(ConnectionService.Test).toHaveBeenCalledWith(expect.objectContaining({ id: 'c1' }), null)
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

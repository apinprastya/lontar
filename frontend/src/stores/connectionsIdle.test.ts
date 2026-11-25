import { beforeEach, describe, expect, it, vi } from 'vitest'

const handlers = new Map<string, (data: unknown) => void>()
const off = vi.fn()
vi.mock('../lib/events', () => ({
  onEvent: (name: string, cb: (data: unknown) => void) => {
    handlers.set(name, cb)
    return off
  },
}))
vi.mock('../../bindings/lontar/internal/services', () => import('../test/mockBindings'))

import { makeSession, resetBindings } from '../test/mockBindings'
import { SESSION_RESUMED, SESSION_SUSPENDED, initialConnectionsState, useConnections, watchIdleSessions } from './connections'

const st = () => useConnections.getState()

describe('idle sessions', () => {
  beforeEach(() => {
    resetBindings()
    handlers.clear()
    off.mockClear()
    const a = makeSession({ id: 's1' })
    const b = makeSession({ id: 's2' })
    useConnections.setState({ ...initialConnectionsState, sessions: [a, b], session: a })
  })

  it('tracks suspended and resumed sessions from backend events', () => {
    const stop = watchIdleSessions()
    handlers.get(SESSION_SUSPENDED)!({ sessionId: 's1' })
    handlers.get(SESSION_SUSPENDED)!({ sessionId: 's1' }) // once only
    handlers.get(SESSION_SUSPENDED)!({ sessionId: 's2' })
    expect(st().suspended).toEqual(['s1', 's2'])
    handlers.get(SESSION_RESUMED)!({ sessionId: 's1' })
    expect(st().suspended).toEqual(['s2'])
    stop()
    expect(off).toHaveBeenCalledTimes(2)
  })

  it('ignores unknown sessions and forgets disconnected ones', async () => {
    st().setSuspended('gone', true)
    expect(st().suspended).toEqual([])
    st().setSuspended('s2', true)
    await st().disconnect('s2')
    expect(st().suspended).toEqual([])
  })
})

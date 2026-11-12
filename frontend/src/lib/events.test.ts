import { describe, expect, it, vi } from 'vitest'

const { on, off } = vi.hoisted(() => {
  const off = vi.fn()
  const on = vi.fn((_name: string, cb: (ev: { data: unknown }) => void) => {
    cb({ data: { jobId: 'j', rows: 5 } })
    return off
  })
  return { on, off }
})
vi.mock('@wailsio/runtime', () => ({ Events: { On: on } }))

import { onEvent } from './events'

describe('onEvent', () => {
  it('passes the event data and returns the unsubscribe function', () => {
    const cb = vi.fn()
    const unsub = onEvent('export:progress', cb)
    expect(on).toHaveBeenCalledWith('export:progress', expect.any(Function))
    expect(cb).toHaveBeenCalledWith({ jobId: 'j', rows: 5 })
    unsub()
    expect(off).toHaveBeenCalled()
  })
})

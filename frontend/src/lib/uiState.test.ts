import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  SAVE_DELAY,
  flushUIState,
  getUIJSON,
  getUIState,
  initUIState,
  isBoolean,
  isWidthMap,
  setUIJSON,
  setUIState,
  type UIStateBackend,
} from './uiState'

function fakeBackend(initial: Record<string, string> = {}) {
  return {
    all: vi.fn(async () => initial as Record<string, string | undefined> | null),
    set: vi.fn(async (_k: string, _v: string) => {}),
  } satisfies UIStateBackend
}

afterEach(() => {
  vi.useRealTimers()
  vi.restoreAllMocks()
})

describe('uiState', () => {
  it('keeps values in memory before a backend is connected', () => {
    expect(getUIState('a')).toBeNull()
    setUIState('a', '1')
    expect(getUIState('a')).toBe('1')
    setUIState('a', null)
    expect(getUIState('a')).toBeNull()
  })

  it('loads stored values without overwriting newer in-memory ones', async () => {
    setUIState('b', 'local')
    const b = fakeBackend({ a: '1', b: 'stored' })
    await initUIState(b)
    expect(getUIState('a')).toBe('1')
    expect(getUIState('b')).toBe('local')
    expect(b.set).not.toHaveBeenCalled()
  })

  it('treats a missing or failing backend as empty', async () => {
    await initUIState({ all: async () => null, set: async () => {} })
    expect(getUIState('a')).toBeNull()
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    await initUIState({ all: () => Promise.reject(new Error('boom')), set: async () => {} })
    expect(warn).toHaveBeenCalled()
  })

  it('debounces writes per key', async () => {
    vi.useFakeTimers()
    const b = fakeBackend()
    await initUIState(b)
    setUIState('w', '1')
    setUIState('w', '2')
    setUIState('w', '2') // unchanged: no new write
    setUIState('other', 'x')
    expect(b.set).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(SAVE_DELAY)
    expect(b.set.mock.calls).toEqual([
      ['w', '2'],
      ['other', 'x'],
    ])
    setUIState('w', '')
    await vi.advanceTimersByTimeAsync(SAVE_DELAY)
    expect(b.set).toHaveBeenLastCalledWith('w', '')
  })

  it('flushes pending writes at once and logs failures', async () => {
    vi.useFakeTimers()
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const b = fakeBackend()
    b.set.mockRejectedValueOnce(new Error('disk full'))
    await initUIState(b)
    setUIState('a', '1')
    setUIState('b', '2')
    await flushUIState()
    expect(b.set).toHaveBeenCalledTimes(2)
    expect(warn).toHaveBeenCalledWith('save UI state:', expect.any(Error))
    await vi.advanceTimersByTimeAsync(SAVE_DELAY)
    expect(b.set).toHaveBeenCalledTimes(2) // nothing left pending
  })

  it('reads and writes validated JSON', () => {
    expect(getUIJSON('flag', false, isBoolean)).toBe(false)
    setUIJSON('flag', true)
    expect(getUIJSON('flag', false, isBoolean)).toBe(true)
    setUIState('flag', '"yes"')
    expect(getUIJSON('flag', false, isBoolean)).toBe(false)
    setUIState('flag', '{broken')
    expect(getUIJSON('flag', false, isBoolean)).toBe(false)
    setUIJSON('flag', undefined)
    expect(getUIState('flag')).toBeNull()
  })

  it('validates column width maps', () => {
    expect(isWidthMap({ a: 10, b: 200.5 })).toBe(true)
    expect(isWidthMap({})).toBe(true)
    expect(isWidthMap({ a: 0 })).toBe(false)
    expect(isWidthMap({ a: '10' })).toBe(false)
    expect(isWidthMap({ a: Infinity })).toBe(false)
    expect(isWidthMap([1])).toBe(false)
    expect(isWidthMap(null)).toBe(false)
  })
})

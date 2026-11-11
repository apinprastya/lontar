import { describe, expect, it, vi } from 'vitest'
import type { WorkspaceState } from '../../stores/workspace'
import { handleShortcut } from './shortcuts'

function fakeState(withTab = true) {
  return {
    activeTab: () => (withTab ? { id: 't1' } : null),
    activateIndex: vi.fn(),
    closeTab: vi.fn(),
    refresh: vi.fn(async () => {}),
    toggleFilterBar: vi.fn(),
    toggleDetail: vi.fn(),
  } as unknown as WorkspaceState & Record<string, ReturnType<typeof vi.fn>>
}

const key = (k: string, over: Partial<KeyboardEvent> = {}) => ({ key: k, ctrlKey: true, metaKey: false, shiftKey: false, altKey: false, ...over })

describe('handleShortcut', () => {
  it('maps Mod shortcuts', () => {
    const s = fakeState()
    expect(handleShortcut(key('3'), 'linux', s)).toBe(true)
    expect(s.activateIndex).toHaveBeenCalledWith(2)
    expect(handleShortcut(key('w'), 'linux', s)).toBe(true)
    expect(s.closeTab).toHaveBeenCalledWith('t1')
    expect(handleShortcut(key('R'), 'linux', s)).toBe(true)
    expect(s.refresh).toHaveBeenCalled()
    expect(handleShortcut(key('f'), 'linux', s)).toBe(true)
    expect(s.toggleFilterBar).toHaveBeenCalledWith('t1')
    expect(handleShortcut(key('i'), 'linux', s)).toBe(true)
    expect(s.toggleDetail).toHaveBeenCalled()
    expect(handleShortcut(key('x'), 'linux', s)).toBe(false)
  })

  it('uses Cmd on macOS and ignores other modifiers', () => {
    const s = fakeState()
    expect(handleShortcut(key('w'), 'darwin', s)).toBe(false)
    expect(handleShortcut(key('w', { ctrlKey: false, metaKey: true }), 'darwin', s)).toBe(true)
    expect(handleShortcut(key('w', { shiftKey: true }), 'linux', s)).toBe(false)
    expect(handleShortcut(key('w', { altKey: true }), 'linux', s)).toBe(false)
    expect(handleShortcut(key('w', { ctrlKey: false }), 'linux', s)).toBe(false)
  })

  it('handles a workspace without tabs', () => {
    const s = fakeState(false)
    expect(handleShortcut(key('w'), 'linux', s)).toBe(true)
    expect(s.closeTab).not.toHaveBeenCalled()
    expect(handleShortcut(key('f'), 'linux', s)).toBe(false)
  })
})

describe('handleShortcut with SQL tabs', () => {
  function queryState(running = false) {
    return {
      activeTab: () => ({ id: 'q1', kind: 'query', running }),
      newQueryTab: vi.fn(),
      runQuery: vi.fn(async () => {}),
      cancelQuery: vi.fn(),
      requestEditor: vi.fn(),
      toggleFilterBar: vi.fn(),
      closeTab: vi.fn(),
    } as unknown as WorkspaceState & Record<string, ReturnType<typeof vi.fn>>
  }

  it('opens tabs, runs and stops queries', () => {
    const s = queryState()
    expect(handleShortcut(key('t'), 'linux', s)).toBe(true)
    expect(s.newQueryTab).toHaveBeenCalled()
    expect(handleShortcut(key('Enter'), 'linux', s)).toBe(true)
    expect(s.runQuery).toHaveBeenLastCalledWith('q1', 'current')
    expect(handleShortcut(key('Enter', { shiftKey: true }), 'linux', s)).toBe(true)
    expect(s.runQuery).toHaveBeenLastCalledWith('q1', 'all')
    expect(handleShortcut(key('Enter', { ctrlKey: false, metaKey: true }), 'darwin', s)).toBe(true)
    // Esc only while running.
    expect(handleShortcut(key('Escape', { ctrlKey: false }), 'linux', s)).toBe(false)
    const running = queryState(true)
    expect(handleShortcut(key('Escape', { ctrlKey: false }), 'linux', running)).toBe(true)
    expect(running.cancelQuery).toHaveBeenCalledWith('q1')
    expect(handleShortcut(key('Escape'), 'linux', running)).toBe(false) // Ctrl+Esc
  })

  it('sends Mod+F to the editor search in SQL tabs (no filter bar)', () => {
    const s = queryState()
    expect(handleShortcut(key('f'), 'linux', s)).toBe(true)
    expect(s.requestEditor).toHaveBeenCalledWith('q1', { type: 'search' })
    expect(s.toggleFilterBar).not.toHaveBeenCalled()
  })

  it('ignores run keys outside SQL tabs', () => {
    const s = fakeState()
    expect(handleShortcut(key('Enter'), 'linux', s)).toBe(false)
    expect(handleShortcut(key('Escape', { ctrlKey: false }), 'linux', s)).toBe(false)
    expect(handleShortcut(key('t', { shiftKey: true }), 'linux', s)).toBe(false)
  })
})

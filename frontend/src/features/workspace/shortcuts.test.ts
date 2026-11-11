import { describe, expect, it, vi } from 'vitest'
import type { WorkspaceState } from '../../stores/workspace'
import { handleShortcut, isTextTarget } from './shortcuts'
import { SHORTCUTS, conflicts } from './keymap'

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

describe('editing shortcuts', () => {
  const tableState = (safeMode = 'off') =>
    ({
      activeTab: () => ({ kind: 'table', id: 't1', type: 'table', columns: [{ name: 'id', type: 'int', isPK: true, nullable: false }] }),
      safeMode,
      requestCommit: vi.fn(async () => {}),
      undoEdit: vi.fn(),
      redoEdit: vi.fn(),
      addRow: vi.fn(),
      newQueryTab: vi.fn(),
    }) as unknown as WorkspaceState & Record<string, ReturnType<typeof vi.fn>>

  it('commits, undoes, redoes and inserts in table tabs', () => {
    const s = tableState()
    expect(handleShortcut(key('s'), 'linux', s)).toBe(true)
    expect(s.requestCommit).toHaveBeenCalledWith('t1')
    expect(handleShortcut(key('z'), 'linux', s)).toBe(true)
    expect(s.undoEdit).toHaveBeenCalledWith('t1')
    expect(handleShortcut(key('Z', { shiftKey: true }), 'linux', s)).toBe(true)
    expect(s.redoEdit).toHaveBeenCalledWith('t1')
    expect(handleShortcut(key('n'), 'linux', s)).toBe(true)
    expect(s.addRow).toHaveBeenCalledWith('t1')
    // Mod+Shift+S is not commit.
    expect(handleShortcut(key('s', { shiftKey: true }), 'linux', s)).toBe(false)
  })

  it('leaves undo and insert to text fields, and to read-only tables', () => {
    const s = tableState()
    expect(handleShortcut(key('z'), 'linux', s, true)).toBe(false)
    expect(handleShortcut(key('n'), 'linux', s, true)).toBe(false)
    expect(s.undoEdit).not.toHaveBeenCalled()
    // Commit still works from a field (e.g. the filter bar).
    expect(handleShortcut(key('s'), 'linux', s, true)).toBe(true)
    const ro = tableState('readonly')
    expect(handleShortcut(key('n'), 'linux', ro)).toBe(false)
    expect(ro.addRow).not.toHaveBeenCalled()
  })

  it('does not treat Mod+S/Z as edits in query tabs', () => {
    const s = { ...fakeState(), activeTab: () => ({ kind: 'query', id: 'q1' }), requestCommit: vi.fn(), undoEdit: vi.fn() } as unknown as WorkspaceState &
      Record<string, ReturnType<typeof vi.fn>>
    expect(handleShortcut(key('s'), 'linux', s)).toBe(false)
    expect(handleShortcut(key('z'), 'linux', s)).toBe(false)
    expect(s.requestCommit).not.toHaveBeenCalled()
  })

  it('recognises text targets', () => {
    expect(isTextTarget(null)).toBe(false)
    expect(isTextTarget(window)).toBe(false)
    expect(isTextTarget(document.createElement('input'))).toBe(true)
    expect(isTextTarget(document.createElement('textarea'))).toBe(true)
    expect(isTextTarget(document.createElement('select'))).toBe(true)
    const cm = document.createElement('div')
    cm.className = 'cm-editor'
    const inner = document.createElement('div')
    cm.appendChild(inner)
    expect(isTextTarget(inner)).toBe(true)
    expect(isTextTarget(document.createElement('canvas'))).toBe(false)
  })
})

describe('keymap', () => {
  it('has no conflicting shortcuts', () => {
    expect(conflicts()).toEqual([])
    expect(SHORTCUTS.find((s) => s.id === 'detail')).toMatchObject({ key: 'i', mod: true })
    expect(SHORTCUTS.find((s) => s.id === 'insertRow')).toMatchObject({ key: 'n', mod: true })
  })

  it('detects conflicts across scopes', () => {
    const list = [
      { id: 'a', key: 'i', mod: true, scope: 'global' as const, description: '' },
      { id: 'b', key: 'i', mod: true, scope: 'grid' as const, description: '' },
      { id: 'c', key: 'i', mod: true, shift: true, scope: 'grid' as const, description: '' },
      { id: 'd', key: 'x', mod: false, scope: 'grid' as const, description: '' },
      { id: 'e', key: 'x', mod: false, scope: 'editor' as const, description: '' },
    ]
    expect(conflicts(list)).toEqual([['a', 'b']])
  })
})

describe('structure shortcuts', () => {
  const state = (view: 'data' | 'structure') =>
    ({
      activeTab: () => ({ kind: 'table', id: 't1', type: 'table', view, columns: [{ name: 'id', type: 'int', isPK: true, nullable: false }] }),
      safeMode: 'off',
      toggleTabView: vi.fn(),
      setTabView: vi.fn(),
      toggleFilterBar: vi.fn(),
      addRow: vi.fn(),
    }) as unknown as WorkspaceState & Record<string, ReturnType<typeof vi.fn>>

  it('toggles data / structure with Mod+E in table tabs', () => {
    const s = state('data')
    expect(handleShortcut(key('e'), 'linux', s)).toBe(true)
    expect(s.toggleTabView).toHaveBeenCalledWith('t1')
    expect(handleShortcut(key('e', { shiftKey: true }), 'linux', s)).toBe(false)
    const q = { activeTab: () => ({ kind: 'query', id: 'q1' }) } as unknown as WorkspaceState
    expect(handleShortcut(key('e'), 'linux', q)).toBe(false)
    expect(SHORTCUTS.find((x) => x.id === 'toggleStructure')).toMatchObject({ key: 'e', mod: true, scope: 'global' })
    expect(SHORTCUTS.find((x) => x.id === 'openReference')).toMatchObject({ key: 'o', mod: true, scope: 'grid' })
  })

  it('opens the filter bar in the data view and never inserts rows from the structure view', () => {
    const s = state('structure')
    expect(handleShortcut(key('f'), 'linux', s)).toBe(true)
    expect(s.setTabView).toHaveBeenCalledWith('t1', 'data')
    expect(s.toggleFilterBar).toHaveBeenCalledWith('t1', true)
    expect(handleShortcut(key('n'), 'linux', s)).toBe(false)
    expect(s.addRow).not.toHaveBeenCalled()
  })
})


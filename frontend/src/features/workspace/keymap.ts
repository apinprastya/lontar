/**
 * Every keyboard shortcut of the workspace, for display and for the
 * conflict test. "mod" is Cmd on macOS, Ctrl elsewhere. Scope says where a
 * shortcut is handled: "global" by the window, "grid" by the focused data
 * grid, "editor" by the focused SQL editor (CodeMirror).
 */
export interface Shortcut {
  id: string
  key: string
  mod: boolean
  shift?: boolean
  scope: 'global' | 'grid' | 'editor'
  description: string
}

export const SHORTCUTS: Shortcut[] = [
  { id: 'newQuery', key: 't', mod: true, scope: 'global', description: 'New SQL query tab' },
  { id: 'closeTab', key: 'w', mod: true, scope: 'global', description: 'Close tab' },
  { id: 'switchTab', key: '1-9', mod: true, scope: 'global', description: 'Switch tab' },
  { id: 'refresh', key: 'r', mod: true, scope: 'global', description: 'Refresh' },
  { id: 'filter', key: 'f', mod: true, scope: 'global', description: 'Filter bar / search' },
  { id: 'detail', key: 'i', mod: true, scope: 'global', description: 'Row detail' },
  { id: 'commit', key: 's', mod: true, scope: 'global', description: 'Commit staged changes' },
  { id: 'undo', key: 'z', mod: true, scope: 'global', description: 'Undo staged change' },
  { id: 'redo', key: 'z', mod: true, shift: true, scope: 'global', description: 'Redo staged change' },
  { id: 'insertRow', key: 'n', mod: true, scope: 'global', description: 'Insert row' },
  { id: 'runCurrent', key: 'enter', mod: true, scope: 'global', description: 'Run statement' },
  { id: 'runAll', key: 'enter', mod: true, shift: true, scope: 'global', description: 'Run all' },
  { id: 'stop', key: 'escape', mod: false, scope: 'global', description: 'Stop the running query' },
  { id: 'copy', key: 'c', mod: true, scope: 'grid', description: 'Copy cells as TSV' },
  { id: 'toggleDetail', key: ' ', mod: false, scope: 'grid', description: 'Row detail' },
  { id: 'edit', key: 'enter', mod: false, scope: 'grid', description: 'Edit cell (or double-click, or type)' },
  { id: 'setNull', key: 'n', mod: true, shift: true, scope: 'grid', description: 'Set NULL' },
  { id: 'duplicateRow', key: 'd', mod: true, scope: 'grid', description: 'Duplicate row' },
  { id: 'deleteRow', key: 'delete', mod: false, scope: 'grid', description: 'Mark rows for deletion (also Backspace)' },
]

/** Shortcuts that collide: same key and modifiers in the same or the global scope. */
export function conflicts(list: Shortcut[] = SHORTCUTS): [string, string][] {
  const out: [string, string][] = []
  const sig = (s: Shortcut) => `${s.mod ? 'mod+' : ''}${s.shift ? 'shift+' : ''}${s.key}`
  for (let i = 0; i < list.length; i++) {
    for (let j = i + 1; j < list.length; j++) {
      const a = list[i]
      const b = list[j]
      const overlap = a.scope === b.scope || a.scope === 'global' || b.scope === 'global'
      // A grid shortcut may intentionally re-handle a global one (it is consumed first); the same id is not a conflict.
      if (overlap && sig(a) === sig(b) && a.id !== b.id) out.push([a.id, b.id])
    }
  }
  return out
}

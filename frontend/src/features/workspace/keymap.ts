import { isMac } from '../../lib/mod'

/**
 * Every keyboard shortcut of the workspace, for display, for the command
 * palette and for the conflict test. "mod" is Cmd on macOS, Ctrl elsewhere.
 * Scope says where a shortcut is handled: "global" by the window, "grid" by
 * the focused data grid, "editor" by the focused SQL editor (CodeMirror).
 *
 * Entries with command: true are listed in the command palette
 * (Mod+Shift+P), generated from this list so that commands and shortcuts
 * never drift apart. A command without a shortcut has key ''.
 */
export interface Shortcut {
  id: string
  key: string
  mod: boolean
  shift?: boolean
  scope: 'global' | 'grid' | 'editor'
  description: string
  /** Listed in the command palette. */
  command?: boolean
}

export const SHORTCUTS: Shortcut[] = [
  { id: 'quickOpen', key: 'p', mod: true, scope: 'global', description: 'Open a table or view…', command: true },
  { id: 'commandPalette', key: 'p', mod: true, shift: true, scope: 'global', description: 'Show all commands' },
  { id: 'switchConnection', key: 'k', mod: true, scope: 'global', description: 'Switch connection…', command: true },
  { id: 'settings', key: ',', mod: true, scope: 'global', description: 'Settings…', command: true },
  { id: 'newQuery', key: 't', mod: true, scope: 'global', description: 'New SQL query tab', command: true },
  { id: 'closeTab', key: 'w', mod: true, scope: 'global', description: 'Close tab', command: true },
  { id: 'switchTab', key: '1-9', mod: true, scope: 'global', description: 'Switch tab' },
  { id: 'refresh', key: 'r', mod: true, scope: 'global', description: 'Refresh', command: true },
  { id: 'filter', key: 'f', mod: true, scope: 'global', description: 'Filter bar / search', command: true },
  { id: 'detail', key: 'i', mod: true, scope: 'global', description: 'Row detail', command: true },
  { id: 'commit', key: 's', mod: true, scope: 'global', description: 'Commit staged changes', command: true },
  { id: 'undo', key: 'z', mod: true, scope: 'global', description: 'Undo staged change', command: true },
  { id: 'redo', key: 'z', mod: true, shift: true, scope: 'global', description: 'Redo staged change', command: true },
  { id: 'insertRow', key: 'n', mod: true, scope: 'global', description: 'Insert row', command: true },
  { id: 'toggleStructure', key: 'e', mod: true, scope: 'global', description: 'Switch between data and structure', command: true },
  { id: 'runCurrent', key: 'enter', mod: true, scope: 'global', description: 'Run statement', command: true },
  { id: 'runAll', key: 'enter', mod: true, shift: true, scope: 'global', description: 'Run all', command: true },
  { id: 'stop', key: 'escape', mod: false, scope: 'global', description: 'Stop the running query', command: true },
  { id: 'exportData', key: '', mod: false, scope: 'global', description: 'Export…', command: true },
  { id: 'importCsv', key: '', mod: false, scope: 'global', description: 'Import CSV into this table…', command: true },
  { id: 'toggleTheme', key: '', mod: false, scope: 'global', description: 'Switch between light and dark theme', command: true },
  { id: 'disconnect', key: '', mod: false, scope: 'global', description: 'Disconnect', command: true },
  { id: 'copy', key: 'c', mod: true, scope: 'grid', description: 'Copy cells as TSV' },
  { id: 'toggleDetail', key: ' ', mod: false, scope: 'grid', description: 'Row detail' },
  { id: 'edit', key: 'enter', mod: false, scope: 'grid', description: 'Edit cell (or double-click, or type)' },
  { id: 'setNull', key: 'n', mod: true, shift: true, scope: 'grid', description: 'Set NULL' },
  { id: 'duplicateRow', key: 'd', mod: true, scope: 'grid', description: 'Duplicate row' },
  { id: 'deleteRow', key: 'delete', mod: false, scope: 'grid', description: 'Mark rows for deletion (also Backspace)' },
  { id: 'openReference', key: 'o', mod: true, scope: 'grid', description: 'Open the referenced row (foreign key cell)' },
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
      // Commands without a key have no shortcut to collide.
      if (overlap && a.key && b.key && sig(a) === sig(b) && a.id !== b.id) out.push([a.id, b.id])
    }
  }
  return out
}

const KEY_NAMES: Record<string, string> = { enter: 'Enter', escape: 'Esc', ' ': 'Space', delete: 'Delete' }

/** Display text of a shortcut ("Ctrl+Shift+P", "⇧⌘P"); "" without a key. */
export function shortcutText(s: Pick<Shortcut, 'key' | 'mod' | 'shift'>, platform?: string): string {
  if (!s.key) return ''
  const key = KEY_NAMES[s.key] ?? (s.key.length === 1 ? s.key.toUpperCase() : s.key)
  if (isMac(platform)) return `${s.shift ? '⇧' : ''}${s.mod ? '⌘' : ''}${key}`
  return [s.mod ? 'Ctrl' : '', s.shift ? 'Shift' : '', key].filter(Boolean).join('+')
}

/** The palette's commands, in registry order. */
export function commands(list: Shortcut[] = SHORTCUTS): Shortcut[] {
  return list.filter((s) => s.command)
}

/** A synthetic key event for a shortcut (used to run a command exactly like its keys). */
export function shortcutEvent(s: Pick<Shortcut, 'key' | 'mod' | 'shift'>, platform?: string) {
  const mac = isMac(platform)
  const key = s.key === 'enter' ? 'Enter' : s.key === 'escape' ? 'Escape' : s.key
  return { key, metaKey: s.mod && mac, ctrlKey: s.mod && !mac, shiftKey: Boolean(s.shift), altKey: false }
}

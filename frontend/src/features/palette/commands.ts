import { activeResultView, resultEditView } from '../../stores/queryTabs'
import { resolveTheme, useSettings } from '../../stores/settings'
import { useUpdate } from '../../stores/update'
import { canEdit, tabRequest, type WorkspaceState, type WorkspaceStore } from '../../stores/workspace'
import type { ExportSource } from '../export/ExportDialog'
import { SHORTCUTS, shortcutEvent } from '../workspace/keymap'
import { handleShortcut } from '../workspace/shortcuts'
import type { PaletteMode } from './CommandPalette'

/** What commands need from the workspace view. */
export interface CommandContext {
  platform?: string
  store: WorkspaceStore
  openPalette(mode: PaletteMode): void
  disconnect(): void
}

/** The export the Export command opens for the active tab (null: nothing to export). */
export function exportSourceFor(s: WorkspaceState): ExportSource | null {
  const tab = s.activeTab()
  if (tab?.kind === 'table') {
    if (tab.columns.length === 0) return null
    return { type: 'table', schema: tab.schema, table: tab.table, request: tabRequest(tab) }
  }
  if (tab?.kind === 'query') {
    const view = activeResultView(tab)
    if (!view) return null
    const st = tab.result?.statements.find((x) => x.resultSets.some((r) => r.id === view.set.id))
    return { type: 'query', sql: st?.sql ?? '', columns: view.set.columns, rows: view.rows, title: tab.title }
  }
  return null
}

/** The table the Import command loads into (a base table, not read-only). */
export function importTargetFor(s: WorkspaceState): { schema: string; table: string } | null {
  const tab = s.activeTab()
  if (tab?.kind !== 'table' || tab.type !== 'table' || s.safeMode === 'readonly') return null
  return { schema: tab.schema, table: tab.table }
}

/** Commands that are not plain key shortcuts (or need the view). */
const ACTIONS: Record<string, (ctx: CommandContext) => void> = {
  quickOpen: (ctx) => ctx.openPalette('tables'),
  commandPalette: (ctx) => ctx.openPalette('commands'),
  switchConnection: (ctx) => ctx.openPalette('connections'),
  settings: () => useSettings.getState().setOpen(true),
  exportData: (ctx) => {
    const src = exportSourceFor(ctx.store.getState())
    if (src) ctx.store.getState().requestExport(src)
  },
  importCsv: (ctx) => {
    const t = importTargetFor(ctx.store.getState())
    if (t) ctx.store.getState().requestImport(t)
  },
  toggleTheme: () => {
    const st = useSettings.getState()
    const next = resolveTheme(st.settings.theme, st.systemDark) === 'dark' ? 'light' : 'dark'
    void st.save({ ...st.settings, theme: next })
  },
  disconnect: (ctx) => ctx.disconnect(),
  checkUpdates: () => void useUpdate.getState().check(),
}

/** IDs with their own action (the rest run exactly like their shortcut). */
export const ACTION_IDS = Object.keys(ACTIONS)

/**
 * Runs a palette command: its action, or the same code path as pressing its
 * shortcut (a synthetic key event through handleShortcut), so a command and
 * its keys cannot behave differently.
 */
export function runCommand(id: string, ctx: CommandContext): boolean {
  const action = ACTIONS[id]
  if (action) {
    action(ctx)
    return true
  }
  const sc = SHORTCUTS.find((x) => x.id === id && x.key)
  if (!sc) return false
  return handleShortcut(shortcutEvent(sc, ctx.platform), ctx.platform, ctx.store.getState(), false)
}

const TABLE_COMMANDS = new Set(['commit', 'undo', 'redo', 'toggleStructure', 'filter'])
/** Commands an editable query result supports too. */
const EDIT_COMMANDS = new Set(['commit', 'undo', 'redo', 'insertRow'])
const QUERY_COMMANDS = new Set(['runCurrent', 'runAll'])

/** Whether a command can do something in the current state (palette greys it out otherwise). */
export function commandAvailable(id: string, s: WorkspaceState): boolean {
  const tab = s.activeTab()
  if (EDIT_COMMANDS.has(id) && tab?.kind === 'query') return Boolean(resultEditView(tab, s.safeMode)?.editable)
  if (TABLE_COMMANDS.has(id)) return tab?.kind === 'table' || (id === 'filter' && tab?.kind === 'query')
  if (QUERY_COMMANDS.has(id)) return tab?.kind === 'query'
  switch (id) {
    case 'stop':
      return tab?.kind === 'query' && tab.running
    case 'insertRow':
      return tab?.kind === 'table' && tab.view !== 'structure' && canEdit(tab, s.safeMode)
    case 'closeTab':
      return tab !== null
    case 'exportData':
      return exportSourceFor(s) !== null
    case 'importCsv':
      return importTargetFor(s) !== null
  }
  return true
}

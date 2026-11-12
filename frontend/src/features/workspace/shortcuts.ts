import { isModPressed, type ModifierEvent } from '../../lib/mod'
import { canEdit, type WorkspaceState } from '../../stores/workspace'
import { resultEditView } from '../../stores/queryTabs'

/** Whether keys go to a text field (undo/redo/insert then stay native). */
export function isTextTarget(target: EventTarget | null | undefined): boolean {
  if (!target || typeof (target as Element).tagName !== 'string') return false
  const el = target as HTMLElement
  return /^(INPUT|TEXTAREA|SELECT)$/.test(el.tagName) || el.isContentEditable || Boolean(el.closest?.('.cm-editor'))
}

/**
 * Handles a workspace keyboard shortcut; returns true when the event was
 * consumed (the caller then prevents the default action, e.g. reload on
 * Mod+R).
 *
 *   Mod+T new SQL tab · Mod+W close tab · Mod+1..9 switch tab ·
 *   Mod+R refresh · Mod+I row detail ·
 *   Mod+F filter bar (table tab) or search (SQL tab) ·
 *   Mod+Enter run statement/selection · Mod+Shift+Enter run all ·
 *   Esc stop a running query ·
 *   table tabs and editable query results: Mod+S commit · Mod+Z /
 *   Mod+Shift+Z undo/redo staged edits ·
 *   Mod+N insert row · Mod+E data / structure (see keymap.ts for the full list)
 *
 * Keys handled by the focused SQL editor itself (CodeMirror marks them
 * defaultPrevented) never reach this function; the caller filters them.
 */
export function handleShortcut(e: ModifierEvent, platform: string | undefined, s: WorkspaceState, inText = false): boolean {
  const tab = s.activeTab()
  if (e.key === 'Escape' && !e.metaKey && !e.ctrlKey && !e.altKey && !e.shiftKey) {
    if (tab?.kind !== 'query' || !tab.running) return false
    s.cancelQuery(tab.id)
    return true
  }
  if (!isModPressed(e, platform) || e.altKey) return false
  const key = e.key.toLowerCase()
  if (key === 'enter') {
    if (tab?.kind !== 'query') return false
    void s.runQuery(tab.id, e.shiftKey ? 'all' : 'current')
    return true
  }
  if (tab?.kind === 'query' && tab.edit && resultEditView(tab, s.safeMode)?.editable) {
    // An editable result: the same keys as a table tab (the SQL editor
    // keeps its own undo and typing).
    if (key === 's' && !e.shiftKey) {
      void s.requestCommit(tab.id)
      return true
    }
    if (!inText && key === 'z') {
      if (e.shiftKey) s.redoEdit(tab.id)
      else s.undoEdit(tab.id)
      return true
    }
    if (!inText && key === 'n' && !e.shiftKey) {
      s.addRow(tab.id)
      return true
    }
  }
  if (tab?.kind === 'table') {
    if (key === 's' && !e.shiftKey) {
      void s.requestCommit(tab.id)
      return true
    }
    if (!inText && key === 'z') {
      if (e.shiftKey) s.redoEdit(tab.id)
      else s.undoEdit(tab.id)
      return true
    }
    if (key === 'e' && !e.shiftKey) {
      s.toggleTabView(tab.id)
      return true
    }
    if (!inText && key === 'n' && !e.shiftKey && tab.view !== 'structure' && canEdit(tab, s.safeMode)) {
      s.addRow(tab.id)
      return true
    }
  }
  if (e.shiftKey) return false
  if (/^[1-9]$/.test(key)) {
    s.activateIndex(Number(key) - 1)
    return true
  }
  switch (key) {
    case 't':
      s.newQueryTab()
      return true
    case 'w':
      if (tab) s.closeTab(tab.id)
      return true
    case 'r':
      void s.refresh()
      return true
    case 'f':
      if (!tab) return false
      if (tab.kind === 'query') s.requestEditor(tab.id, { type: 'search' })
      else if (tab.view === 'structure') {
        // The filter bar belongs to the data view.
        s.setTabView(tab.id, 'data')
        s.toggleFilterBar(tab.id, true)
      } else s.toggleFilterBar(tab.id)
      return true
    case 'i':
      s.toggleDetail()
      return true
  }
  return false
}

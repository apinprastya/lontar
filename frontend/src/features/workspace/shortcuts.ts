import { isModPressed, type ModifierEvent } from '../../lib/mod'
import type { WorkspaceState } from '../../stores/workspace'

/**
 * Handles a workspace keyboard shortcut; returns true when the event was
 * consumed (the caller then prevents the default action, e.g. reload on
 * Mod+R).
 *
 *   Mod+T new SQL tab · Mod+W close tab · Mod+1..9 switch tab ·
 *   Mod+R refresh · Mod+I row detail ·
 *   Mod+F filter bar (table tab) or search (SQL tab) ·
 *   Mod+Enter run statement/selection · Mod+Shift+Enter run all ·
 *   Esc stop a running query
 *
 * Keys handled by the focused SQL editor itself (CodeMirror marks them
 * defaultPrevented) never reach this function; the caller filters them.
 */
export function handleShortcut(e: ModifierEvent, platform: string | undefined, s: WorkspaceState): boolean {
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
      else s.toggleFilterBar(tab.id)
      return true
    case 'i':
      s.toggleDetail()
      return true
  }
  return false
}
